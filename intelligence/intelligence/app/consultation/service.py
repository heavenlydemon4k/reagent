"""
Consultation Service — per-card Q&A (max 10 turns).

Each consultation is scoped to a single card (thread) and enforces a hard
limit of 10 turns tracked in Redis. Answers are grounded exclusively in the
retrieved email chunks with full citation metadata.
"""

from __future__ import annotations

import logging
import time
from typing import Optional

from jinja2 import Environment, FileSystemLoader, select_autoescape

from intelligence.app.consultation.models import Citation, ConsultRequest, ConsultResponse
from intelligence.app.consultation.retriever import ChunkRetriever
from intelligence.core.config import get_settings
from intelligence.core.llm_client import LLMClient, GenerationResult

logger = logging.getLogger(__name__)

# Jinja2 env for loading prompt templates
_jinja_env = Environment(
    loader=FileSystemLoader("/mnt/agents/output/intelligence/intelligence/core/prompt_templates"),
    autoescape=select_autoescape(),
)


class ConsultationService:
    """Q&A against a specific card's thread chunks. Max 10 turns."""

    def __init__(
        self,
        llm: LLMClient,
        retriever: ChunkRetriever,
        redis,
    ) -> None:
        self.llm = llm
        self.retriever = retriever
        self.redis = redis
        self.max_turns = get_settings().max_consultation_turns

    async def ask(
        self,
        card_id: str,
        user_id: str,
        question: str,
    ) -> ConsultResponse:
        """
        Process a consultation question against a specific card.

        Steps:
            1. Check turn count in Redis; reject if >= max_turns.
            2. Retrieve relevant chunks via vector search + re-ranking.
            3. Build prompt from consultation template + chunks.
            4. Generate answer via LLM (Claude 3.5 Sonnet).
            5. Increment turn count in Redis.
            6. Return answer with citations.

        Args:
            card_id: The card/thread being consulted on.
            user_id: Multi-tenancy user identifier.
            question: The user's question.

        Returns:
            ConsultResponse with answer, citations, and turn metadata.
        """
        t0 = time.perf_counter()
        turns_key = f"consultation:turns:{card_id}:{user_id}"

        # 1. Check turn count
        current_turns = await self._get_turn_count(turns_key)
        if current_turns >= self.max_turns:
            logger.warning(
                "Consultation turn limit reached for card=%s user=%s (%d/%d)",
                card_id,
                user_id,
                current_turns,
                self.max_turns,
            )
            return ConsultResponse(
                answer="Maximum consultation turns reached for this card. Please start a new consultation or use the general chat for follow-up questions.",
                card_id=card_id,
                turns_used=current_turns,
                turns_remaining=0,
            )

        # 2. Retrieve relevant chunks
        try:
            chunks = await self.retriever.retrieve(
                query=question,
                thread_id=card_id,
                user_id=user_id,
                top_k=5,
            )
        except Exception as exc:
            logger.error("Chunk retrieval failed: %s", exc, exc_info=True)
            chunks = []

        # 3. Build prompt
        prompt = self._build_prompt(question, chunks)

        # 4. Generate answer via LLM
        try:
            result: GenerationResult = await self.llm.generate(
                prompt=prompt,
                system=self._system_prompt(),
                temperature=0.3,
                max_tokens=2000,
            )
        except Exception as exc:
            logger.error("LLM generation failed: %s", exc, exc_info=True)
            return ConsultResponse(
                answer="I'm sorry, I encountered an error generating a response. Please try again.",
                card_id=card_id,
                turns_used=current_turns,
                turns_remaining=self.max_turns - current_turns,
            )

        # 5. Increment turn count
        new_turns = await self._increment_turns(turns_key)
        latency_ms = int((time.perf_counter() - t0) * 1000)

        # Build citations from chunks
        citations = [
            Citation(
                chunk_id=c.chunk_id,
                thread_id=c.thread_id,
                email_id=c.email_id,
                sender_email=c.sender_email,
                content_snippet=c.content_snippet or c.content[:200],
                timestamp=c.timestamp,
            )
            for c in chunks
        ]

        logger.info(
            "Consultation answered card=%s turns=%d/%d latency=%dms",
            card_id,
            new_turns,
            self.max_turns,
            latency_ms,
        )

        return ConsultResponse(
            answer=result.text,
            card_id=card_id,
            turns_used=new_turns,
            turns_remaining=self.max_turns - new_turns,
            citations=citations,
            model_used=result.model or self.llm.model_name,
            tokens_input=result.tokens_input,
            tokens_output=result.tokens_output,
            latency_ms=latency_ms,
        )

    async def get_turns_remaining(self, card_id: str, user_id: str) -> int:
        """
        Return the number of turns remaining for a consultation.

        Args:
            card_id: The card/thread identifier.
            user_id: Multi-tenancy user identifier.

        Returns:
            Number of turns remaining (0 if at or over limit).
        """
        turns_key = f"consultation:turns:{card_id}:{user_id}"
        current = await self._get_turn_count(turns_key)
        return max(0, self.max_turns - current)

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    async def _get_turn_count(self, key: str) -> int:
        """Fetch current turn count from Redis (defaults to 0)."""
        try:
            val = await self.redis.get(key)
            return int(val) if val is not None else 0
        except Exception:
            logger.warning("Failed to read turn count for key=%s", key)
            return 0

    async def _increment_turns(self, key: str) -> int:
        """Atomically increment turn count in Redis. Returns new value."""
        try:
            new_val = await self.redis.incr(key)
            # Set expiry to 30 days so old consultations auto-cleanup
            await self.redis.expire(key, 30 * 24 * 3600)
            return int(new_val)
        except Exception:
            logger.warning("Failed to increment turn count for key=%s", key)
            return self.max_turns  # fail-closed

    def _system_prompt(self) -> str:
        """Return the system prompt for consultation."""
        return (
            "You are an expert email assistant. Answer the user's question "
            "using ONLY the provided email context. If the context does not "
            "contain enough information to answer confidently, say so. "
            "Cite specific emails by sender and date when possible. "
            "Be concise and direct. Do not hallucinate information."
        )

    def _build_prompt(self, question: str, chunks: list) -> str:
        """
        Render the consultation prompt using the Jinja2 template.

        Falls back to inline template if the file is not found.
        """
        try:
            template = _jinja_env.get_template("consultation.jinja2")
        except Exception:
            # Fallback inline template
            template = _jinja_env.from_string(
                "{%- if chunks -%}\n"
                "Context from emails:\n"
                "{%- for chunk in chunks %}\n"
                "---\n"
                "From: {{ chunk.sender_email }}\n"
                "Date: {{ chunk.timestamp.strftime('%Y-%m-%d %H:%M') }}\n"
                "Content: {{ chunk.content_snippet or chunk.content[:300] }}\n"
                "{%- endfor %}\n"
                "{%- endif %}\n\n"
                "Question: {{ question }}\n\n"
                "Answer:"
            )
        return template.render(question=question, chunks=chunks)
