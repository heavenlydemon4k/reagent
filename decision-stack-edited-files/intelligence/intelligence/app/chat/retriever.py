"""
Cross-source context retrieval for the Chat service.

Unlike Consultation (which is scoped to a single thread), Chat retrieves
context from multiple sources:
    - Relationship graph (Neo4j): contacts mentioned in the message
    - Recent threads (Qdrant): semantic similarity to message content
    - Calendar: upcoming events
    - Linked card (if any): that card's chunks exclusively
"""

from __future__ import annotations

import logging
from datetime import datetime, timedelta
from typing import Any, Dict, List, Optional

from intelligence.app.compression.embedder import Embedder
from intelligence.app.compression.store import ChunkStore
from intelligence.app.chat.models import Conversation
from intelligence.core.config import get_settings

logger = logging.getLogger(__name__)


class ContextRetriever:
    """Retrieves and aggregates context from multiple sources for chat."""

    def __init__(
        self,
        chunk_store: ChunkStore,
        embedder: Embedder,
        neo4j_client,
        calendar_client=None,
        calendar_service=None,
        cross_encoder=None,
    ) -> None:
        self.chunks = chunk_store
        self.embedder = embedder
        self.neo4j = neo4j_client
        self.calendar = calendar_client
        self.calendar_service = calendar_service
        self.reranker = cross_encoder

    async def retrieve(
        self,
        user_id: str,
        conversation: Conversation,
        message: str,
        linked_card_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        """
        Retrieve cross-source context for a chat message.

        Args:
            user_id: Multi-tenancy user identifier.
            conversation: The current conversation (for linked context sources).
            message: The user's message to find relevant context for.
            linked_card_id: Optional card ID to scope retrieval exclusively.

        Returns:
            Dict with keys: contacts, threads, events, citations, chunks.
        """
        result: Dict[str, Any] = {
            "contacts": [],
            "threads": [],
            "events": [],
            "citations": [],
            "chunks": [],
        }

        # 1. If linked to a card, retrieve that card's chunks exclusively
        if linked_card_id:
            card_chunks = await self._retrieve_linked_card_chunks(
                user_id, linked_card_id, message
            )
            result["chunks"] = card_chunks
            result["citations"] = self._chunks_to_citations(card_chunks)
            # In linked-card mode, skip other sources for focus
            return result

        # 2. Retrieve from relationship graph — contacts mentioned
        try:
            contacts = await self._extract_contacts(user_id, message)
            result["contacts"] = contacts
        except Exception as exc:
            logger.debug("Contact extraction failed: %s", exc)

        # 3. Semantic search across recent threads
        try:
            thread_chunks = await self._retrieve_thread_chunks(user_id, message)
            result["chunks"] = thread_chunks
            result["citations"] = self._chunks_to_citations(thread_chunks)
        except Exception as exc:
            logger.debug("Thread chunk retrieval failed: %s", exc)

        # 4. Calendar context — upcoming events
        try:
            events = await self._retrieve_calendar_events(user_id)
            result["events"] = events
        except Exception as exc:
            logger.debug("Calendar retrieval failed: %s", exc)

        # 5. Calendar context — fetch when scheduling intent detected
        if self.calendar_service:
            try:
                # Check if message has scheduling intent
                has_scheduling = await self.calendar_service.detect_scheduling_intent(message)
                if has_scheduling:
                    calendar_ctx = await self.calendar_service.get_calendar_context_for_card(
                        user_id=user_id,
                        card_text=message,
                    )
                    result["calendar"] = calendar_ctx

                    # Also check for proposed time conflicts
                    deadline = self.calendar_service._ner.extract_deadline(message)
                    if deadline:
                        conflict = await self.calendar_service.check_conflicts(
                            user_id, deadline, deadline + timedelta(hours=1)
                        )
                        result["calendar_conflicts"] = conflict
            except Exception as exc:
                logger.warning("Calendar context fetch failed (non-blocking): %s", exc)

        logger.debug(
            "Context retrieved: %d contacts, %d chunks, %d events",
            len(result["contacts"]),
            len(result["chunks"]),
            len(result["events"]),
        )
        return result

    # ------------------------------------------------------------------
    # Source-specific retrieval
    # ------------------------------------------------------------------

    async def _retrieve_linked_card_chunks(
        self,
        user_id: str,
        card_id: str,
        message: str,
        top_k: int = 5,
    ) -> List:
        """Retrieve chunks scoped exclusively to a linked card."""
        query_vector = await self.embedder.embed_single(message)
        chunks = await self.chunks.search_similar(
            query_vector=query_vector,
            user_id=user_id,
            thread_id=card_id,
            limit=10,
        )
        # Re-rank if cross-encoder available
        if self.reranker and len(chunks) > 1:
            pairs = [(message, c.content_snippet or c.content) for c in chunks]
            scores = self.reranker.predict(pairs)
            scored = list(zip(chunks, scores))
            scored.sort(key=lambda x: x[1], reverse=True)
            chunks = [c for c, _ in scored[:top_k]]
        else:
            chunks = chunks[:top_k]
        return chunks

    async def _retrieve_thread_chunks(
        self,
        user_id: str,
        message: str,
        top_k: int = 5,
    ) -> List:
        """Semantic search across all user threads for relevant chunks."""
        query_vector = await self.embedder.embed_single(message)
        chunks = await self.chunks.search_similar(
            query_vector=query_vector,
            user_id=user_id,
            thread_id=None,  # cross-thread search
            limit=10,
        )
        # Re-rank if available
        if self.reranker and len(chunks) > 1:
            pairs = [(message, c.content_snippet or c.content) for c in chunks]
            scores = self.reranker.predict(pairs)
            scored = list(zip(chunks, scores))
            scored.sort(key=lambda x: x[1], reverse=True)
            chunks = [c for c, _ in scored[:top_k]]
        else:
            chunks = chunks[:top_k]
        return chunks

    async def _extract_contacts(self, user_id: str, message: str) -> List[Dict[str, Any]]:
        """
        Extract contacts mentioned in the message via Neo4j.

        Uses a simple keyword match against known contact names/emails
        in the user's relationship graph.
        """
        # Extract potential name tokens (simple heuristic: capitalized words > 3 chars)
        import re
        tokens = set(re.findall(r"[A-Z][a-z]{2,}", message))
        if not tokens:
            return []

        # Query Neo4j for matching contacts
        query = """
        MATCH (u:User {id: $user_id})-[:KNOWS]->(c:Contact)
        WHERE ANY(token IN $tokens WHERE
            c.name CONTAINS token OR c.email CONTAINS token)
        RETURN c.id AS id, c.name AS name, c.email AS email, c.company AS company
        LIMIT 10
        """
        try:
            records = await self.neo4j.run_read(query, {"user_id": user_id, "tokens": list(tokens)})
            return [dict(r) for r in records]
        except Exception:
            # Neo4j may not have the schema yet; return empty
            return []

    async def _retrieve_calendar_events(
        self,
        user_id: str,
        days_ahead: int = 7,
    ) -> List[Dict[str, Any]]:
        """
        Retrieve upcoming calendar events for the user.

        If no calendar client is configured, returns an empty list.
        """
        if self.calendar is None:
            return []

        now = datetime.utcnow()
        end = now + timedelta(days=days_ahead)
        try:
            events = await self.calendar.list_events(user_id, start=now, end=end)
            return events
        except Exception:
            return []

    @staticmethod
    def _chunks_to_citations(chunks: List) -> List[Dict[str, Any]]:
        """Convert Chunk objects to citation dicts for response metadata."""
        from intelligence.app.consultation.models import Citation

        citations = []
        for c in chunks:
            try:
                citations.append(
                    Citation(
                        chunk_id=c.chunk_id,
                        thread_id=c.thread_id,
                        email_id=c.email_id,
                        sender_email=c.sender_email,
                        content_snippet=c.content_snippet or c.content[:200],
                        timestamp=c.timestamp,
                    ).model_dump()
                )
            except Exception:
                # If Citation import fails, use plain dict
                citations.append(
                    {
                        "chunk_id": str(c.chunk_id),
                        "thread_id": str(c.thread_id),
                        "email_id": str(c.email_id),
                        "sender_email": c.sender_email,
                        "content_snippet": c.content_snippet or c.content[:200],
                        "timestamp": c.timestamp.isoformat(),
                    }
                )
        return citations
