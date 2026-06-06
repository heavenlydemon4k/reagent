"""API endpoint tests for the OCR service."""

import io
from unittest.mock import patch

import pytest
from fastapi.testclient import TestClient

from app.main import app
from app.models import OCRResult

client = TestClient(app)


class TestHealthEndpoint:
    """Tests for the /health endpoint."""

    def test_health_returns_200(self):
        """Health endpoint should return 200 OK."""
        response = client.get("/v1/health")
        assert response.status_code == 200

    def test_health_response_structure(self):
        """Health response should have the expected structure."""
        response = client.get("/v1/health")
        data = response.json()

        assert "status" in data
        assert "service" in data
        assert "version" in data
        assert "tesseract_version" in data
        assert data["service"] == "ocr"

    def test_health_status_values(self):
        """Health status should be healthy or degraded based on tesseract."""
        response = client.get("/v1/health")
        data = response.json()
        assert data["status"] in ("healthy", "degraded")


class TestOCREndpoint:
    """Tests for the /ocr endpoint."""

    @pytest.fixture
    def mock_ocr_result(self):
        """Create a sample OCR result for mocking."""
        return OCRResult(
            text="Hello World",
            confidence=0.95,
            word_count=2,
            page_count=1,
            flagged_for_review=False,
            metadata={"test": True},
        )

    def test_ocr_empty_file(self):
        """OCR endpoint should reject empty files."""
        response = client.post(
            "/v1/ocr",
            files={"file": ("test.png", io.BytesIO(b""), "image/png")},
        )
        assert response.status_code == 400
        assert "empty" in response.json()["detail"].lower()

    def test_ocr_unsupported_file_type(self):
        """OCR endpoint should reject unsupported file types."""
        response = client.post(
            "/v1/ocr",
            files={"file": ("test.txt", io.BytesIO(b"some text"), "text/plain")},
            data={"file_type": "auto"},
        )
        assert response.status_code == 400
        assert "unsupported" in response.json()["detail"].lower()

    def test_ocr_invalid_file_type_hint(self):
        """OCR endpoint should reject invalid file_type hints."""
        response = client.post(
            "/v1/ocr?file_type=invalid_type",
            files={"file": ("test.png", io.BytesIO(b"fake png data"), "image/png")},
        )
        assert response.status_code == 400
        assert "Invalid file_type" in response.json()["detail"]

    @patch("app.router.engine.extract_from_image")
    def test_ocr_image_success(self, mock_extract, mock_ocr_result):
        """OCR endpoint should process images and return results."""
        mock_extract.return_value = mock_ocr_result

        # Create a minimal valid PNG (1x1 pixel)
        # PNG signature + IHDR + IDAT + IEND chunks
        png_data = (
            b"\x89PNG\r\n\x1a\n"  # PNG signature
            b"\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"
            b"\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N"
            b"\x00\x00\x00\x00IEND\xaeB`\x82"
        )

        response = client.post(
            "/v1/ocr",
            files={"file": ("test.png", io.BytesIO(png_data), "image/png")},
            data={"file_type": "image"},
        )

        assert response.status_code == 200
        data = response.json()
        assert data["text"] == "Hello World"
        assert data["confidence"] == 0.95
        assert data["word_count"] == 2
        assert data["flagged_for_review"] is False
        mock_extract.assert_called_once()

    @patch("app.router.engine.extract_from_pdf")
    def test_ocr_pdf_success(self, mock_extract, mock_ocr_result):
        """OCR endpoint should process PDFs and return results."""
        mock_extract.return_value = mock_ocr_result

        # Minimal PDF structure
        pdf_data = (
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

        response = client.post(
            "/v1/ocr",
            files={"file": ("test.pdf", io.BytesIO(pdf_data), "application/pdf")},
            data={"file_type": "pdf"},
        )

        assert response.status_code == 200
        data = response.json()
        assert data["text"] == "Hello World"
        assert data["confidence"] == 0.95
        mock_extract.assert_called_once()

    @patch("app.router.engine.extract_from_image")
    def test_ocr_flagged_for_review(self, mock_extract):
        """OCR result with confidence < 0.7 should be flagged."""
        mock_extract.return_value = OCRResult(
            text="Some text",
            confidence=0.55,
            word_count=2,
            flagged_for_review=True,
        )

        png_data = (
            b"\x89PNG\r\n\x1a\n"
            b"\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"
            b"\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N"
            b"\x00\x00\x00\x00IEND\xaeB`\x82"
        )

        response = client.post(
            "/v1/ocr",
            files={"file": ("test.png", io.BytesIO(png_data), "image/png")},
            data={"file_type": "image"},
        )

        assert response.status_code == 200
        data = response.json()
        assert data["confidence"] == 0.55
        assert data["flagged_for_review"] is True

    def test_ocr_file_size_limit(self):
        """OCR endpoint should reject files exceeding the size limit."""
        large_bytes = b"x" * (11 * 1024 * 1024)  # 11 MB

        response = client.post(
            "/v1/ocr",
            files={"file": ("large.png", io.BytesIO(large_bytes), "image/png")},
        )

        assert response.status_code == 413
        assert "too large" in response.json()["detail"].lower()

    @patch("app.router.engine.extract_from_image")
    def test_ocr_auto_detect_image(self, mock_extract, mock_ocr_result):
        """Auto-detection should identify PNG files as images."""
        mock_extract.return_value = mock_ocr_result

        png_data = (
            b"\x89PNG\r\n\x1a\n"
            b"\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"
            b"\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N"
            b"\x00\x00\x00\x00IEND\xaeB`\x82"
        )

        response = client.post(
            "/v1/ocr",
            files={"file": ("test.png", io.BytesIO(png_data), "image/png")},
            data={"file_type": "auto"},
        )

        assert response.status_code == 200
        mock_extract.assert_called_once()

    @patch("app.router.engine.extract_from_pdf")
    def test_ocr_auto_detect_pdf(self, mock_extract, mock_ocr_result):
        """Auto-detection should identify PDF files."""
        mock_extract.return_value = mock_ocr_result

        pdf_data = (
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

        response = client.post(
            "/v1/ocr",
            files={"file": ("document.pdf", io.BytesIO(pdf_data), "application/pdf")},
            data={"file_type": "auto"},
        )

        assert response.status_code == 200
        mock_extract.assert_called_once()

    @patch("app.router.engine.extract_from_image")
    def test_ocr_internal_error_handling(self, mock_extract):
        """OCR endpoint should handle internal errors gracefully."""
        mock_extract.side_effect = RuntimeError("Processing failed")

        png_data = (
            b"\x89PNG\r\n\x1a\n"
            b"\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde"
            b"\x00\x00\x00\x0cIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N"
            b"\x00\x00\x00\x00IEND\xaeB`\x82"
        )

        response = client.post(
            "/v1/ocr",
            files={"file": ("test.png", io.BytesIO(png_data), "image/png")},
        )

        assert response.status_code == 500
        assert "failed" in response.json()["detail"].lower()
