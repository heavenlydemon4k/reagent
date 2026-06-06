"""PDF handling with text layer extraction and OCR fallback."""

import io

import pdfplumber
from pdf2image import convert_from_bytes
from PIL import Image


class PDFHandler:
    """Handles PDF text extraction with fallback to OCR for scanned documents."""

    TEXT_DENSITY_THRESHOLD = 0.10  # Min ratio of pages with text to use text layer
    MIN_CHARS_PER_PAGE = 20  # Minimum characters to consider a page as having text

    def has_text_layer(self, pdf_bytes: bytes) -> bool:
        """Check if the PDF has a meaningful extractable text layer.

        Args:
            pdf_bytes: Raw PDF file bytes.

        Returns:
            True if more than TEXT_DENSITY_THRESHOLD ratio of pages have text.
        """
        text_pages = 0
        total_pages = 0

        with pdfplumber.open(io.BytesIO(pdf_bytes)) as pdf:
            total_pages = len(pdf.pages)
            for page in pdf.pages:
                text = page.extract_text() or ""
                if len(text.strip()) >= self.MIN_CHARS_PER_PAGE:
                    text_pages += 1

        if total_pages == 0:
            return False

        return (text_pages / total_pages) >= self.TEXT_DENSITY_THRESHOLD

    def extract_text_layer(self, pdf_bytes: bytes) -> tuple[str, int]:
        """Extract text from the PDF's text layer using pdfplumber.

        Args:
            pdf_bytes: Raw PDF file bytes.

        Returns:
            Tuple of (concatenated text from all pages, page count).
        """
        texts = []
        page_count = 0

        with pdfplumber.open(io.BytesIO(pdf_bytes)) as pdf:
            page_count = len(pdf.pages)
            for page in pdf.pages:
                page_text = page.extract_text() or ""
                texts.append(page_text.strip())

        return "\n\n".join(t for t in texts if t), page_count

    async def convert_to_images(self, pdf_bytes: bytes) -> list[bytes]:
        """Convert PDF pages to PNG images.

        Args:
            pdf_bytes: Raw PDF file bytes.

        Returns:
            List of PNG image bytes, one per page.
        """
        images = convert_from_bytes(
            pdf_bytes,
            dpi=200,
            fmt="png",
        )

        image_bytes_list = []
        for img in images:
            buf = io.BytesIO()
            img.save(buf, format="PNG")
            image_bytes_list.append(buf.getvalue())

        return image_bytes_list

    def get_page_count(self, pdf_bytes: bytes) -> int:
        """Get the number of pages in a PDF.

        Args:
            pdf_bytes: Raw PDF file bytes.

        Returns:
            Number of pages.
        """
        with pdfplumber.open(io.BytesIO(pdf_bytes)) as pdf:
            return len(pdf.pages)
