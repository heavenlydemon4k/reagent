"""Pydantic request/response models for the OCR service."""

from typing import Literal

from pydantic import BaseModel, Field


class OCRRequest(BaseModel):
    """OCR request metadata (file is uploaded as multipart/form-data)."""

    file_type: Literal["image", "pdf"] = Field(
        ...,
        description="Type of file to process",
    )


class OCRResult(BaseModel):
    """OCR extraction result with confidence scoring."""

    text: str = Field(..., description="Extracted text content")
    confidence: float = Field(
        ...,
        ge=0.0,
        le=1.0,
        description="Confidence score (0.0-1.0)",
    )
    word_count: int = Field(..., ge=0, description="Number of words extracted")
    page_count: int = Field(
        default=1,
        ge=1,
        description="Number of pages processed",
    )
    flagged_for_review: bool = Field(
        default=False,
        description="True if confidence < 0.7",
    )
    metadata: dict = Field(
        default_factory=dict,
        description="Additional extraction metadata",
    )


class HealthResponse(BaseModel):
    """Health check response."""

    status: str
    service: str = "ocr"
    version: str
    tesseract_version: str | None
