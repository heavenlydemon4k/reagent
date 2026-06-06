"""Circuit breaker for Deepgram API calls."""

from __future__ import annotations

import logging
import threading
import time
from enum import Enum
from typing import Any, Callable, Optional

logger = logging.getLogger("stt.circuit_breaker")


class CircuitState(Enum):
    """States of a circuit breaker."""

    CLOSED = "closed"
    OPEN = "open"
    HALF_OPEN = "half-open"


class CircuitBreakerOpen(Exception):
    """Raised when the circuit breaker is open."""

    def __init__(self, name: str, state: str, last_failure_ago: float) -> None:
        self.name = name
        self.state = state
        self.last_failure_ago = last_failure_ago
        super().__init__(
            f"Circuit breaker {name!r} is {state} "
            f"(last failure: {last_failure_ago:.1f}s ago)"
        )


class CircuitBreaker:
    """Protects external API calls from cascading failures."""

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

    @property
    def state(self) -> CircuitState:
        with self._lock:
            return self._current_state()

    def is_open(self) -> bool:
        return self.state == CircuitState.OPEN

    def _current_state(self) -> CircuitState:
        if (
            self._state == CircuitState.OPEN
            and self._last_failure_time is not None
            and time.time() - self._last_failure_time > self.reset_timeout
        ):
            self._state = CircuitState.HALF_OPEN
            self._half_open_calls = 0
        return self._state

    def _acquire_permit(self) -> bool:
        with self._lock:
            state = self._current_state()
            if state == CircuitState.OPEN:
                return False
            if state == CircuitState.HALF_OPEN:
                if self._half_open_calls >= self.half_open_max_calls:
                    return False
                self._half_open_calls += 1
            return True

    def call(self, fn: Callable[..., Any], *args: Any, **kwargs: Any) -> Any:
        if not self._acquire_permit():
            last_ago = time.time() - (self._last_failure_time or time.time())
            raise CircuitBreakerOpen(self.name, self._state.value, last_ago)
        try:
            result = fn(*args, **kwargs)
        except Exception:
            self._record_failure()
            raise
        self._record_success()
        return result

    async def acall(self, fn: Callable[..., Any], *args: Any, **kwargs: Any) -> Any:
        if not self._acquire_permit():
            last_ago = time.time() - (self._last_failure_time or time.time())
            raise CircuitBreakerOpen(self.name, self._state.value, last_ago)
        try:
            result = await fn(*args, **kwargs)
        except Exception:
            self._record_failure()
            raise
        self._record_success()
        return result

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


# Shared circuit breaker for Deepgram
deepgram_breaker = CircuitBreaker("deepgram", failure_threshold=5, reset_timeout=30.0)
