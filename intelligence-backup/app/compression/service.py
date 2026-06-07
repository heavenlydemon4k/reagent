"""CompressionService — the main engine.

Transforms raw email threads into decision cards. This IS the product.
Without this service, there are no cards.

Pipeline:
1. Fetch chunks for thread from Qdrant (parallel with context)
2. Fetch relationship context from Neo4j (parallel with chunks + calendar)
3. Fetch calendar context from PostgreSQL (parallel with chunks + Neo4j)
4. Select generation tier based on thread complexity
5. Check Redis cache (skip generation if hit)
6. Render Jinja2 prompt (tier-specific template)
7. Generate card via LLM (tiered: Haiku / Sonnet / Hierarchical)
8. Parse JSON response
9. Citation verification (zero hallucination tolerance)
10. Retry loop: max 3 attempts on verification failure
11. On 3 failures: route to manual review queue
12. Compute urgency score
13. Persist card to PostgreSQL
14. Publish CreateCard event to NATS
"""
from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import time
from typing import Any, Optional
from uuid import UUID

import jinja2

from intelligence.app.compression.chunker import Chunk
from intelligence.app.compression.context_builder import ContextBuilder
from intelligence.app.compression.hierarchical import HierarchicalSummarizer
from intelligence.app.compression.models import (
    CardContext,
    CardResult,
    ChunkCitation,
    DecisionCard,
    UrgencySignals,
    VerificationResult,
)
from intelligence.app.compression.store import ChunkStore
from intelligence.app.compression.verifier import CitationVerifier
from intelligence.core.fallback_chain import FallbackChain
from intelligence.core.redis_client import get_redis
from intelligence.infra.db.neo4j_client import Neo4jClient
from intelligence.infra.db.postgres_client import PostgresClient
from intelligence.infra.queue.nats_client import NATSClient

logger = logging.getLogger(__name__)


