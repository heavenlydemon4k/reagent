"""OCR engine that orchestrates extraction for both images and PDFs."""

import statistics

from app.image import ImageHandler
from app.models import OCRResult
from app.pdf import PDFHandler


class OCREngine:
    """Orchestrates OCR extraction for both images and PDFs."""

    TEXT_LAYER_CONFIDENCE = 0.95  # Text layer extraction is highly reliable
    MIN_CHARS_FOR_TEXT_LAYER = 50

    def __init__(self) -> None:
        self._image_handler = ImageHandler()
        self._pdf_handler = PDFHandler()

    async def extract_from_image(self, image_bytes: bytes, filename: str = "") -> OCRResult:
        """Extract text from an image file.

        Args:
            image_bytes: Raw image file bytes.
            filename: Original filename for metadata.

        Returns:
            OCRResult with extracted text and confidence score.
        """
        return await self._image_handler.extract(image_bytes, filename)

    async def extract_from_pdf(self, pdf_bytes: bytes, filename: str = "") -> OCRResult:
        """Extract text from a PDF, preferring text layer with OCR fallback.

        Strategy:
        1. Check if PDF has a meaningful text layer.
        2. If yes: extract text via pdfplumber (high confidence).
        3. If no / sparse: convert to images and run OCR on each page.
        4. Aggregate results from all pages.

        Args:
            pdf_bytes: Raw PDF file bytes.
            filename: Original filename for metadata.

        Returns:
            OCRResult with extracted text and averaged confidence score.
        """
        if self._pdf_handler.has_text_layer(pdf_bytes):
            text, page_count = self._pdf_handler.extract_text_layer(pdf_bytes)

            word_count = len(text.split())

            # If we got substantial text, return with high confidence
            if len(text.strip()) >= self.MIN_CHARS_FOR_TEXT_LAYER:
                return OCRResult(
                    text=text,
                    confidence=self.TEXT_LAYER_CONFIDENCE,
                    word_count=word_count,
                    page_count=page_count,
                    flagged_for_review=False,
                    metadata={
                        "filename": filename,
                        "extraction_method": "text_layer",
                        "page_count": page_count,
                    },
                )

        # Fallback: convert to images and OCR each page
        image_bytes_list = await self._pdf_handler.convert_to_images(pdf_bytes)
        page_count = len(image_bytes_list)

        results: list[OCRResult] = []
        for idx, img_bytes in enumerate(image_bytes_list):
            page_result = await self._image_handler.extract(
                img_bytes,
                filename=f"{filename}_page_{idx + 1}",
            )
            page_result.metadata["page"] = idx + 1
            results.append(page_result)

        # Aggregate results
        all_texts = [r.text for r in results if r.text]
        aggregated_text = "\n\n".join(all_texts)

        if results:
            # Average confidence weighted by word count
            total_words = sum(r.word_count for r in results)
            if total_words > 0:
                avg_confidence = sum(
                    r.confidence * r.word_count for r in results
                ) / total_words
            else:
                avg_confidence = statistics.mean(
                    [r.confidence for r in results]
                ) if results else 0.0
        else:
            avg_confidence = 0.0

        total_word_count = sum(r.word_count for r in results)

        return OCRResult(
            text=aggregated_text,
            confidence=round(avg_confidence, 4),
            word_count=total_word_count,
            page_count=page_count,
            flagged_for_review=avg_confidence < 0.7,
            metadata={
                "filename": filename,
                "extraction_method": "ocr_fallback",
                "page_count": page_count,
                "pages": [r.metadata for r in results],
            },
        )
