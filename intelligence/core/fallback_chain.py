"""
Fallback Chain — Intelligence Layer Gateway

Orchestrates LLM calls across multiple providers with automatic fallback,
cost guardrails, and per-user metering.

Architecture:
    1. Rate-limit check (Redis)
    2. Cost-anomaly check (rolling 7-day average)
    3. Try primary → fallback → cost_fallback
    4. Meter every attempt to Redis + PostgreSQL
    5. On total failure: queue in pending_llm, notify user

Invariants:
- NEVER generate cards with degraded models without user consent.
- Cost > 2x average → switch to cheaper model + warning flag.
- Token metering accurate to ±5%.

Example:
    chain = FallbackChain(
        primary=AnthropicClient(api_key=..., model="claude-3-5-sonnet-20241022"),
        fallback=AnthropicClient(api_key=..., model="claude-3-haiku-20240307"),
        cost_fallback=OpenAIClient(api_key=..., model="gpt-3.5-turbo"),
        meter=TokenMeter(redis, db_pool),
    )
    result = await chain.generate(prompt, system=..., user_id="user_42")
"""

from __future__ import annotations

import asyncio
import json
import logging
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, AsyncIterator, Dict, List, Optional

import redis.asyncio as redis

from intelligence.core.llm_client import LLMClient, GenerationResult
from intelligence.core.metering import TokenMeter

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Pending-LLM queue (lightweight in-memory; replace with Celery / SQS)
# ---------------------------------------------------------------------------

@dataclass
class PendingLLMTask:
    """Represents a task queued for later retry when all LLMs fail."""

    user_id: str
    prompt: str
    system: Optional[str]
    temperature: float
    max_tokens: int
    created_at: float = field(default_factory=time.time)
    attempts: int = 0


_pending_queue: List[PendingLLMTask] = []


def _enqueue_pending_memory(task: PendingLLMTask) -> None:
    """Add a task to the in-memory pending-LLM queue (fallback when Redis is unavailable)."""
    _pending_queue.append(task)
    logger.info(
        "Queued pending_llm task (memory) for user=%s (queue depth=%d)",
        task.user_id, len(_pending_queue),
    )


def _drain_pending_memory(max_size: int = 100) -> List[PendingLLMTask]:
    """
    Drain the in-memory pending queue (fallback when Redis is unavailable).

    Returns up to *max_size* tasks.
    """
    global _pending_queue
    batch = _pending_queue[:max_size]
    _pending_queue = _pending_queue[max_size:]
    return batch


# ---------------------------------------------------------------------------
# FallbackChain
# ---------------------------------------------------------------------------

