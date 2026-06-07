"""
Tests for the metering module (metering.py).

Covers TokenMeter with mocked Redis and PostgreSQL backends.
"""

import asyncio
from datetime import date
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from intelligence.core.metering import TokenMeter


@pytest.fixture
def mock_redis():
    """Return a mock Redis client with async methods."""
    redis = MagicMock()
    redis.pipeline = MagicMock(return_value=redis)
    redis.incrby = AsyncMock(return_value=redis)
    redis.incrbyfloat = AsyncMock(return_value=redis)
    redis.incr = AsyncMock(return_value=redis)
    redis.expire = AsyncMock(return_value=redis)
    redis.execute = AsyncMock(return_value=[])
    redis.mget = AsyncMock(return_value=["100", "1.23", "5"])
    redis.get = AsyncMock(return_value="42")
    redis.scan_iter = AsyncMock(return_value=[])
    return redis


@pytest.fixture
def mock_db():
    """Return a mock asyncpg pool."""
    pool = MagicMock()
    pool.acquire = MagicMock()
    return pool


@pytest.fixture
def meter(mock_redis, mock_db):
    return TokenMeter(redis_client=mock_redis, db_pool=mock_db)


class TestRecordUsage:
    @pytest.mark.asyncio
    async def test_redis_counters(self, meter, mock_redis):
        await meter.record_usage(
            user_id="user_42",
            model="claude-3-5-sonnet-20241022",
            tokens_input=100,
            tokens_output=50,
            cost=0.015,
            latency_ms=200.0,
        )
        mock_redis.pipeline.assert_called_once()
        mock_redis.incrby.assert_called()
        mock_redis.incrbyfloat.assert_called()
        mock_redis.expire.assert_called()

    @pytest.mark.asyncio
    async def test_no_redis(self, mock_db):
        meter = TokenMeter(redis_client=None, db_pool=mock_db)
        mock_db.execute = AsyncMock()
        await meter.record_usage(
            user_id="user_42",
            model="claude-3-5-sonnet-20241022",
            tokens_input=100,
            tokens_output=50,
            cost=0.015,
        )
        # Should not raise — silently skip Redis


class TestGetDailyUsage:
    @pytest.mark.asyncio
    async def test_redis_path(self, meter, mock_redis):
        mock_redis.mget = AsyncMock(return_value=["1000", "5.50", "10"])
        result = await meter.get_daily_usage("user_42")
        assert result["user_id"] == "user_42"
        assert result["total_tokens"] == 1000
        assert result["total_cost"] == 5.50
        assert result["total_calls"] == 10

    @pytest.mark.asyncio
    async def test_no_backends(self):
        meter = TokenMeter(redis_client=None, db_pool=None)
        result = await meter.get_daily_usage("user_42")
        assert result["total_tokens"] == 0
        assert result["total_cost"] == 0.0


class TestIsOverBudget:
    @pytest.mark.asyncio
    async def test_no_spend(self, meter, mock_redis):
        mock_redis.mget = AsyncMock(return_value=["0", "0.0", "0"])
        assert await meter.is_over_budget("user_42") is False

    @pytest.mark.asyncio
    async def test_high_spend_no_history(self, meter, mock_redis):
        """With no history, use a small baseline to avoid false positives."""
        mock_redis.mget = AsyncMock(return_value=["1000", "5.00", "10"])
        assert await meter.is_over_budget("user_42") is True  # $5 > 2 * $0.50 baseline


class TestRateLimit:
    @pytest.mark.asyncio
    async def test_allowed(self, meter, mock_redis):
        mock_redis.get = AsyncMock(return_value="10")
        result = await meter.check_rate_limit("user_42", daily_limit=1000)
        assert result["allowed"] is True
        assert result["calls_today"] == 10

    @pytest.mark.asyncio
    async def test_denied(self, meter, mock_redis):
        mock_redis.get = AsyncMock(return_value="1000")
        result = await meter.check_rate_limit("user_42", daily_limit=1000)
        assert result["allowed"] is False

    @pytest.mark.asyncio
    async def test_no_redis(self):
        meter = TokenMeter(redis_client=None, db_pool=None)
        result = await meter.check_rate_limit("user_42", daily_limit=1000)
        assert result["allowed"] is True  # fail-open
