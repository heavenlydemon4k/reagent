"""
OpenAI embedding client for the chunking pipeline.

Uses ``text-embedding-3-large`` with 1024 dimensions (truncated) for
optimal speed/quality trade-off.  Batching is automatically chunked
to respect OpenAI's 2048-text limit per request.
"""

from __future__ import annotations

import logging
from typing import List

from openai import AsyncOpenAI

from intelligence.core.config import get_settings

logger = logging.getLogger(__name__)


class Embedder:
    """Async OpenAI embedding wrapper."""

    # OpenAI hard limit per request
    MAX_BATCH_SIZE: int = 2048

    def __init__(
        self,
        api_key: str | None = None,
        model: str = "text-embedding-3-large",
        dimensions: int = 1024,
    ) -> None:
        if api_key is None:
            api_key = get_settings().openai_api_key
        if not api_key:
            raise ValueError(
                "OpenAI API key is required. Set OPENAI_API_KEY env var or pass api_key."
            )
        self.client = AsyncOpenAI(api_key=api_key)
        self.model = model
        self.dimensions = dimensions  # 1024 for large, 1536 for small

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def embed(self, texts: List[str]) -> List[List[float]]:
        """
        Embed a list of texts, automatically batching to stay under the
        2048-per-request ceiling.

        Args:
            texts: List of strings to embed (each ≤ 8191 tokens).

        Returns:
            List of embedding vectors (one per input text).
        """
        if not texts:
            return []

        # Deduplicate while preserving order
        unique = list(dict.fromkeys(texts))
        text_to_embedding: dict[str, List[float]] = {}

        for i in range(0, len(unique), self.MAX_BATCH_SIZE):
            batch = unique[i : i + self.MAX_BATCH_SIZE]
            logger.debug(
                "Embedding batch %d: %d texts",
                i // self.MAX_BATCH_SIZE,
                len(batch),
            )
            response = await self.client.embeddings.create(
                model=self.model,
                input=batch,
                dimensions=self.dimensions,
            )
            for item, text in zip(response.data, batch):
                text_to_embedding[text] = item.embedding

        # Return in original order
        return [text_to_embedding[t] for t in texts]

    async def embed_single(self, text: str) -> List[float]:
        """Embed a single string."""
        if not text.strip():
            # Return zero-vector for empty input so the pipeline doesn't crash
            return [0.0] * self.dimensions
        result = await self.embed([text])
        return result[0]

    # ------------------------------------------------------------------
    # Cleanup
    # ------------------------------------------------------------------

    async def close(self) -> None:
        """Close the underlying HTTP client."""
        await self.client.close()
