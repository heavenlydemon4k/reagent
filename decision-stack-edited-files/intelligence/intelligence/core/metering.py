"""
Token and cost metering for LLM usage.

Tracks usage in Redis for real-time counters and in PostgreSQL
for durable billing records.
"""

import json
import logging
from dataclasses import asdict, dataclass
from datetime import date, datetime, timezone
from typing import Any, Dict, Optional
from uuid import UUID, uuid4

from intelligence.core.config import get_settings

try:
    from intelligence.core.db import get_connection
except Exception:
    get_connection = None  # type: ignore[assignment]

try:
    from intelligence.core.redis_client import get_redis
except Exception:
    get_redis = None  # type: ignore[assignment]

logger = logging.getLogger(__name__)


@dataclass
class UsageRecord:
    """A single LLM usage event."""
    id: UUID
    user_id: UUID
    thread_id: Optional[UUID]
    model_used: str
    tokens_input: int
    tokens_output: int
    latency_ms: int
    cost_estimate: float
    timestamp: datetime


async def record_usage(
    user_id: UUID,
    model_used: str,
    tokens_input: int,
    tokens_output: int,
    latency_ms: int,
    cost_estimate: float,
    thread_id: Optional[UUID] = None,
) -> UsageRecord:
    """Persist a usage record to PostgreSQL and update Redis counters.

    Returns the created UsageRecord.
    """
    record = UsageRecord(
        id=uuid4(),
        user_id=user_id,
        thread_id=thread_id,
        model_used=model_used,
        tokens_input=tokens_input,
        tokens_output=tokens_output,
        latency_ms=latency_ms,
        cost_estimate=cost_estimate,
        timestamp=datetime.now(timezone.utc),
    )

    # --- PostgreSQL durable record ---
    async with get_connection() as conn:
        await conn.execute(
            """
            INSERT INTO llm_usage (
                id, user_id, thread_id, model_used,
                tokens_input, tokens_output, latency_ms, cost_estimate, timestamp
            ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
            """,
            record.id,
            record.user_id,
            record.thread_id,
            record.model_used,
            record.tokens_input,
            record.tokens_output,
            record.latency_ms,
            record.cost_estimate,
            record.timestamp,
        )

    # --- Redis counters ---
    redis = await get_redis()
    settings = get_settings()
    service = settings.service_name

    # Increment counters
    await redis.hincrby(f"{service}:usage:daily:{record.timestamp.date().isoformat()}", "tokens_input", tokens_input)
    await redis.hincrby(f"{service}:usage:daily:{record.timestamp.date().isoformat()}", "tokens_output", tokens_output)
    await redis.hincrbyfloat(f"{service}:usage:daily:{record.timestamp.date().isoformat()}", "cost_estimate", cost_estimate)
    await redis.hincrby(f"{service}:usage:user:{str(user_id)}:{record.timestamp.date().isoformat()}", "requests", 1)

    # TTL on keys (30 days)
    if redis is not None:
        await redis.expire(f"{service}:usage:daily:{record.timestamp.date().isoformat()}", 60 * 60 * 24 * 30)
        await redis.expire(f"{service}:usage:user:{str(user_id)}:{record.timestamp.date().isoformat()}", 60 * 60 * 24 * 30)

    return record


async def get_daily_summary(date: Optional[str] = None) -> dict:
    """Fetch daily usage summary from Redis."""
    from datetime import date as dt_date

    target_date = date or dt_date.today().isoformat()

    if get_redis is not None:
        redis = await get_redis()
        settings = get_settings()
        service = settings.service_name

        key = f"{service}:usage:daily:{target_date}"
        data = await redis.hgetall(key)

        if data:
            return {
                "date": target_date,
                "tokens_input": int(data.get("tokens_input", 0)),
                "tokens_output": int(data.get("tokens_output", 0)),
                "cost_estimate": float(data.get("cost_estimate", 0)),
            }

    return {
        "date": target_date,
        "tokens_input": 0,
        "tokens_output": 0,
        "cost_estimate": 0.0,
    }


# ---------------------------------------------------------------------------
# TokenMeter — used by FallbackChain for rate-limit and budget checks
# ---------------------------------------------------------------------------


