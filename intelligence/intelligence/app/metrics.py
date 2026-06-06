"""
Prometheus metrics middleware for the Intelligence Layer.

Instruments all HTTP requests with latency histograms and counters,
exposing /metrics for Prometheus scraping.
"""

import time
from typing import Callable

from fastapi import FastAPI, Request
from prometheus_client import Counter, Histogram, generate_latest, CONTENT_TYPE_LATEST
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import Response

# ---------------------------------------------------------------------------
# Prometheus collectors
# ---------------------------------------------------------------------------

REQUEST_COUNT = Counter(
    "http_requests_total",
    "Total HTTP requests",
    ["method", "endpoint", "status_code"],
)

REQUEST_LATENCY = Histogram(
    "http_request_duration_seconds",
    "HTTP request latency",
    ["method", "endpoint"],
    buckets=[0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0],
)

ACTIVE_CONNECTIONS = Counter(
    "http_active_connections",
    "Number of active HTTP connections",
)


# ---------------------------------------------------------------------------
# Middleware
# ---------------------------------------------------------------------------


class PrometheusMiddleware(BaseHTTPMiddleware):
    """FastAPI middleware that records request metrics."""

    async def dispatch(self, request: Request, call_next: Callable) -> Response:
        method = request.method
        path = request.url.path

        # Skip metrics endpoint itself
        if path == "/metrics":
            return await call_next(request)

        start = time.perf_counter()
        ACTIVE_CONNECTIONS.inc()

        try:
            response = await call_next(request)
            status_code = str(response.status_code)
            return response
        except Exception:
            status_code = "500"
            raise
        finally:
            duration = time.perf_counter() - start
            REQUEST_COUNT.labels(method=method, endpoint=path, status_code=status_code).inc()
            REQUEST_LATENCY.labels(method=method, endpoint=path).observe(duration)
            ACTIVE_CONNECTIONS.dec()


# ---------------------------------------------------------------------------
# Metrics endpoint handler
# ---------------------------------------------------------------------------


async def metrics_endpoint() -> Response:
    """Expose Prometheus metrics in text format."""
    data = generate_latest()
    return Response(content=data, media_type=CONTENT_TYPE_LATEST)


# ---------------------------------------------------------------------------
# Install into FastAPI app
# ---------------------------------------------------------------------------


def install_metrics(app: FastAPI) -> None:
    """Add Prometheus middleware and /metrics endpoint to the app."""
    app.add_middleware(PrometheusMiddleware)
    app.add_route("/metrics", metrics_endpoint)