class CompressionService:
    """Transforms raw email threads into decision cards."""

    # ------------------------------------------------------------------
    # System prompt — injected into every LLM call
    # ------------------------------------------------------------------

    SYSTEM_PROMPT = (
        "You are Decision Stack's intelligence engine. Your job is to read email "
        "thread data and produce a decision card that helps the user make a decision "
        "quickly.\n\n"
        "CRITICAL RULES:\n"
        "1. Every claim MUST cite a chunk_id from the provided chunks. No exceptions.\n"
        "2. If you cannot verify a claim against the chunks, OMIT IT. Do not guess.\n"
        '3. "they_want" must be a single sentence, max 280 characters.\n'
        '4. "need_from_user" must be the explicit gap only the user can fill. Do not '
        "infer tacit knowledge (margins, risk, relationship history).\n"
        "5. Respond with valid JSON only. No markdown fences, no commentary.\n"
    )

    SYSTEM_PROMPT_CONDENSED = (
        "You are Decision Stack's intelligence engine. Read the email thread "
        "and produce a decision card as JSON.\n\n"
        "RULES:\n"
        "1. Every claim MUST cite a chunk_id. No exceptions.\n"
        "2. If you cannot verify a claim, OMIT IT.\n"
        '3. "they_want": single sentence, max 280 chars.\n'
        '4. "need_from_user": explicit gap only the user can fill.\n'
        "5. Respond with valid JSON only. No markdown.\n"
    )

    # Retry configuration
    _MAX_RETRIES: int = 3

    # Scheduling keywords for tier selection
    _SCHEDULING_KEYWORDS: frozenset[str] = frozenset(
        [
            "schedule",
            "meeting",
            "calendar",
            "monday",
            "friday",
            "next week",
            "tomorrow",
            "zoom",
            "call",
            "appointment",
        ]
    )

    def __init__(
        self,
        llm: FallbackChain,
        chunk_store: ChunkStore,
        neo4j: Neo4jClient,
        postgres: PostgresClient,
        nats: NATSClient,
        prompt_template: str,
        condensed_prompt_template: Optional[str] = None,
        summarizer: Optional[HierarchicalSummarizer] = None,
    ) -> None:
        self.llm = llm
        self.chunks = chunk_store
        self.neo4j = neo4j
        self.postgres = postgres
        self.nats = nats
        self.prompt_template = prompt_template
        self._jinja = jinja2.Environment(loader=jinja2.BaseLoader())
        self._template = self._jinja.from_string(prompt_template)
        self._condensed_template = (
            self._jinja.from_string(condensed_prompt_template)
            if condensed_prompt_template
            else self._template
        )
        self._context_builder = ContextBuilder(neo4j, postgres)
        self._summarizer = summarizer

    # ------------------------------------------------------------------
    # Tier selection
    # ------------------------------------------------------------------

    def _select_generation_tier(self, thread_id: str, chunks: list[Chunk]) -> str:
        """Select LLM tier based on thread complexity.

        Tier 1 (fast): < 5 chunks, no scheduling keywords -> Haiku
        Tier 2 (standard): 5-20 chunks -> Sonnet
        Tier 3 (hierarchical): > 20 chunks -> use pre-computed summary
        """
        chunk_count = len(chunks)

        if chunk_count < 5:
            combined = " ".join(c.content for c in chunks).lower()
            has_scheduling = any(
                kw in combined for kw in self._SCHEDULING_KEYWORDS
            )
            if not has_scheduling:
                logger.info(
                    "Tier selected: fast (Haiku) for thread=%s (%d chunks, no scheduling)",
                    thread_id,
                    chunk_count,
                )
                return "fast"  # Haiku

        if chunk_count <= 20:
            logger.info(
                "Tier selected: standard (Sonnet) for thread=%s (%d chunks)",
                thread_id,
                chunk_count,
            )
            return "standard"  # Sonnet

        logger.info(
            "Tier selected: hierarchical (summary + Sonnet) for thread=%s (%d chunks)",
            thread_id,
            chunk_count,
        )
        return "hierarchical"  # Summary + Sonnet

    # ------------------------------------------------------------------
    # Cache helpers
    # ------------------------------------------------------------------

    def _chunk_hash(self, chunks: list[Chunk]) -> str:
        """Hash chunk contents for cache key versioning."""
        combined = "".join(c.content for c in chunks)
        return hashlib.sha256(combined.encode()).hexdigest()[:16]

    async def _get_cached_card(self, cache_key: str) -> Optional[CardResult]:
        """Check Redis for a cached card result."""
        try:
            redis = await get_redis()
            cached = await redis.get(cache_key)
            if cached:
                return CardResult.parse_raw(cached)
        except Exception as exc:
            logger.warning("Card cache read failed (non-blocking): %s", exc)
        return None

    async def _cache_card(
        self, cache_key: str, result: CardResult, ttl: int
    ) -> None:
        """Store a card result in Redis with TTL."""
        try:
            redis = await get_redis()
            await redis.setex(cache_key, ttl, result.json())
            logger.debug("Cached card result under key=%s (ttl=%ds)", cache_key, ttl)
        except Exception as exc:
            logger.warning("Card cache write failed (non-blocking): %s", exc)

    # ------------------------------------------------------------------
    # Main entry point
    # ------------------------------------------------------------------

    async def generate_card(
        self,
        user_id: str,
        thread_id: str,
        raw_email_ids: list[str],
    ) -> CardResult:
        """Generate a decision card from a raw email thread.

        Args:
            user_id: Mailbox owner UUID (string).
            thread_id: Email thread UUID (string).
            raw_email_ids: List of email UUIDs in the thread (for provenance).

        Returns:
            CardResult: contains the card, verification status, retry count,
            and latency metrics.
        """
        overall_start = time.monotonic()
        logger.info(
            "generate_card start: user=%s thread=%s emails=%d",
            user_id,
            thread_id,
            len(raw_email_ids),
        )

        # ---- 1-3. Fetch chunks + context IN PARALLEL ----
        chunks_task = self.chunks.get_chunks_by_thread(thread_id, user_id)
        rel_task = self._get_relationship_context(user_id, thread_id)
        cal_task = self._get_calendar_context(user_id)

        chunks, rel_ctx, cal_ctx = await asyncio.gather(
            chunks_task, rel_task, cal_task
        )

        if not chunks:
            logger.error("No chunks found for thread %s -- aborting", thread_id)
            return CardResult(
                card=None,
                citations_verified=False,
                retry_count=0,
                routed_to_manual_review=True,
                routing_reason="No chunks found for thread",
            )
        logger.debug("Fetched %d chunks for thread %s", len(chunks), thread_id)

        # ---- 4. Select generation tier ----
        tier = self._select_generation_tier(thread_id, chunks)

        # ---- 5. Check Redis cache ----
        cache_key = f"card:{thread_id}:v{self._chunk_hash(chunks)}"
        cached = await self._get_cached_card(cache_key)
        if cached:
            logger.info("Card cache HIT for thread %s (tier=%s)", thread_id, tier)
            cached.latency_ms = int((time.monotonic() - overall_start) * 1000)
            return cached
        logger.info("Card cache MISS for thread %s (tier=%s)", thread_id, tier)

        # ---- 6-11. LLM generation + citation verification with retry loop ----
        card_data: dict[str, Any] | None = None
        verification: VerificationResult | None = None
        retries = 0
        model_used = ""
        tokens_used = 0

        for attempt in range(1, self._MAX_RETRIES + 1):
            retries = attempt - 1
            logger.info("Generation attempt %d/%d (tier=%s)", attempt, self._MAX_RETRIES, tier)

            # Build tier-specific prompt
            if tier == "fast":
                prompt = self._render_prompt_condensed(chunks, rel_ctx, cal_ctx)
            elif tier == "hierarchical":
                prompt = await self._render_prompt_hierarchical(
                    user_id, thread_id, chunks, rel_ctx, cal_ctx
                )
            else:
                prompt = self._render_prompt(chunks, rel_ctx, cal_ctx)

            # Generate card via LLM (tier-specific model)
            try:
                if tier == "fast":
                    # Use Haiku (fallback tier) for fast generation
                    result = await self.llm.generate(
                        prompt,
                        system=self.SYSTEM_PROMPT_CONDENSED,
                        temperature=0.2,
                        max_tokens=1200,
                        user_id=user_id,
                        preferred_model="fallback",
                    )
                else:
                    result = await self.llm.generate(
                        prompt,
                        system=self.SYSTEM_PROMPT,
                        temperature=0.2,
                        max_tokens=1500,
                        user_id=user_id,
                    )
                model_used = result.model
                tokens_used = result.tokens_used
            except Exception as exc:
                logger.exception("LLM generation failed on attempt %d: %s", attempt, exc)
                if attempt < self._MAX_RETRIES:
                    continue
                return self._route_to_manual_review(
                    "LLM generation failed after max retries",
                    retries,
                    model_used,
                )

            # Parse JSON response
            try:
                card_data = self._parse_llm_json(result.text)
            except (json.JSONDecodeError, KeyError) as exc:
                logger.error("JSON parse failed on attempt %d: %s", attempt, exc)
                if attempt < self._MAX_RETRIES:
                    continue
                return self._route_to_manual_review(
                    f"JSON parse failure: {exc}",
                    retries,
                    model_used,
                    tokens_used,
                )

            # CITATION VERIFICATION (critical step — runs regardless of tier)
            citations_raw = card_data.get("citations", [])
            verifier = CitationVerifier(self.chunks)
            verification = await verifier.verify(citations_raw, thread_id, user_id)

            # If hallucination detected: reject -> retry
            if verification.passed:
                logger.info("Citations verified on attempt %d", attempt)
                break

            logger.warning(
                "Citation verification FAILED on attempt %d: %d failures",
                attempt,
                len(verification.failed_citations),
            )
            if attempt < self._MAX_RETRIES:
                continue  # retry with fresh generation

            # On 3 failures: route to manual review queue
            logger.error("Max retries exceeded -- routing to manual review")
            return self._route_to_manual_review(
                f"Citation verification failed {self._MAX_RETRIES}x: "
                f"{len(verification.failed_citations)} hallucinated citations",
                retries,
                model_used,
                tokens_used,
                failed_citations=verification.failed_citations,
            )

        # Should never happen, but guards against unset card_data
        if card_data is None or verification is None:
            return self._route_to_manual_review(
                "Unexpected null state after retry loop",
                retries,
            )

        # ---- 12. Compute urgency score ----
        urgency_signals = UrgencySignals(**card_data.get("urgency_signals", {}))
        urgency = self._score_urgency(card_data, urgency_signals)

        # ---- 13. Persist card to PostgreSQL ----
        card = await self._persist_card(
            user_id=user_id,
            thread_id=thread_id,
            card_data=card_data,
            chunks=chunks,
            urgency_score=urgency,
            urgency_signals=urgency_signals,
            verification=verification,
            model_used=model_used,
            tokens_used=tokens_used,
            retry_count=retries,
        )

        # ---- 14. Publish CreateCard event to NATS ----
        await self._publish_card_event(card)

        overall_latency_ms = int((time.monotonic() - overall_start) * 1000)

        logger.info(
            "generate_card complete: card=%s verified=%s retries=%d tier=%s latency=%dms",
            card.id,
            verification.passed,
            retries,
            tier,
            overall_latency_ms,
        )

        card_result = CardResult(
            card=card,
            citations_verified=verification.passed,
            retry_count=retries,
            latency_ms=overall_latency_ms,
            model_used=model_used,
            tokens_used=tokens_used,
        )

        # Store in cache (5 min TTL)
        await self._cache_card(cache_key, card_result, ttl=300)

        return card_result

    # ------------------------------------------------------------------
    # Context assembly (delegates to ContextBuilder)
    # ------------------------------------------------------------------

    async def _get_relationship_context(self, user_id: str, thread_id: str) -> str:
        return await self._context_builder.build_relationship_context(user_id, thread_id)

    async def _get_calendar_context(self, user_id: str) -> str:
        return await self._context_builder.build_calendar_context(user_id)

    # ------------------------------------------------------------------
    # Prompt rendering (tier-specific)
    # ------------------------------------------------------------------

    def _render_prompt(
        self,
        chunks: list[Chunk],
        relationship_context: str,
        calendar_context: str,
    ) -> str:
        """Render the full Jinja2 prompt template with all context."""
        return self._template.render(
            chunks=chunks,
            relationship_context=relationship_context,
            calendar_context=calendar_context,
        )

    def _render_prompt_condensed(
        self,
        chunks: list[Chunk],
        relationship_context: str,
        calendar_context: str,
    ) -> str:
        """Render the condensed prompt for fast-tier (Haiku) generation."""
        return self._condensed_template.render(
            chunks=chunks,
            relationship_context=relationship_context,
            calendar_context=calendar_context,
        )

    async def _render_prompt_hierarchical(
        self,
        user_id: str,
        thread_id: str,
        chunks: list[Chunk],
        relationship_context: str,
        calendar_context: str,
    ) -> str:
        """Render prompt using hierarchical summary + last 3 chunks only."""
        summary_text = ""
        if self._summarizer is not None:
            try:
                summary = await self._summarizer.summarize_thread(user_id, thread_id)
                if summary:
                    summary_text = summary.narrative
                    logger.debug(
                        "Hierarchical summary loaded for thread=%s (%d chars)",
                        thread_id,
                        len(summary_text),
                    )
            except Exception as exc:
                logger.warning("Hierarchical summary failed (non-blocking): %s", exc)

        if not summary_text:
            logger.warning(
                "No hierarchical summary available for thread=%s; falling back to full chunks",
                thread_id,
            )
            return self._render_prompt(chunks, relationship_context, calendar_context)

        # Use summary + last 3 chunks only
        recent_chunks = chunks[-3:] if len(chunks) >= 3 else chunks
        return self._template.render(
            chunks=recent_chunks,
            relationship_context=relationship_context,
            calendar_context=calendar_context,
            thread_summary=summary_text,
        )

    # ------------------------------------------------------------------
    # JSON parsing (with repair heuristics)
    # ------------------------------------------------------------------

    @staticmethod
    def _parse_llm_json(raw: str) -> dict[str, Any]:
        """Extract and parse JSON from LLM response text.

        Handles markdown fences and trailing commentary.
        """
        text = raw.strip()

        # Strip markdown fences if present
        if text.startswith("```"):
            lines = text.splitlines()
            # Remove opening fence
            if lines[0].startswith("```"):
                lines = lines[1:]
            # Remove closing fence
            if lines and lines[-1].startswith("```"):
                lines = lines[:-1]
            text = "\n".join(lines).strip()

        # Sometimes LLM adds commentary after JSON -- grab first {...} block
        start_idx = text.find("{")
        end_idx = text.rfind("}")
        if start_idx == -1 or end_idx == -1 or end_idx <= start_idx:
            raise json.JSONDecodeError("No JSON object found in response", text, 0)

        json_str = text[start_idx : end_idx + 1]
        return json.loads(json_str)

    # ------------------------------------------------------------------
    # Urgency scoring
    # ------------------------------------------------------------------

    @staticmethod
    def _score_urgency(
        card_data: dict[str, Any],
        signals: UrgencySignals,
    ) -> float:
        """Compute urgency score from detected signals.

        Scoring rubric (capped at 1.0):
        - deadline < 24h:     +0.4
        - deadline 24-72h:    +0.2
        - high interaction:   +0.1
        - urgent keywords:    +0.2
        """
        score = 0.0

        if signals.deadline_within_72h:
            # Determine if < 24h by inspecting raw deadlines
            deadlines = card_data.get("context", {}).get("deadlines", [])
            has_imminent = any(
                "hour" in d.lower() or "today" in d.lower() or "tomorrow" in d.lower()
                for d in deadlines
            )
            score += 0.4 if has_imminent else 0.2
        elif signals.has_deadline:
            score += 0.2

        if signals.high_interaction_volume:
            score += 0.1

        if signals.urgent_keywords:
            score += 0.2

        return min(round(score, 2), 1.0)

    # ------------------------------------------------------------------
    # Persistence
    # ------------------------------------------------------------------

    async def _persist_card(
        self,
        *,
        user_id: str,
        thread_id: str,
        card_data: dict[str, Any],
        chunks: list[Chunk],
        urgency_score: float,
        urgency_signals: UrgencySignals,
        verification: VerificationResult,
        model_used: str,
        tokens_used: int,
        retry_count: int,
    ) -> DecisionCard:
        """Persist the decision card to PostgreSQL and return the model."""
        # Build citations from verified data
        citations_raw = card_data.get("citations", [])
        chunk_lookup = {c.chunk_id: c for c in chunks}

        chunk_citations: list[ChunkCitation] = []
        for cit in citations_raw:
            cid = cit.get("chunk_id", "")
            chunk = chunk_lookup.get(UUID(cid) if isinstance(cid, str) else cid)
            if chunk is None:
                continue
            chunk_citations.append(
                ChunkCitation(
                    chunk_id=UUID(cid) if isinstance(cid, str) else cid,
                    verbatim_snippet=cit.get("verbatim", ""),
                    email_id=chunk.email_id,
                    paragraph_index=chunk.paragraph_index,
                )
            )

        # Build context sub-model
        ctx_raw = card_data.get("context", {})
        card_context = CardContext(
            history_summary=ctx_raw.get("history_summary"),
            prior_commitments=ctx_raw.get("prior_commitments", []),
            quoted_numbers=ctx_raw.get("quoted_numbers", []),
            deadlines=ctx_raw.get("deadlines", []),
            sentiment=ctx_raw.get("sentiment"),
        )

        card = DecisionCard(
            user_id=UUID(user_id),
            thread_id=UUID(thread_id),
            from_field=card_data.get("from_field", {}),
            they_want=card_data["they_want"],
            context=card_context,
            need_from_user=card_data["need_from_user"],
            chunk_citations=chunk_citations,
            citations_verified=verification.passed,
            urgency_score=urgency_score,
            urgency_signals=urgency_signals,
            model_used=model_used,
            tokens_used=tokens_used,
            retry_count=retry_count,
        )

        # Insert into PostgreSQL
        await self.postgres.execute(
            """
            INSERT INTO decision_cards (
                id, user_id, thread_id, from_field, they_want,
                context_history_summary, context_prior_commitments,
                context_quoted_numbers, context_deadlines, context_sentiment,
                need_from_user, chunk_citations, citations_verified,
                urgency_score, urgency_signals, model_used, tokens_used,
                retry_count, created_at
            ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
            ON CONFLICT (id) DO NOTHING
            """,
            str(card.id),
            str(card.user_id),
            str(card.thread_id),
            json.dumps(card.from_field),
            card.they_want,
            card.context.history_summary,
            json.dumps(card.context.prior_commitments),
            json.dumps(card.context.quoted_numbers),
            json.dumps(card.context.deadlines),
            card.context.sentiment,
            card.need_from_user,
            json.dumps([c.model_dump() for c in card.chunk_citations]),
            card.citations_verified,
            card.urgency_score,
            json.dumps(card.urgency_signals.model_dump()),
            card.model_used,
            card.tokens_used,
            card.retry_count,
            card.created_at.isoformat(),
        )

        logger.info("Persisted decision card %s to PostgreSQL", card.id)
        return card

    # ------------------------------------------------------------------
    # Event publishing
    # ------------------------------------------------------------------

    async def _publish_card_event(self, card: DecisionCard) -> None:
        """Publish a CreateCard domain event to NATS."""
        event = {
            "event_type": "CreateCard",
            "payload": {
                "card_id": str(card.id),
                "user_id": str(card.user_id),
                "thread_id": str(card.thread_id),
                "they_want": card.they_want,
                "urgency_score": card.urgency_score,
                "citations_verified": card.citations_verified,
                "model_used": card.model_used,
                "created_at": card.created_at.isoformat(),
            },
        }
        await self.nats.publish("cards.created", event)
        logger.debug("Published CreateCard event for %s", card.id)

    # ------------------------------------------------------------------
    # Manual review routing
    # ------------------------------------------------------------------

    def _route_to_manual_review(
        self,
        reason: str,
        retry_count: int,
        model_used: str = "",
        tokens_used: int = 0,
        failed_citations: list[dict[str, Any]] | None = None,
    ) -> CardResult:
        """Return a CardResult indicating manual review routing."""
        logger.error("Routed to manual review: %s", reason)
        return CardResult(
            card=None,
            citations_verified=False,
            retry_count=retry_count,
            model_used=model_used,
            tokens_used=tokens_used,
            routed_to_manual_review=True,
            routing_reason=reason,
        )
