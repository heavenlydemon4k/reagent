"""OCR engine tests with mocked dependencies."""

import io
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from app.engine import OCREngine
from app.models import OCRResult


class TestOCREngineImage:
    """Tests for OCREngine.extract_from_image with mocked pytesseract."""

    @pytest.fixture
    def engine(self):
        """Create a fresh OCREngine instance."""
        return OCREngine()

    @pytest.fixture
    def fake_image_bytes(self):
        """Create minimal fake PNG bytes."""
        return (
            b"\x89PNG\r\n\x1a\n"
            b"\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"
            b"\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N"
            b"\x00\x00\x00\x00IEND\xaeB`\x82"
        )

    @pytest.mark.asyncio
    @patch("app.image.pytesseract.image_to_data")
    @patch("app.image.Image.open")
    async def test_extract_from_image_success(
        self, mock_open, mock_image_to_data, engine, fake_image_bytes
    ):
        """Successful image extraction should return OCRResult with confidence."""
        mock_img = MagicMock()
        mock_img.mode = "RGB"
        mock_img.size = (100, 50)
        mock_open.return_value = mock_img

        mock_image_to_data.return_value = {
            "text": ["Hello", "World", ""],
            "conf": [95, 88, -1],
        }

        result = await engine.extract_from_image(fake_image_bytes, "test.png")

        assert isinstance(result, OCRResult)
        assert result.text == "Hello World"
        assert result.word_count == 2
        assert 0.0 <= result.confidence <= 1.0
        assert result.page_count == 1
        assert result.flagged_for_review is False

    @pytest.mark.asyncio
    @patch("app.image.pytesseract.image_to_data")
    @patch("app.image.Image.open")
    async def test_extract_from_image_low_confidence_flagged(
        self, mock_open, mock_image_to_data, engine, fake_image_bytes
    ):
        """Low confidence results should be flagged for review."""
        mock_img = MagicMock()
        mock_img.mode = "RGB"
        mock_img.size = (100, 50)
        mock_open.return_value = mock_img

        mock_image_to_data.return_value = {
            "text": [" blurry ", "text"],
            "conf": [45, 50],
        }

        result = await engine.extract_from_image(fake_image_bytes, "test.png")

        assert isinstance(result, OCRResult)
        assert result.confidence < 0.7
        assert result.flagged_for_review is True

    @pytest.mark.asyncio
    @patch("app.image.pytesseract.image_to_data")
    @patch("app.image.Image.open")
    async def test_extract_from_image_rgba_conversion(
        self, mock_open, mock_image_to_data, engine, fake_image_bytes
    ):
        """RGBA images should be converted to RGB before OCR."""
        mock_img = MagicMock()
        mock_img.mode = "RGBA"
        mock_img.size = (100, 50)
        mock_open.return_value = mock_img

        mock_image_to_data.return_value = {
            "text": ["Test"],
            "conf": [90],
        }

        result = await engine.extract_from_image(fake_image_bytes, "test.png")

        mock_img.convert.assert_called_once_with("RGB")
        assert isinstance(result, OCRResult)
        assert result.text == "Test"

    @pytest.mark.asyncio
    @patch("app.image.pytesseract.image_to_data")
    @patch("app.image.Image.open")
    async def test_extract_from_image_empty_result(
        self, mock_open, mock_image_to_data, engine, fake_image_bytes
    ):
        """Empty image with no text should return zero-confidence result."""
        mock_img = MagicMock()
        mock_img.mode = "RGB"
        mock_img.size = (100, 50)
        mock_open.return_value = mock_img

        mock_image_to_data.return_value = {
            "text": ["", "", ""],
            "conf": [-1, -1, -1],
        }

        result = await engine.extract_from_image(fake_image_bytes, "test.png")

        assert isinstance(result, OCRResult)
        assert result.text == ""
        assert result.confidence == 0.0
        assert result.word_count == 0
        assert result.flagged_for_review is True

    @pytest.mark.asyncio
    @patch("app.image.pytesseract.image_to_data")
    @patch("app.image.Image.open")
    async def test_extract_from_image_filters_low_confidence_words(
        self, mock_open, mock_image_to_data, engine, fake_image_bytes
    ):
        """Words with confidence <= 30 should be filtered out."""
        mock_img = MagicMock()
        mock_img.mode = "RGB"
        mock_img.size = (100, 50)
        mock_open.return_value = mock_img

        mock_image_to_data.return_value = {
            "text": ["good", "bad", "ok"],
            "conf": [90, 25, 85],
        }

        result = await engine.extract_from_image(fake_image_bytes, "test.png")

        assert "good" in result.text
        assert "ok" in result.text
        assert "bad" not in result.text


