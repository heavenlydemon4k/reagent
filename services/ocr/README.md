# Decision Stack OCR Service

A standalone Python/FastAPI microservice that receives images and PDFs, extracts text, and returns confidence scores. Built for the Decision Stack ingestion mesh.

## Features

- **Image OCR**: Extracts text from PNG, JPG, TIFF, BMP, GIF, and WebP using Tesseract OCR
- **PDF Processing**: Smart extraction that prefers existing text layers and falls back to OCR for scanned documents
- **Confidence Scoring**: Per-word confidence with weighted averaging; results below 0.7 are flagged for review
- **Health Checks**: Tesseract availability and service status
- **Structured Logging**: JSON-structured logs via structlog
- **Production Ready**: Multi-stage Docker build, non-root user, configurable via environment variables

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | `/v1/ocr` | Upload an image or PDF for text extraction |
| GET | `/v1/health` | Service health check and tesseract version |
| GET | `/docs` | Auto-generated Swagger UI |
| GET | `/redoc` | Auto-generated ReDoc UI |

## Quick Start

### Prerequisites

- Python 3.11+
- Tesseract OCR (`apt-get install tesseract-ocr`)
- Poppler (`apt-get install poppler-utils` for PDF support)

### Local Development

```bash
# Create virtual environment
python -m venv .venv
source .venv/bin/activate

# Install dependencies
pip install -r requirements.txt

# Run the service
uvicorn app.main:app --reload --port 8081
```

### Docker

```bash
# Build image
docker build -t decision-stack-ocr .

# Run container
docker run -p 8081:8081 decision-stack-ocr
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OCR_PORT` | 8081 | Service port |
| `OCR_HOST` | 0.0.0.0 | Bind host |
| `OCR_LOG_LEVEL` | info | Logging level |
| `OCR_TESSERACT_CMD` | /usr/bin/tesseract | Tesseract binary path |
| `OCR_MAX_FILE_SIZE_MB` | 10 | Maximum upload file size (MB) |

## API Usage

### Extract text from an image

```bash
curl -X POST "http://localhost:8081/v1/ocr" \
  -F "file=@document.png" \
  -F "file_type=image"
```

### Extract text from a PDF

```bash
curl -X POST "http://localhost:8081/v1/ocr" \
  -F "file=@document.pdf" \
  -F "file_type=pdf"
```

### Auto-detect file type

```bash
curl -X POST "http://localhost:8081/v1/ocr" \
  -F "file=@document.png" \
  -F "file_type=auto"
```

### Response format

```json
{
  "text": "Extracted text content...",
  "confidence": 0.9234,
  "word_count": 42,
  "page_count": 1,
  "flagged_for_review": false,
  "metadata": {
    "filename": "document.png",
    "image_size": [1200, 800],
    "image_mode": "RGB",
    "words_detected": 45,
    "high_confidence_words": 42
  }
}
```

## Project Structure

```
services/ocr/
  app/
    __init__.py
    main.py              # FastAPI application entry point
    router.py            # API routes (/ocr, /health)
    models.py            # Pydantic request/response schemas
    engine.py            # OCR engine orchestrator
    pdf.py               # PDF handling (text layer vs scanned)
    image.py             # Image OCR (pytesseract)
    health.py            # Health check logic
  core/
    __init__.py
    config.py            # Settings via pydantic-settings
    logging_config.py    # Structured JSON logging
  tests/
    __init__.py
    test_main.py         # API endpoint tests
    test_engine.py       # OCR engine tests (mocked)
  Dockerfile
  requirements.txt
  pyproject.toml
  README.md
```

## Running Tests

```bash
pytest tests/ -v
```

## Invariants

- Confidence < 0.7: flagged for review but still returned
- PDFs: prefer text layer extraction, fallback to OCR for scanned documents
- Max file size: 10MB default (configurable via `OCR_MAX_FILE_SIZE_MB`)
- Async: all endpoints are async
- Docker: runs as non-root user
