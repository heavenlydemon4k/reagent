"""
SSE Streaming Router — Real-time card generation progress.

Provides Server-Sent Events endpoints that stream pipeline stage progress
to clients, enabling live UI updates during card and draft generation.

Events:
    data: {"stage": "fetching_chunks", "progress": 10}
    data: {"stage": "building_context", "progress": 30}
    data: {"stage": "checking_cache", "progress": 40}
    data: {"stage": "generating", "progress": 50, "tier": "fast"}
    data: {"stage": "verifying", "progress": 80}
    data: {"stage": "persisting", "progress": 90}
    data: {"stage": "complete", "progress": 100, "card": {...}}
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
from typing import AsyncGenerator, Optional

from fastapi import APIRouter, Depends, Header, HTTPException, status
from fastapi.responses import StreamingResponse

from intelligence.app.compression.hierarchical import HierarchicalSummarizer
from intelligence.app.compression.models import CardResult
from intelligence.app.compression.service import CompressionService

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/cards", tags=["streaming"])

# ---------------------------------------------------------------------------
# Dependency
# ---------------------------------------------------------------------------

_compression_service: Optional[CompressionService] = None


def configure_compression_service(service: CompressionService) -> None:
    """Inject the compression service instance (called during app startup)."""
    global _compression_service
    _compression_service = service


async def get_compression_service() -> CompressionService:
    """FastAPI dependency: return the compression service."""
    if _compression_service is None:
        raise HTTPException(
            status_code=status.HTTP_503_SERVICE_UNAVAILABLE,
            detail="Compression service not initialized",
        )
    return _compression_service


# ---------------------------------------------------------------------------
# SSE helpers
# ---------------------------------------------------------------------------


def _sse_event(data: dict) -> str:
    """Serialize a dict as an SSE event line."""
    return f"data: {json.dumps(data)}\n\n"


# ---------------------------------------------------------------------------
# Streaming endpoints
# ---------------------------------------------------------------------------


@router.get("/{thread_id}/stream")
async def stream_card_generation(
    thread_id: str,
    user_id: str = Header(..., alias="X-User-ID"),
    compression_service: CompressionService = Depends(get_compression_service),
) -> StreamingResponse:
    """Stream card generation progress as Server-Sent Events.

    Yields JSON events at each pipeline stage:
        - fetching_chunks (10%)
        - building_context (30%)
        - checking_cache  (40%)
        - generating      (50-70%, includes tier info)
        - verifying       (80%)
        - persisting      (90%)
        - complete        (100%, includes card data)

    Args:
        thread_id: Email thread UUID.
        user_id: Authenticated user's UUID (from X-User-ID header).

    Returns:
        StreamingResponse with text/event-stream media type.
    """
    logger.info("SSE stream started for thread=%s user=%s", thread_id, user_id)

    async def event_generator() -> AsyncGenerator[str, None]:
        pipeline_start = time.monotonic()

        try:
            # ---- Stage 1: Fetching chunks ----
            yield _sse_event({"stage": "fetching_chunks", "progress": 10})
            chunks = await compression_service.chunks.get_chunks_by_thread(
                thread_id, user_id
            )

            if not chunks:
                yield _sse_event({
                    "stage": "error",
                    "progress": 0,
                    "error": "No chunks found for thread",
                })
                return

            # ---- Stage 2: Building context ----
            yield _sse_event({"stage": "building_context", "progress": 30})

            # Fetch context in parallel
            rel_task = compression_service._get_relationship_context(user_id, thread_id)
            cal_task = compression_service._get_calendar_context(user_id)
            rel_ctx, cal_ctx = await asyncio.gather(rel_task, cal_task)

            # ---- Stage 3: Checking cache ----
            yield _sse_event({"stage": "checking_cache", "progress": 40})
            cache_key = f"card:{thread_id}:v{compression_service._chunk_hash(chunks)}"
            cached = await compression_service._get_cached_card(cache_key)

            if cached:
                yield _sse_event({
                    "stage": "complete",
                    "progress": 100,
                    "card": cached.model_dump(mode="json") if hasattr(cached, "model_dump") else cached.dict(),
                    "cache_hit": True,
                    "latency_ms": int((time.monotonic() - pipeline_start) * 1000),
                })
                logger.info("SSE stream: cache hit for thread=%s", thread_id)
                return

            # ---- Stage 4: Select tier ----
            tier = compression_service._select_generation_tier(thread_id, chunks)

            # ---- Stage 5: Generating ----
            yield _sse_event({
                "stage": "generating",
                "progress": 50,
                "tier": tier,
                "chunk_count": len(chunks),
            })

            # Build tier-specific prompt
            if tier == "fast":
                prompt = compression_service._render_prompt_condensed(
                    chunks, rel_ctx, cal_ctx
                )
            elif tier == "hierarchical":
                prompt = await compression_service._render_prompt_hierarchical(
                    user_id, thread_id, chunks, rel_ctx, cal_ctx
                )
            else:
                prompt = compression_service._render_prompt(chunks, rel_ctx, cal_ctx)

            # Generate via LLM
            try:
                if tier == "fast":
                    result = await compression_service.llm.generate(
                        prompt,
                        system=compression_service.SYSTEM_PROMPT_CONDENSED,
                        temperature=0.2,
                        max_tokens=1200,
                        user_id=user_id,
                        preferred_model="fallback",
                    )
                else:
                    result = await compression_service.llm.generate(
                        prompt,
                        system=compression_service.SYSTEM_PROMPT,
                        temperature=0.2,
                        max_tokens=1500,
                        user_id=user_id,
                    )
            except Exception as exc:
                logger.exception("LLM generation failed in SSE stream: %s", exc)
                yield _sse_event({
                    "stage": "error",
                    "progress": 50,
                    "error": f"LLM generation failed: {exc}",
                })
                return

            # ---- Stage 6: Parsing ----
            yield _sse_event({"stage": "parsing", "progress": 60})
            try:
                card_data = compression_service._parse_llm_json(result.text)
            except (json.JSONDecodeError, KeyError) as exc:
                logger.error("JSON parse failed in SSE stream: %s", exc)
                yield _sse_event({
                    "stage": "error",
                    "progress": 60,
                    "error": f"JSON parse failure: {exc}",
                })
                return

            # ---- Stage 7: Verifying ----
            yield _sse_event({"stage": "verifying", "progress": 80})
            from intelligence.app.compression.verifier import CitationVerifier

            citations_raw = card_data.get("citations", [])
            verifier = CitationVerifier(compression_service.chunks)
            verification = await verifier.verify(citations_raw, thread_id, user_id)

            if not verification.passed:
                yield _sse_event({
                    "stage": "error",
                    "progress": 80,
                    "error": "Citation verification failed",
                    "failed_citations": len(verification.failed_citations),
                })
                return

            # ---- Stage 8: Persisting ----
            yield _sse_event({"stage": "persisting", "progress": 90})

            urgency_signals = compression_service._score_urgency(
                card_data, card_data.get("urgency_signals", {})
            )
            urgency = compression_service._score_urgency(card_data, urgency_signals)

            card = await compression_service._persist_card(
                user_id=user_id,
                thread_id=thread_id,
                card_data=card_data,
                chunks=chunks,
                urgency_score=urgency,
                urgency_signals=urgency_signals,
                verification=verification,
                model_used=result.model,
                tokens_used=result.tokens_used,
                retry_count=0,
            )

            await compression_service._publish_card_event(card)

            overall_latency_ms = int((time.monotonic() - pipeline_start) * 1000)

            card_result = CardResult(
                card=card,
                citations_verified=verification.passed,
                retry_count=0,
                latency_ms=overall_latency_ms,
                model_used=result.model,
                tokens_used=result.tokens_used,
            )

            # Cache the result
            await compression_service._cache_card(cache_key, card_result, ttl=300)

            # ---- Stage 9: Complete ----
            yield _sse_event({
                "stage": "complete",
                "progress": 100,
                "card": card_result.model_dump(mode="json") if hasattr(card_result, "model_dump") else card_result.dict(),
                "tier": tier,
                "cache_hit": False,
                "latency_ms": overall_latency_ms,
            })
            logger.info(
                "SSE stream complete for thread=%s tier=%s latency=%dms",
                thread_id, tier, overall_latency_ms,
            )

        except Exception as exc:
            logger.exception("SSE stream error for thread=%s: %s", thread_id, exc)
            yield _sse_event({
                "stage": "error",
                "progress": 0,
                "error": str(exc),
            })

    return StreamingResponse(
        event_generator(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
        },
    )
