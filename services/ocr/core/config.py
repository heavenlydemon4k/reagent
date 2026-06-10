"""Application settings via pydantic-settings."""

from pydantic_settings import BaseSettings


class Settings(BaseSettings):
    """OCR service configuration loaded from environment variables."""

    port: int = 8001
    host: str = "0.0.0.0"
    log_level: str = "info"
    tesseract_cmd: str = "/usr/bin/tesseract"
    max_file_size_mb: int = 10

    model_config = {"env_prefix": "OCR_"}


settings = Settings()
