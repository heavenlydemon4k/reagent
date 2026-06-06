"""Health check utilities."""

import shutil

import pytesseract

from app import __version__
from app.models import HealthResponse


def get_tesseract_version() -> str | None:
    """Get the installed tesseract version, or None if not available."""
    tesseract_path = shutil.which("tesseract")
    if not tesseract_path:
        return None
    try:
        return pytesseract.get_tesseract_version()
    except Exception:
        return None


def build_health_response() -> HealthResponse:
    """Build a health check response."""
    tesseract_path = shutil.which("tesseract")
    version = get_tesseract_version()

    if tesseract_path and version:
        status = "healthy"
    else:
        status = "degraded"

    return HealthResponse(
        status=status,
        version=__version__,
        tesseract_version=str(version) if version else None,
    )
