"""Bridge module — re-exports FallbackChain from the top-level core package."""
from __future__ import annotations

import sys
import os

# Add project root to path so we can import the top-level core package
_project_root = os.path.abspath(
    os.path.join(os.path.dirname(__file__), "..", "..", "..")
)
if _project_root not in sys.path:
    sys.path.insert(0, _project_root)

# ruff: noqa: E402
from core.fallback_chain import FallbackChain as FallbackChain
from core.fallback_chain import PendingLLMTask

__all__ = ["FallbackChain", "PendingLLMTask"]
