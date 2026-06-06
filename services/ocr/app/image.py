"""Image OCR handler using pytesseract."""

import io
import statistics

import pytesseract
from PIL import Image

from app.models import OCRResult


class ImageHandler:
    """Handles image OCR using pytesseract."""

    async def extract(self, image_bytes: bytes, filename: str = "") -> OCRResult:
        """Extract text from image bytes with confidence scoring.

        Uses pytesseract.image_to_data() to get per-word confidence scores,
        then calculates a weighted average confidence.

        Args:
            image_bytes: Raw image file bytes.
            filename: Original filename for metadata.

        Returns:
            OCRResult with extracted text and confidence score.
        """
        image = Image.open(io.BytesIO(image_bytes))

        # Convert to RGB if needed (handles RGBA, palette, etc.)
        if image.mode not in ("RGB", "L"):
            image = image.convert("RGB")

        # Run pytesseract with detailed output
        data = pytesseract.image_to_data(
            image,
            output_type=pytesseract.Output.DICT,
        )

        words = []
        confidences = []

        n_boxes = len(data["text"])
        for i in range(n_boxes):
            word = data["text"][i].strip()
            conf = int(data["conf"][i])

            # Filter out empty strings and low-confidence words
            if word and conf > 30:
                words.append(word)
                confidences.append(conf)

        text = " ".join(words)
        word_count = len(words)

        if confidences:
            # Use median confidence for robustness, then normalize to 0-1
            avg_confidence = statistics.median(confidences) / 100.0
            # Clamp to valid range
            avg_confidence = max(0.0, min(1.0, avg_confidence))
        else:
            avg_confidence = 0.0

        flagged_for_review = avg_confidence < 0.7

        return OCRResult(
            text=text,
            confidence=round(avg_confidence, 4),
            word_count=word_count,
            page_count=1,
            flagged_for_review=flagged_for_review,
            metadata={
                "filename": filename,
                "image_size": image.size,
                "image_mode": image.mode,
                "words_detected": n_boxes,
                "high_confidence_words": len(confidences),
            },
        )
