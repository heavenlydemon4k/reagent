"""Bridge module — re-exports NATSClient from the top-level infra package."""
from __future__ import annotations

import sys
import os

# Add project root to path so we can import the top-level infra package
_project_root = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "..", "..")
)
if _project_root not in sys.path:
    sys.path.insert(0, _project_root)

# ruff: noqa: E402
from infra.queue.nats_client import NATSClient as NATSClient

__all__ = ["NATSClient"]