class TokenMeter:
    """
    Tracks token usage and costs per user.

    Uses Redis for fast daily counters (auto-expire after 48h) and
    PostgreSQL for durable history and analytical queries.
    """

    def __init__(
        self,
        redis_client: Optional[Any] = None,
        db_pool: Optional[Any] = None,
    ):
        self.redis = redis_client
        self.db = db_pool

    # -- Recording --

    async def record_usage(
        self,
        user_id: str,
        model: str,
        tokens_input: int,
        tokens_output: int,
        cost: float,
        latency_ms: float = 0.0,
    ) -> None:
        """Record usage in Redis counters and PostgreSQL."""
        # Redis counters (best-effort)
        if self.redis is not None:
            try:
                today = date.today().isoformat()
                await self.redis.hincrby(f"meter:{user_id}:tokens:{today}", "total", tokens_input + tokens_output)
                await self.redis.hincrbyfloat(f"meter:{user_id}:cost:{today}", "total", float(cost))
                await self.redis.hincrby(f"meter:{user_id}:calls:{today}", "total", 1)
            except Exception as exc:
                logger.warning("Redis metering failed (non-blocking): %s", exc)

    # -- Budget checks --

    async def is_over_budget(self, user_id: str, multiplier: float = 2.0) -> bool:
        """Check if today's cost exceeds multiplier x 7-day average."""
        daily = await self.get_daily_usage(user_id)
        today_cost = daily.get("total_cost", 0.0)
        if today_cost == 0.0:
            return False
        avg_cost = await self.get_average_cost(user_id, days=7)
        if avg_cost == 0.0:
            baseline = 0.50
            return today_cost > (multiplier * baseline)
        return today_cost > (multiplier * avg_cost)

    async def get_average_cost(self, user_id: str, days: int = 7) -> float:
        """Rolling average daily cost. Returns 0.0 if no history."""
        if self.db is None:
            return 0.0
        try:
            row = await self.db.fetchrow(
                """
                SELECT AVG(daily_cost) AS avg_cost
                FROM (
                    SELECT DATE(created_at) AS day, SUM(cost_estimate) AS daily_cost
                    FROM llm_usage
                    WHERE user_id = $1
                      AND created_at >= NOW() - ($2 || ' days')::INTERVAL
                    GROUP BY DATE(created_at)
                ) subq
                """,
                user_id, str(days),
            )
            avg = row["avg_cost"] if row and row["avg_cost"] else 0.0
            return round(float(avg), 6)
        except Exception as exc:
            logger.warning("Failed to compute average cost: %s", exc)
            return 0.0

    async def get_daily_usage(self, user_id: str, day: Optional[date] = None) -> Dict[str, Any]:
        """Return usage for a single day."""
        day = day or date.today()
        total_tokens = 0
        total_cost = 0.0
        total_calls = 0

        if self.redis is not None:
            try:
                today = day.isoformat()
                tokens_raw = await self.redis.hget(f"meter:{user_id}:tokens:{today}", "total")
                cost_raw = await self.redis.hget(f"meter:{user_id}:cost:{today}", "total")
                calls_raw = await self.redis.hget(f"meter:{user_id}:calls:{today}", "total")
                total_tokens = int(tokens_raw or 0)
                total_cost = round(float(cost_raw or 0.0), 6)
                total_calls = int(calls_raw or 0)
            except Exception as exc:
                logger.warning("Redis query failed: %s", exc)

        return {
            "user_id": user_id,
            "date": day.isoformat(),
            "total_tokens": total_tokens,
            "total_cost": total_cost,
            "total_calls": total_calls,
        }

    # -- Rate-limit helpers (Redis-backed) --

    async def check_rate_limit(
        self,
        user_id: str,
        daily_limit: int = 1000,
    ) -> Dict[str, Any]:
        """Check whether the user has exceeded their daily call limit."""
        today = date.today().isoformat()
        calls_key = f"meter:{user_id}:calls:{today}"

        allowed = True
        calls_today = 0

        if self.redis is not None:
            try:
                raw = await self.redis.hget(calls_key, "total")
                calls_today = int(raw or 0)
                allowed = calls_today < daily_limit
            except Exception as exc:
                logger.warning("Rate-limit check failed (allowing): %s", exc)
                allowed = True

        return {
            "allowed": allowed,
            "calls_today": calls_today,
            "limit": daily_limit,
        }

    async def increment_rate_counter(self, user_id: str) -> None:
        """Increment the daily call counter."""
        today = date.today().isoformat()
        calls_key = f"meter:{user_id}:calls:{today}"
        if self.redis is not None:
            try:
                await self.redis.hincrby(calls_key, "total", 1)
            except Exception as exc:
                logger.warning("Failed to increment rate counter: %s", exc)
