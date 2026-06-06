"""
Token and cost metering for LLM usage.

Tracks usage in Redis for real-time counters and in PostgreSQL
for durable billing records.
"""

import json
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from typing import Optional
from uuid import UUID, uuid4

from intelligence.core.config import get_settings
from intelligence.core.db import get_connection
from intelligence.core.redis_client import get_redis


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
    await redis.expire(f"{service}:usage:daily:{record.timestamp.date().isoformat()}", 60 * 60 * 24 * 30)
    await redis.expire(f"{service}:usage:user:{str(user_id)}:{record.timestamp.date().isoformat()}", 60 * 60 * 24 * 30)

    return record


async def get_daily_summary(date: Optional[str] = None) -> dict:
    """Fetch daily usage summary from Redis."""
    from datetime import date as dt_date

    target_date = date or dt_date.today().isoformat()
    redis = await get_redis()
    settings = get_settings()
    service = settings.service_name

    key = f"{service}:usage:daily:{target_date}"
    data = await redis.hgetall(key)

    if not data:
        return {
            "date": target_date,
            "tokens_input": 0,
            "tokens_output": 0,
            "cost_estimate": 0.0,
        }

    return {
        "date": target_date,
        "tokens_input": int(data.get("tokens_input", 0)),
        "tokens_output": int(data.get("tokens_output", 0)),
        "cost_estimate": float(data.get("cost_estimate", 0)),
    }
