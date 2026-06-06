"""
Post-login hooks for pre-loading user data into cache.

Called after successful authentication (from the auth gateway or via the
/v1/auth/preload endpoint). Pre-loads voice examples and other per-user
data into Redis so that subsequent requests are served from cache.

Design constraints:
- Pre-load is fire-and-forget: it runs async and does NOT block the login response.
- Individual pre-load failures are non-blocking: other tasks continue.
- All cache entries use 24-hour TTL.
"""

from __future__ import annotations

import asyncio
import logging
from typing import TYPE_CHECKING, List

from intelligence.core.redis_client import get_redis

if TYPE_CHECKING:
    from intelligence.app.drafting.voice_retriever import VoiceRetriever

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Pre-load orchestration
# ---------------------------------------------------------------------------


async def on_user_login(
    user_id: str,
    voice_retriever: "VoiceRetriever",
) -> dict:
    """Called after successful authentication.

    Pre-loads per-user data into Redis in parallel:
        - Voice examples (top 10)  → key: voice:{user_id}:top10  TTL: 24h

    Fire-and-forget: this coroutine is scheduled as a background task so
    that the login response is not delayed. Callers should use::

        asyncio.create_task(on_user_login(user_id, voice_retriever))

    Args:
        user_id: The authenticated user's UUID (string).
        voice_retriever: VoiceRetriever instance for fetching voice examples.

    Returns:
        A summary dict with task results for observability.
    """
    logger.info("Starting post-login pre-load for user %s", user_id)
    started_at = asyncio.get_event_loop().time()

    tasks = [
        voice_retriever.preload_voice_examples(user_id),
    ]
    task_names = ["voice_examples"]

    results = await asyncio.gather(*tasks, return_exceptions=True)

    summary: dict = {"user_id": user_id, "tasks": {}}
    for name, result in zip(task_names, results):
        if isinstance(result, Exception):
            logger.warning(
                "Pre-load task '%s' failed for user %s: %s",
                name,
                user_id,
                result,
            )
            summary["tasks"][name] = {"status": "error", "error": str(result)}
        else:
            logger.info(
                "Pre-load task '%s' completed for user %s: %d items",
                name,
                user_id,
                len(result) if isinstance(result, list) else 1,
            )
            summary["tasks"][name] = {
                "status": "ok",
                "count": len(result) if isinstance(result, list) else 1,
            }

    elapsed = (asyncio.get_event_loop().time() - started_at) * 1000
    summary["elapsed_ms"] = round(elapsed, 2)
    logger.info(
        "Post-login pre-load complete for user %s in %.1fms",
        user_id,
        elapsed,
    )
    return summary


# ---------------------------------------------------------------------------
# Bulk cache warming (admin/ops use)
# ---------------------------------------------------------------------------


async def warm_cache_for_users(
    user_ids: List[str],
    voice_retriever: "VoiceRetriever",
) -> dict:
    """Warm the voice cache for a batch of users (admin/ops endpoint).

    Useful for:
        - Pre-warming cache after a deployment.
        - Bulk cache refresh before peak hours.
        - Recovering from a Redis cold start.

    Args:
        user_ids: List of user UUIDs to warm.
        voice_retriever: VoiceRetriever instance.

    Returns:
        Summary dict with success/error counts.
    """
    logger.info("Bulk cache warm starting for %d users", len(user_ids))

    results = await asyncio.gather(
        *[voice_retriever.preload_voice_examples(uid) for uid in user_ids],
        return_exceptions=True,
    )

    success_count = sum(
        1 for r in results if not isinstance(r, Exception)
    )
    error_count = len(results) - success_count

    logger.info(
        "Bulk cache warm complete: %d succeeded, %d failed (of %d users)",
        success_count,
        error_count,
        len(user_ids),
    )
    return {
        "total_users": len(user_ids),
        "succeeded": success_count,
        "failed": error_count,
    }


# ---------------------------------------------------------------------------
# Cache invalidation helpers
# ---------------------------------------------------------------------------


async def invalidate_user_cache(user_id: str) -> dict:
    """Invalidate all cached data for a user (e.g., on logout or data refresh).

    Args:
        user_id: The user's UUID.

    Returns:
        Summary of invalidated keys.
    """
    redis = await get_redis()
    keys_to_delete = [
        f"voice:{user_id}:top10",
        f"voice:{user_id}:top10",  # duplicate safety
    ]

    deleted = 0
    for key in set(keys_to_delete):
        try:
            result = await redis.delete(key)
            deleted += result
        except Exception as exc:
            logger.warning("Failed to delete cache key %s: %s", key, exc)

    logger.info("Invalidated %d cache keys for user %s", deleted, user_id)
    return {"user_id": user_id, "keys_deleted": deleted}
