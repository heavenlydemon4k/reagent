"""
Token / Cost Metering

Tracks per-user LLM usage in both Redis (fast counters) and PostgreSQL
(durable history).  Used by the fallback chain for budget guardrails.

Key features:
- Daily counters in Redis with automatic expiry
- Persistent INSERTs into ``token_usage`` table
- Rolling-average cost calculation for anomaly detection
- Budget-exceed flag with configurable multiplier

Example:
    meter = TokenMeter(redis_pool, db_pool)
    await meter.record_usage("user_42", "claude-3-5-sonnet", 100, 50, 0.015)
    usage = await meter.get_daily_usage("user_42")
"""

from __future__ import annotations

import json
import logging
from datetime import date, datetime, timedelta
from typing import Any, Dict, List, Optional

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Redis key helpers
# ---------------------------------------------------------------------------

_REDIS_PREFIX = "intelligence:meter"


def _daily_key(user_id: str, metric: str, day: Optional[date] = None) -> str:
    day = day or date.today()
    return f"{_REDIS_PREFIX}:daily:{user_id}:{metric}:{day.isoformat()}"


def _model_key(user_id: str, model: str, day: Optional[date] = None) -> str:
    day = day or date.today()
    return f"{_REDIS_PREFIX}:model:{user_id}:{model}:{day.isoformat()}"


# ---------------------------------------------------------------------------
# SQL statements
# ---------------------------------------------------------------------------

_INSERT_USAGE_SQL = """
INSERT INTO token_usage (
    user_id,
    model,
    tokens_input,
    tokens_output,
    cost_usd,
    latency_ms,
    created_at
) VALUES ($1, $2, $3, $4, $5, $6, NOW())
"""

_DAILY_USAGE_SQL = """
SELECT
    model,
    SUM(tokens_input) + SUM(tokens_output) AS total_tokens,
    SUM(cost_usd) AS total_cost
FROM token_usage
WHERE user_id = $1
  AND created_at >= $2
  AND created_at <  $3
GROUP BY model
"""

_AVG_COST_SQL = """
SELECT AVG(daily_cost) AS avg_cost
FROM (
    SELECT DATE(created_at) AS day, SUM(cost_usd) AS daily_cost
    FROM token_usage
    WHERE user_id = $1
      AND created_at >= NOW() - ($2 || ' days')::INTERVAL
    GROUP BY DATE(created_at)
) subq
"""

# ---------------------------------------------------------------------------
# Schema bootstrap (for tests / fresh installs)
# ---------------------------------------------------------------------------

_CREATE_TABLE_SQL = """
CREATE TABLE IF NOT EXISTS token_usage (
    id          SERIAL PRIMARY KEY,
    user_id     TEXT NOT NULL,
    model       TEXT NOT NULL,
    tokens_input    INTEGER NOT NULL DEFAULT 0,
    tokens_output   INTEGER NOT NULL DEFAULT 0,
    cost_usd        NUMERIC(12, 6) NOT NULL DEFAULT 0.0,
    latency_ms      NUMERIC(10, 2),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_token_usage_user_created
    ON token_usage (user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_token_usage_model
    ON token_usage (model);
"""

