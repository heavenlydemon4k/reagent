"""API routes for the OCR service."""

from fastapi import APIRouter, File, HTTPException, UploadFile

from app.engine import OCREngine
from app.health import build_health_response
from app.models import OCRResult, HealthResponse
from core.config import settings
from core.logging_config import get_logger

router = APIRouter()
logger = get_logger(__name__)

# Supported MIME types and extensions
SUPPORTED_IMAGE_TYPES = {
    "image/png",
    "image/jpeg",
    "image/jpg",
    "image/tiff",
    "image/bmp",
    "image/gif",
    "image/webp",
}
SUPPORTED_PDF_TYPES = {"application/pdf"}
SUPPORTED_EXTENSIONS = {
    ".png", ".jpg", ".jpeg", ".tiff", ".tif", ".bmp", ".gif", ".webp", ".pdf"
}

MAX_FILE_SIZE = settings.max_file_size_mb * 1024 * 1024

engine = OCREngine()


def _detect_file_type(file: UploadFile, file_type_hint: str) -> str:
    """Determine whether a file is an image or PDF.

    Args:
        file: The uploaded file.
        file_type_hint: User-provided hint ("auto", "image", "pdf").

    Returns:
        "image" or "pdf".

    Raises:
        HTTPException: If file type cannot be determined or is unsupported.
    """
    if file_type_hint == "image":
        return "image"
    if file_type_hint == "pdf":
        return "pdf"

    # Auto-detect from content-type
    content_type = (file.content_type or "").lower()
    if content_type in SUPPORTED_IMAGE_TYPES:
        return "image"
    if content_type in SUPPORTED_PDF_TYPES:
        return "pdf"

    # Fallback: detect from filename extension
    filename = (file.filename or "").lower()
    if filename.endswith(".pdf"):
        return "pdf"
    if any(filename.endswith(ext) for ext in (".png", ".jpg", ".jpeg", ".tiff", ".tif", ".bmp", ".gif", ".webp")):
        return "image"

    raise HTTPException(
        status_code=400,
        detail=f"Unsupported file type. Content-Type: {content_type}. "
               f"Supported: images (png, jpg, tiff, bmp, gif, webp) and PDF.",
    )


@router.post("/ocr", response_model=OCRResult)
async def ocr_endpoint(
    file: UploadFile = File(...),
    file_type: str = "auto",
):
    """Extract text from an uploaded image or PDF file.

    - **file**: The image or PDF file to process.
    - **file_type**: Hint for the file type ("auto", "image", or "pdf").
        Default is "auto" which detects from MIME type or extension.

    Returns the extracted text with confidence scores.
    Results with confidence < 0.7 are flagged for review but still returned.
    """
    logger.info(
        "OCR request received",
        filename=file.filename,
        content_type=file.content_type,
        file_type_hint=file_type,
    )

    # Validate file type hint
    if file_type not in ("auto", "image", "pdf"):
        raise HTTPException(
            status_code=400,
            detail=f"Invalid file_type: {file_type}. Use 'auto', 'image', or 'pdf'.",
        )

    # Detect actual file type
    detected_type = _detect_file_type(file, file_type)

    # Read file bytes
    file_bytes = await file.read()

    # Validate file size
    if len(file_bytes) > MAX_FILE_SIZE:
        raise HTTPException(
            status_code=413,
            detail=f"File too large. Max size: {settings.max_file_size_mb}MB. "
                   f"Received: {len(file_bytes) / (1024 * 1024):.1f}MB.",
        )

    if len(file_bytes) == 0:
        raise HTTPException(status_code=400, detail="Empty file uploaded.")

    logger.info(
        "Processing file",
        filename=file.filename,
        detected_type=detected_type,
        size_bytes=len(file_bytes),
    )

    try:
        if detected_type == "image":
            result = await engine.extract_from_image(file_bytes, file.filename or "")
        else:
            result = await engine.extract_from_pdf(file_bytes, file.filename or "")
    except HTTPException:
        raise
    except Exception as exc:
        logger.error(
            "OCR processing failed",
            filename=file.filename,
            error=str(exc),
            exc_info=True,
        )
        raise HTTPException(
            status_code=500,
            detail=f"OCR processing failed: {str(exc)}",
        )

    logger.info(
        "OCR processing complete",
        filename=file.filename,
        word_count=result.word_count,
        confidence=result.confidence,
        flagged=result.flagged_for_review,
    )

    return result


@router.get("/health", response_model=HealthResponse)
async def health() -> HealthResponse:
    """Return service health status and tesseract version."""
    return build_health_response()
