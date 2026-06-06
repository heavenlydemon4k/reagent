"""FastAPI application entry point for the OCR service."""

from contextlib import asynccontextmanager

import pytesseract
from fastapi import FastAPI

from app import __version__
from app.router import router
from core.config import settings
from core.logging_config import configure_logging, get_logger

logger = get_logger(__name__)


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Manage application lifecycle."""
    # Startup
    configure_logging(settings.log_level)
    logger.info(
        "OCR service starting",
        version=__version__,
        host=settings.host,
        port=settings.port,
        log_level=settings.log_level,
    )

    # Verify tesseract is available
    try:
        version = pytesseract.get_tesseract_version()
        logger.info("Tesseract OCR available", version=str(version))
    except Exception as exc:
        logger.warning(
            "Tesseract OCR not available - image/PDF OCR will fail",
            error=str(exc),
        )

    yield

    # Shutdown
    logger.info("OCR service shutting down")


app = FastAPI(
    title="Decision Stack OCR Service",
    description="Extract text from images and PDFs with confidence scoring.",
    version=__version__,
    lifespan=lifespan,
    docs_url="/docs",
    redoc_url="/redoc",
)
app.include_router(router, prefix="/v1")