# ---------------------------------------------------------------------------
# TokenMeter
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
        """
        Args:
            redis_client: An async Redis client (``redis.asyncio.Redis``).
            db_pool: An asyncpg connection pool.
        """
        self.redis = redis_client
        self.db = db_pool

    # ------------------------------------------------------------------
    # Bootstrapping
    # ------------------------------------------------------------------

    async def init_db(self) -> None:
        """Create the ``token_usage`` table if it doesn't exist."""
        if self.db is None:
            logger.warning("TokenMeter.init_db called with no DB pool")
            return
        async with self.db.acquire() as conn:
            await conn.execute(_CREATE_TABLE_SQL)
        logger.info("token_usage table ensured")

    # ------------------------------------------------------------------
    # Recording
    # ------------------------------------------------------------------

    async def record_usage(
        self,
        user_id: str,
        model: str,
        tokens_input: int,
        tokens_output: int,
        cost: float,
        latency_ms: float = 0.0,
    ) -> None:
        """
        Record usage in Redis counters **and** PostgreSQL.

        Args:
            user_id: The user's stable identifier.
            model: Canonical model name (used for cost lookup).
            tokens_input: Input tokens consumed.
            tokens_output: Output tokens generated.
            cost: Estimated cost in USD.
            latency_ms: Wall-clock latency of the call.
        """
        total_tokens = tokens_input + tokens_output
        today = date.today()

        # ---- Redis (fast counters) ----
        if self.redis is not None:
            try:
                pipe = self.redis.pipeline()
                # Global daily counters
                pipe.incrby(_daily_key(user_id, "tokens", today), total_tokens)
                pipe.incrbyfloat(_daily_key(user_id, "cost", today), float(cost))
                pipe.incrby(_daily_key(user_id, "calls", today), 1)
                # Per-model breakdown
                pipe.incrby(_model_key(user_id, model, today), total_tokens)
                # Set 48h expiry so old counters don't bloat Redis
                for k in [
                    _daily_key(user_id, "tokens", today),
                    _daily_key(user_id, "cost", today),
                    _daily_key(user_id, "calls", today),
                    _model_key(user_id, model, today),
                ]:
                    pipe.expire(k, 172800)
                await pipe.execute()
            except Exception as exc:
                logger.warning("Redis metering failed (non-blocking): %s", exc)

        # ---- PostgreSQL (durable history) ----
        if self.db is not None:
            try:
                await self.db.execute(
                    _INSERT_USAGE_SQL,
                    user_id,
                    model,
                    tokens_input,
                    tokens_output,
                    cost,
                    latency_ms,
                )
            except Exception as exc:
                logger.warning("PostgreSQL metering failed (non-blocking): %s", exc)

    # ------------------------------------------------------------------
    # Queries
    # ------------------------------------------------------------------

    async def get_daily_usage(self, user_id: str, day: Optional[date] = None) -> Dict[str, Any]:
        """
        Return usage for a single day.

        Returns:
            ``{
                "user_id": str,
                "date": str,
                "total_tokens": int,
                "total_cost": float,
                "total_calls": int,
                "model_breakdown": {"claude-3-5-sonnet": 1200, ...},
            }``
        """
        day = day or date.today()
        total_tokens = 0
        total_cost = 0.0
        total_calls = 0
        model_breakdown: Dict[str, int] = {}

        # Prefer Redis for today's fast counters
        if self.redis is not None:
            try:
                tokens_key = _daily_key(user_id, "tokens", day)
                cost_key = _daily_key(user_id, "cost", day)
                calls_key = _daily_key(user_id, "calls", day)

                tokens_raw, cost_raw, calls_raw = await self.redis.mget(
                    tokens_key, cost_key, calls_key
                )
                total_tokens = int(tokens_raw or 0)
                total_cost = round(float(cost_raw or 0.0), 6)
                total_calls = int(calls_raw or 0)

                # Model breakdown — scan for model keys
                pattern = f"{_REDIS_PREFIX}:model:{user_id}:*:{day.isoformat()}"
                async for key in self.redis.scan_iter(match=pattern):
                    model_name = key.decode().split(":")[-2]
                    val = await self.redis.get(key)
                    if val:
                        model_breakdown[model_name] = int(val)
            except Exception as exc:
                logger.warning("Redis query failed, falling back to PG: %s", exc)

        # Fall back to PostgreSQL if Redis is unavailable or for historical days
        if self.db is not None and not total_tokens:
            try:
                day_start = datetime.combine(day, datetime.min.time())
                day_end = day_start + timedelta(days=1)
                rows = await self.db.fetch(_DAILY_USAGE_SQL, user_id, day_start, day_end)
                for row in rows:
                    model = row["model"]
                    tokens = row["total_tokens"] or 0
                    cost = row["total_cost"] or 0.0
                    total_tokens += int(tokens)
                    total_cost += float(cost)
                    model_breakdown[model] = int(tokens)
            except Exception as exc:
                logger.warning("PostgreSQL query failed: %s", exc)

        return {
            "user_id": user_id,
            "date": day.isoformat(),
            "total_tokens": total_tokens,
            "total_cost": round(total_cost, 6),
            "total_calls": total_calls,
            "model_breakdown": model_breakdown,
        }

    async def get_average_cost(self, user_id: str, days: int = 7) -> float:
        """
        Rolling average daily cost over the last *days* days.

        Args:
            user_id: The user to query.
            days: Look-back window in days (default 7).

        Returns:
            Average cost per day in USD (0.0 if no history).
        """
        if self.db is None:
            logger.debug("No DB pool — returning 0.0 for average cost")
            return 0.0

        try:
            row = await self.db.fetchrow(_AVG_COST_SQL, user_id, str(days))
            avg = row["avg_cost"] if row and row["avg_cost"] else 0.0
            return round(float(avg), 6)
        except Exception as exc:
            logger.warning("Failed to compute average cost: %s", exc)
            return 0.0

    async def is_over_budget(self, user_id: str, multiplier: float = 2.0) -> bool:
        """
        Check if today's cost exceeds *multiplier* x the 7-day rolling average.

        This is the primary cost-anomaly signal used by the fallback chain.

        Args:
            user_id: User to check.
            multiplier: Threshold multiplier (default 2.0).

        Returns:
            ``True`` if today's spend is anomalously high.
        """
        daily = await self.get_daily_usage(user_id)
        today_cost = daily.get("total_cost", 0.0)

        # Short-circuit: no spend means no exceedance
        if today_cost == 0.0:
            return False

        avg_cost = await self.get_average_cost(user_id, days=7)

        # If there's no history, use a small baseline to avoid false-positives
        if avg_cost == 0.0:
            baseline = 0.50  # 50c baseline for new users
            return today_cost > (multiplier * baseline)

        return today_cost > (multiplier * avg_cost)

    # ------------------------------------------------------------------
    # Rate-limit helpers (Redis-backed)
    # ------------------------------------------------------------------

    async def check_rate_limit(
        self,
        user_id: str,
        daily_limit: int = 1000,
    ) -> Dict[str, Any]:
        """
        Check whether the user has exceeded their daily call limit.

        Returns:
            ``{"allowed": bool, "calls_today": int, "limit": int}``
        """
        today = date.today()
        calls_key = _daily_key(user_id, "calls", today)

        allowed = True
        calls_today = 0

        if self.redis is not None:
            try:
                raw = await self.redis.get(calls_key)
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
        """Increment the daily call counter (used after a successful LLM call)."""
        today = date.today()
        calls_key = _daily_key(user_id, "calls", today)
        if self.redis is not None:
            try:
                await self.redis.incr(calls_key)
                await self.redis.expire(calls_key, 172800)
            except Exception as exc:
                logger.warning("Failed to increment rate counter: %s", exc)
