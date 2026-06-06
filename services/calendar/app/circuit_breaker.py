"""Circuit breaker pattern implementation for external service calls.

Prevents cascading failures by stopping calls to failing services.
States:
  - CLOSED:   Normal operation, calls pass through.
  - OPEN:     Calls fail fast without reaching the service.
  - HALF_OPEN: A trial call is allowed to test if the service recovered.

Usage:
    cb = CircuitBreaker("google_calendar", failure_threshold=5, reset_timeout=30)
    try:
        result = cb.call(lambda: fetch_events())
    except CircuitBreakerOpen:
        # Service is down, use fallback
        pass
"""

from __future__ import annotations

import logging
import threading
import time
from enum import Enum
from typing import Any, Callable, Optional

logger = logging.getLogger(__name__)


class CircuitState(Enum):
    """States of a circuit breaker."""

    CLOSED = "closed"
    OPEN = "open"
    HALF_OPEN = "half-open"


class CircuitBreakerOpen(Exception):
    """Raised when the circuit breaker is open and a call is attempted."""

    def __init__(self, name: str, state: str, last_failure_ago: float) -> None:
        self.name = name
        self.state = state
        self.last_failure_ago = last_failure_ago
        super().__init__(
            f"Circuit breaker {name!r} is {state} "
            f"(last failure: {last_failure_ago:.1f}s ago)"
        )


class CircuitBreaker:
    """Protects external service calls from cascading failures.

    Args:
        name: Human-readable name for logging/metrics.
        failure_threshold: Number of consecutive failures before opening.
        reset_timeout: Seconds to wait before attempting half-open.
        half_open_max_calls: Max calls allowed in half-open state.
    """

    def __init__(
        self,
        name: str,
        failure_threshold: int = 5,
        reset_timeout: float = 30.0,
        half_open_max_calls: int = 3,
    ) -> None:
        self.name = name
        self.failure_threshold = failure_threshold
        self.reset_timeout = reset_timeout
        self.half_open_max_calls = half_open_max_calls

        self._state = CircuitState.CLOSED
        self._failures = 0
        self._last_failure_time: Optional[float] = None
        self._half_open_calls = 0
        self._lock = threading.RLock()

    # ------------------------------------------------------------------
    # State queries
    # ------------------------------------------------------------------

    @property
    def state(self) -> CircuitState:
        """Current state (thread-safe)."""
        with self._lock:
            return self._current_state()

    def is_open(self) -> bool:
        """Return True if the circuit is currently open."""
        return self.state == CircuitState.OPEN

    def is_closed(self) -> bool:
        """Return True if the circuit is currently closed."""
        return self.state == CircuitState.CLOSED

    # ------------------------------------------------------------------
    # Core API
    # ------------------------------------------------------------------

    def call(self, fn: Callable[..., Any], *args: Any, **kwargs: Any) -> Any:
        """Execute *fn* if the circuit allows it.

        Raises CircuitBreakerOpen if the breaker is open.
        Any exception raised by *fn* counts as a failure and is re-raised.
        """
        if not self._acquire_permit():
            last_ago = time.time() - (self._last_failure_time or time.time())
            raise CircuitBreakerOpen(
                self.name, self._state.value, last_ago
            )

        try:
            result = fn(*args, **kwargs)
        except Exception:
            self._record_failure()
            raise

        self._record_success()
        return result

    async def acall(self, fn: Callable[..., Any], *args: Any, **kwargs: Any) -> Any:
        """Async variant of ``call`` — executes *fn* if circuit allows it."""
        if not self._acquire_permit():
            last_ago = time.time() - (self._last_failure_time or time.time())
            raise CircuitBreakerOpen(
                self.name, self._state.value, last_ago
            )

        try:
            result = fn(*args, **kwargs)
        except Exception:
            self._record_failure()
            raise

        self._record_success()
        return result

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _current_state(self) -> CircuitState:
        """Compute current state, transitioning OPEN → HALF_OPEN if timeout elapsed."""
        if (
            self._state == CircuitState.OPEN
            and self._last_failure_time is not None
            and time.time() - self._last_failure_time > self.reset_timeout
        ):
            self._state = CircuitState.HALF_OPEN
            self._half_open_calls = 0
        return self._state

    def _acquire_permit(self) -> bool:
        """Return True if the caller may proceed with the external call."""
        with self._lock:
            state = self._current_state()
            if state == CircuitState.OPEN:
                return False
            if state == CircuitState.HALF_OPEN:
                if self._half_open_calls >= self.half_open_max_calls:
                    return False
                self._half_open_calls += 1
            return True

    def _record_failure(self) -> None:
        with self._lock:
            self._failures += 1
            self._last_failure_time = time.time()
            if self._failures >= self.failure_threshold:
                self._state = CircuitState.OPEN
                logger.warning(
                    f"Circuit breaker {self.name!r} OPENED after "
                    f"{self._failures} consecutive failures"
                )

    def _record_success(self) -> None:
        with self._lock:
            self._failures = 0
            self._state = CircuitState.CLOSED
            self._half_open_calls = 0


# ---------------------------------------------------------------------------
# Presets for known integrations
# ---------------------------------------------------------------------------

PRESETS: dict[str, dict[str, Any]] = {
    "google_calendar": {"failure_threshold": 5, "reset_timeout": 30.0, "half_open_max_calls": 3},
    "outlook_calendar": {"failure_threshold": 5, "reset_timeout": 30.0, "half_open_max_calls": 3},
    "deepgram": {"failure_threshold": 5, "reset_timeout": 30.0, "half_open_max_calls": 3},
    "elevenlabs": {"failure_threshold": 5, "reset_timeout": 30.0, "half_open_max_calls": 3},
}


def get_preset(name: str) -> dict[str, Any]:
    """Return preset configuration for a named integration."""
    return PRESETS.get(name, {"failure_threshold": 5, "reset_timeout": 30.0, "half_open_max_calls": 3})