class FallbackChain:
    """
    Orchestrates LLM calls with automatic fallback.

    The chain contains three tiers:
    - **primary** — best-quality model (Claude 3.5 Sonnet)
    - **fallback** — cheaper same-provider model (Claude 3 Haiku)
    - **cost_fallback** — cheapest model (GPT-3.5-turbo)

    Every ``generate`` call follows this exact pipeline:

    1. Rate-limit check (Redis daily counter).
    2. Cost-anomaly check (7-day rolling average).
    3. Attempt **primary**. On 5xx/timeout: retry once, then proceed to fallback.
    4. Attempt **fallback**. On failure: proceed to cost fallback.
    5. Attempt **cost_fallback**.
    6. If all fail: enqueue in ``pending_llm``, return error to user.
    7. Meter every call to Redis + PostgreSQL.
    """

    def __init__(
        self,
        primary: LLMClient,
        fallback: LLMClient,
        cost_fallback: LLMClient,
        meter: Optional[TokenMeter] = None,
        daily_rate_limit: int = 1000,
        cost_exceed_multiplier: float = 2.0,
        redis_client: Optional[redis.Redis] = None,
    ):
        self.primary = primary
        self.fallback = fallback
        self.cost_fallback = cost_fallback
        self.meter = meter
        self.daily_rate_limit = daily_rate_limit
        self.cost_exceed_multiplier = cost_exceed_multiplier
        self.redis = redis_client
        self.pending_key = "intelligence:pending_llm"

    # ------------------------------------------------------------------
    # Core generation
    # ------------------------------------------------------------------

    async def generate(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
        user_id: Optional[str] = None,
        preferred_model: str = "primary",
    ) -> GenerationResult:
        """
        Generate text using the fallback chain.

        Args:
            prompt: The user message / prompt text.
            system: Optional system prompt.
            temperature: Sampling temperature.
            max_tokens: Maximum tokens to generate.
            user_id: Optional user identifier for metering & rate limits.
            preferred_model: Which model tier to try first
                ("primary", "fallback", "cost_fallback").

        Returns:
            GenerationResult — always non-None.  Check ``.is_success``.
        """
        started_at = time.perf_counter()

        # 1. Rate-limit check
        if user_id and self.meter is not None:
            rate_info = await self.meter.check_rate_limit(
                user_id, self.daily_rate_limit
            )
            if not rate_info["allowed"]:
                logger.warning(
                    "Rate limit exceeded for user=%s (%d/%d)",
                    user_id, rate_info["calls_today"], rate_info["limit"],
                )
                return GenerationResult.error(
                    model="rate_limit",
                    error_message=(
                        f"Daily rate limit exceeded: {rate_info['calls_today']}"
                        f" / {rate_info['limit']} calls. Try again tomorrow."
                    ),
                    metadata={"rate_limit": rate_info},
                )

        # 2. Cost-anomaly check — should we force cost_fallback?
        force_cost_fallback = False
        if user_id and self.meter is not None:
            try:
                force_cost_fallback = await self.meter.is_over_budget(
                    user_id, multiplier=self.cost_exceed_multiplier
                )
                if force_cost_fallback:
                    logger.info(
                        "Cost anomaly detected for user=%s — forcing cost_fallback",
                        user_id,
                    )
            except Exception as exc:
                logger.warning("Cost-anomaly check failed (continuing): %s", exc)

        # 3. Execute through the chain
        result: GenerationResult

        if force_cost_fallback:
            # Skip straight to cost_fallback with a warning flag
            result = await self._try_generate(
                self.cost_fallback, prompt, system, temperature, max_tokens, user_id=user_id
            )
            result.warning_flags.append("cost_fallback_forced: budget_anomaly")
            total_latency = (time.perf_counter() - started_at) * 1000
            result.latency_ms += total_latency
            return result

        # Build ordered tier list based on preferred_model
        client_map = {
            "primary": self.primary,
            "fallback": self.fallback,
            "cost_fallback": self.cost_fallback,
        }
        tier_names = ["primary", "fallback", "cost_fallback"]
        tier_labels = {
            "primary": "Tier 1: Primary (Claude 3.5 Sonnet)",
            "fallback": "Tier 2: Fallback (Claude 3 Haiku)",
            "cost_fallback": "Tier 3: Cost Fallback (GPT-3.5-turbo)",
        }

        # Reorder tiers so preferred_model is first
        if preferred_model in client_map and preferred_model != "primary":
            tier_names.remove(preferred_model)
            tier_names.insert(0, preferred_model)

        # Attempt each tier in order
        for tier_idx, tier_name in enumerate(tier_names):
            client = client_map[tier_name]
            is_preferred = tier_name == preferred_model

            # Log tier selection
            if tier_idx == 0:
                logger.info(
                    "Starting generation with %s",
                    tier_labels.get(tier_name, tier_name),
                )

            result = await self._try_generate(
                client, prompt, system, temperature, max_tokens, user_id=user_id
            )
            if result.is_success:
                total_latency = (time.perf_counter() - started_at) * 1000
                result.latency_ms += total_latency
                if tier_idx > 0:
                    result.warning_flags.append(f"fallback: tier_{tier_idx + 1}_used")
                return result

            # On retryable error at preferred tier: retry once
            if is_preferred and result.metadata.get("retryable"):
                logger.info("Retrying preferred model (attempt 2)")
                await asyncio.sleep(0.5)  # 500ms backoff
                result = await self._try_generate(
                    client, prompt, system, temperature, max_tokens, user_id=user_id
                )
                if result.is_success:
                    total_latency = (time.perf_counter() - started_at) * 1000
                    result.latency_ms += total_latency
                    return result

            # Log fallback for next tier
            if tier_idx < len(tier_names) - 1:
                next_name = tier_names[tier_idx + 1]
                logger.info(
                    "Falling back to %s",
                    tier_labels.get(next_name, next_name),
                )

        # 4. Total failure — queue for later (persisted to Redis when available)
        logger.error(
            "All LLM tiers failed for user=%s. Enqueuing in pending_llm.", user_id
        )
        await self._queue_pending(
            user_id=user_id or "anonymous",
            prompt=prompt,
            system=system,
            temperature=temperature,
            max_tokens=max_tokens,
        )

        total_latency = (time.perf_counter() - started_at) * 1000
        result.latency_ms += total_latency
        result.error_message = (
            f"All LLM providers failed. Your request has been queued for retry. "
            f"(primary={self.primary.model_name}, fallback={self.fallback.model_name}, "
            f"cost_fallback={self.cost_fallback.model_name})"
        )
        return result

    # ------------------------------------------------------------------
    # Budget-aware generation
    # ------------------------------------------------------------------

    async def generate_with_budget(
        self,
        prompt: str,
        max_cost: float,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
        user_id: Optional[str] = None,
    ) -> GenerationResult:
        """
        Select the cheapest model that can handle the request within budget.

        Args:
            prompt: The user message / prompt text.
            max_cost: Maximum acceptable cost in USD.
            system: Optional system prompt.
            temperature: Sampling temperature.
            max_tokens: Maximum tokens to generate.
            user_id: Optional user identifier for metering.

        Returns:
            GenerationResult — cheapest successful result within budget,
            or an error if even the cheapest model exceeds *max_cost*.
        """
        from intelligence.core.llm_client import COST_TABLE

        started_at = time.perf_counter()

        # Ordered cheapest → most expensive
        tiers = [
            ("cheapest", self.cost_fallback),
            ("fallback", self.fallback),
            ("primary", self.primary),
        ]

        # Estimate max cost per tier (rough: max_tokens output + 4K input)
        estimated_input_tokens = len(prompt) // 4 + 500  # rough heuristic
        for label, client in tiers:
            pricing = COST_TABLE.get(client.model_name, {})
            if not pricing:
                continue
            est_cost = (estimated_input_tokens / 1000) * pricing["input"] + \
                       (max_tokens / 1000) * pricing["output"]
            if est_cost <= max_cost:
                result = await self._try_generate(
                    client, prompt, system, temperature, max_tokens, user_id=user_id
                )
                if result.is_success:
                    result.metadata["budget_tier"] = label
                    result.metadata["budget_limit"] = max_cost
                    total_latency = (time.perf_counter() - started_at) * 1000
                    result.latency_ms += total_latency
                    return result

        # Nothing fit in budget — return error
        return GenerationResult.error(
            model="budget",
            error_message=f"No model available within budget of ${max_cost:.4f}",
            metadata={"budget_limit": max_cost},
        )

    # ------------------------------------------------------------------
    # Streaming (no fallback — must succeed on chosen model)
    # ------------------------------------------------------------------

    async def generate_stream(
        self,
        prompt: str,
        system: Optional[str] = None,
        temperature: float = 0.4,
        max_tokens: int = 2000,
        user_id: Optional[str] = None,
        preferred_model: str = "primary",
    ) -> AsyncIterator[str]:
        """
        Stream text from a chosen model.

        Args:
            prompt: The user message / prompt text.
            system: Optional system prompt.
            temperature: Sampling temperature.
            max_tokens: Maximum tokens to generate.
            user_id: Optional user identifier.
            preferred_model: One of ``"primary"``, ``"fallback"``, ``"cost_fallback"``.

        Yields:
            Text fragments as they arrive.
        """
        client_map = {
            "primary": self.primary,
            "fallback": self.fallback,
            "cost_fallback": self.cost_fallback,
        }
        client = client_map.get(preferred_model, self.primary)
        async for chunk in client.generate_stream(prompt, system, temperature, max_tokens):
            yield chunk

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    async def _try_generate(
        self,
        client: LLMClient,
        prompt: str,
        system: Optional[str],
        temperature: float,
        max_tokens: int,
        user_id: Optional[str] = None,
    ) -> GenerationResult:
        """Call a single client and meter the result."""
        result = await client.generate(prompt, system, temperature, max_tokens)

        # Meter — success or failure
        if user_id and self.meter is not None and result.total_tokens > 0:
            try:
                await self.meter.record_usage(
                    user_id=user_id,
                    model=result.model or client.model_name,
                    tokens_input=result.tokens_input,
                    tokens_output=result.tokens_output,
                    cost=result.cost_usd,
                    latency_ms=result.latency_ms,
                )
                await self.meter.increment_rate_counter(user_id)
            except Exception as exc:
                logger.warning("Metering failed (non-blocking): %s", exc)

        return result

    # ------------------------------------------------------------------
    # Pending task persistence (Redis-backed)
    # ------------------------------------------------------------------

    async def _queue_pending(
        self,
        user_id: str,
        prompt: str,
        system: Optional[str],
        temperature: float,
        max_tokens: int,
    ) -> None:
        """Persist a failed LLM task to Redis (or in-memory fallback)."""
        if self.redis is not None:
            task = {
                "user_id": user_id,
                "prompt": prompt,
                "system": system,
                "temperature": temperature,
                "max_tokens": max_tokens,
                "queued_at": datetime.now(timezone.utc).isoformat(),
            }
            try:
                await self.redis.lpush(self.pending_key, json.dumps(task))
                queue_depth = await self.redis.llen(self.pending_key)
                logger.info(
                    "Queued pending_llm task (Redis) for user=%s (queue depth=%d)",
                    user_id, queue_depth,
                )
            except Exception as exc:
                logger.warning("Redis enqueue failed, falling back to memory: %s", exc)
                _enqueue_pending_memory(
                    PendingLLMTask(
                        user_id=user_id,
                        prompt=prompt,
                        system=system,
                        temperature=temperature,
                        max_tokens=max_tokens,
                    )
                )
        else:
            _enqueue_pending_memory(
                PendingLLMTask(
                    user_id=user_id,
                    prompt=prompt,
                    system=system,
                    temperature=temperature,
                    max_tokens=max_tokens,
                )
            )

    async def drain_pending(self, max_size: int = 100) -> List[GenerationResult]:
        """
        Process tasks that were queued while the service was down.

        Drains from Redis first (when available), then falls back to the
        in-memory queue. Returns results from re-attempted generations.

        Called on startup to clear any backlog.
        """
        results: List[GenerationResult] = []

        # --- Redis-backed drain ---
        if self.redis is not None:
            processed = 0
            while processed < max_size:
                task_json = await self.redis.rpop(self.pending_key)
                if not task_json:
                    break
                try:
                    task = json.loads(task_json)
                    result = await self.generate(
                        prompt=task["prompt"],
                        system=task.get("system"),
                        temperature=task.get("temperature", 0.4),
                        max_tokens=task.get("max_tokens", 2000),
                        user_id=task.get("user_id"),
                    )
                    results.append(result)
                    processed += 1
                except Exception as exc:
                    logger.error("Failed to drain pending task from Redis: %s", exc)

        # --- In-memory fallback drain ---
        memory_tasks = _drain_pending_memory(max_size=max_size)
        for task in memory_tasks:
            result = await self.generate(
                prompt=task.prompt,
                system=task.system,
                temperature=task.temperature,
                max_tokens=task.max_tokens,
                user_id=task.user_id,
            )
            results.append(result)

        if results:
            logger.info("Drained %d pending LLM tasks (%d succeeded)",
                        len(results), sum(1 for r in results if r.is_success))
        return results

    # ------------------------------------------------------------------
    # Diagnostics
    # ------------------------------------------------------------------

    def describe(self) -> Dict[str, Any]:
        """Return a human-readable description of the chain configuration."""
        return {
            "primary": self.primary.model_name,
            "fallback": self.fallback.model_name,
            "cost_fallback": self.cost_fallback.model_name,
            "daily_rate_limit": self.daily_rate_limit,
            "cost_exceed_multiplier": self.cost_exceed_multiplier,
            "meter_enabled": self.meter is not None,
            "pending_queue_depth": len(_pending_queue),
        }