class TestOCREnginePDF:
    """Tests for OCREngine.extract_from_pdf with mocked dependencies."""

    @pytest.fixture
    def engine(self):
        """Create a fresh OCREngine instance."""
        return OCREngine()

    @pytest.fixture
    def fake_pdf_bytes(self):
        """Create minimal valid PDF bytes."""
        return (
            b"%PDF-1.4\n"
            b"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
            b"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n"
            b"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>\nendobj\n"
            b"xref\n0 4\n0000000000 65535 f \n"
            b"0000000009 00000 n \n"
            b"0000000058 00000 n \n"
            b"0000000115 00000 n \n"
            b"trailer\n<< /Size 4 /Root 1 0 R >>\n"
            b"startxref\n196\n%%EOF\n"
        )

    @pytest.mark.asyncio
    @patch("app.engine.PDFHandler.has_text_layer")
    @patch("app.engine.PDFHandler.extract_text_layer")
    async def test_extract_from_pdf_text_layer(
        self, mock_extract_text, mock_has_text, engine, fake_pdf_bytes
    ):
        """PDF with text layer should use text extraction (high confidence)."""
        mock_has_text.return_value = True
        mock_extract_text.return_value = (
            "This is a test PDF with text content.\nMultiple lines here.",
            1,
        )

        result = await engine.extract_from_pdf(fake_pdf_bytes, "test.pdf")

        assert isinstance(result, OCRResult)
        assert "test PDF" in result.text
        assert result.confidence == 0.95
        assert result.page_count == 1
        assert result.flagged_for_review is False
        assert result.metadata["extraction_method"] == "text_layer"

    @pytest.mark.asyncio
    @patch("app.engine.PDFHandler.has_text_layer")
    @patch("app.engine.PDFHandler.extract_text_layer")
    async def test_extract_from_pdf_sparse_text_fallback(
        self, mock_extract_text, mock_has_text, engine, fake_pdf_bytes
    ):
        """PDF with sparse text should fall back to OCR."""
        mock_has_text.return_value = True
        mock_extract_text.return_value = ("Hi", 1)  # Too short, triggers fallback

        with patch.object(
            engine._pdf_handler, "convert_to_images", new_callable=AsyncMock
        ) as mock_convert:
            with patch.object(
                engine._image_handler, "extract", new_callable=AsyncMock
            ) as mock_img_extract:
                mock_convert.return_value = [b"fake_image_png_bytes"]
                mock_img_extract.return_value = OCRResult(
                    text="OCR extracted text",
                    confidence=0.85,
                    word_count=3,
                    page_count=1,
                    flagged_for_review=False,
                )

                result = await engine.extract_from_pdf(fake_pdf_bytes, "test.pdf")

        assert isinstance(result, OCRResult)
        assert result.metadata["extraction_method"] == "ocr_fallback"

    @pytest.mark.asyncio
    @patch("app.engine.PDFHandler.has_text_layer")
    async def test_extract_from_pdf_scanned_fallback(self, mock_has_text, engine, fake_pdf_bytes):
        """Scanned PDF (no text layer) should use OCR fallback."""
        mock_has_text.return_value = False

        with patch.object(
            engine._pdf_handler, "convert_to_images", new_callable=AsyncMock
        ) as mock_convert:
            with patch.object(
                engine._image_handler, "extract", new_callable=AsyncMock
            ) as mock_img_extract:
                mock_convert.return_value = [b"page1_png", b"page2_png"]
                mock_img_extract.side_effect = [
                    OCRResult(
                        text="Page one content",
                        confidence=0.90,
                        word_count=3,
                        page_count=1,
                        flagged_for_review=False,
                    ),
                    OCRResult(
                        text="Page two content",
                        confidence=0.80,
                        word_count=3,
                        page_count=1,
                        flagged_for_review=False,
                    ),
                ]

                result = await engine.extract_from_pdf(fake_pdf_bytes, "scanned.pdf")

        assert isinstance(result, OCRResult)
        assert result.page_count == 2
        assert "Page one" in result.text
        assert "Page two" in result.text
        assert result.metadata["extraction_method"] == "ocr_fallback"

    @pytest.mark.asyncio
    @patch("app.engine.PDFHandler.has_text_layer")
    async def test_extract_from_pdf_multipage_confidence_weighted(
        self, mock_has_text, engine, fake_pdf_bytes
    ):
        """Multi-page PDF should average confidence weighted by word count."""
        mock_has_text.return_value = False

        with patch.object(
            engine._pdf_handler, "convert_to_images", new_callable=AsyncMock
        ) as mock_convert:
            with patch.object(
                engine._image_handler, "extract", new_callable=AsyncMock
            ) as mock_img_extract:
                mock_convert.return_value = [b"p1", b"p2"]
                # Page 1: 10 words at 0.9 confidence
                # Page 2: 90 words at 0.5 confidence
                # Weighted avg: (0.9*10 + 0.5*90) / 100 = 0.54
                mock_img_extract.side_effect = [
                    OCRResult(
                        text="a b c d e f g h i j",
                        confidence=0.9,
                        word_count=10,
                        page_count=1,
                        flagged_for_review=False,
                    ),
                    OCRResult(
                        text="x " * 90,
                        confidence=0.5,
                        word_count=90,
                        page_count=1,
                        flagged_for_review=True,
                    ),
                ]

                result = await engine.extract_from_pdf(fake_pdf_bytes, "weighted.pdf")

        assert isinstance(result, OCRResult)
        assert result.word_count == 100
        assert result.page_count == 2
        # Weighted average: (0.9*10 + 0.5*90) / 100 = 54/100 = 0.54
        assert result.confidence == pytest.approx(0.54, abs=0.01)
        assert result.flagged_for_review is True


class TestOCREngineInitialization:
    """Tests for OCREngine setup."""

    def test_engine_has_handlers(self):
        """Engine should initialize with image and PDF handlers."""
        engine = OCREngine()
        assert engine._image_handler is not None
        assert engine._pdf_handler is not None

    def test_text_layer_confidence_constant(self):
        """Text layer confidence should be high."""
        assert OCREngine.TEXT_LAYER_CONFIDENCE == 0.95
