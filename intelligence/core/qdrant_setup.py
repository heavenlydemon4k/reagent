"""
Qdrant Setup Module for Decision Stack Intelligence Layer.

Handles creation, indexing, health checks, and deletion of all vector
collections used by the system. All operations are idempotent.
"""

import logging
import os
from typing import Dict, List, Optional

from qdrant_client import QdrantClient
from qdrant_client.http.models import (
    CollectionDescription,
    CollectionStatus,
    Distance,
    FieldCondition,
    Filter,
    MatchValue,
    models,
)

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Collection Configuration Constants
# ---------------------------------------------------------------------------

# All collections use 1024 dimensions to match OpenAI text-embedding-3-large
VECTOR_SIZE: int = 1024
VECTOR_DISTANCE: Distance = Distance.COSINE
ON_DISK: bool = True

# Shared payload index fields across collections
SHARED_KEYWORD_FIELDS: List[str] = [
    "user_id",
    "chunk_id",
    "thread_id",
    "email_id",
    "sender_email",
]

# Collection-specific configurations
COLLECTION_CONFIGS: Dict[str, Dict] = {
    "email_chunks": {
        "vector_size": VECTOR_SIZE,
        "distance": VECTOR_DISTANCE,
        "on_disk": ON_DISK,
        "payload_fields": {
            "user_id": "keyword",
            "chunk_id": "keyword",
            "thread_id": "keyword",
            "email_id": "keyword",
            "sender_email": "keyword",
            "is_signature": "bool",
            "timestamp": "integer",
        },
    },
    "voice_examples": {
        "vector_size": VECTOR_SIZE,
        "distance": VECTOR_DISTANCE,
        "on_disk": ON_DISK,
        "payload_fields": {
            "user_id": "keyword",
            "example_id": "keyword",
            "sender_email": "keyword",
            "sent_at": "integer",
            "tone_tags": "keyword",
        },
    },
    "consultation_index": {
        "vector_size": VECTOR_SIZE,
        "distance": VECTOR_DISTANCE,
        "on_disk": ON_DISK,
        "payload_fields": {
            "user_id": "keyword",
            "chunk_id": "keyword",
            "thread_id": "keyword",
            "email_id": "keyword",
            "sender_email": "keyword",
            "is_signature": "bool",
            "timestamp": "integer",
            "thread_summary": "bool",
        },
    },
}

COLLECTION_NAMES: List[str] = list(COLLECTION_CONFIGS.keys())


class QdrantSetup:
    """Manages Qdrant collection lifecycle for the Intelligence Layer."""

    def __init__(
        self,
        client: Optional[QdrantClient] = None,
        url: Optional[str] = None,
        api_key: Optional[str] = None,
    ) -> None:
        """
        Initialize QdrantSetup.

        Args:
            client: An existing QdrantClient instance (used in tests).
            url: Qdrant server URL (overrides env var).
            api_key: Qdrant API key (overrides env var).
        """
        if client is not None:
            self.client = client
        else:
            self.url = url or os.environ.get("QDRANT_URL", "http://localhost:6333")
            self.api_key = api_key or os.environ.get("QDRANT_API_KEY")
            self.client = QdrantClient(url=self.url, api_key=self.api_key)

    # ------------------------------------------------------------------
    # Collection Lifecycle
    # ------------------------------------------------------------------

    def create_collections(self) -> Dict[str, bool]:
        """
        Create all three collections if they do not already exist.

        Returns:
            Dict mapping collection name -> True if created/exists.
        """
        results: Dict[str, bool] = {}
        existing = self._existing_collections()

        for name, cfg in COLLECTION_CONFIGS.items():
            if name in existing:
                logger.info("Collection '%s' already exists — skipping creation.", name)
                results[name] = True
                continue

            self.client.create_collection(
                collection_name=name,
                vectors_config=models.VectorParams(
                    size=cfg["vector_size"],
                    distance=cfg["distance"],
                    on_disk=cfg["on_disk"],
                ),
            )
            logger.info("Collection '%s' created.", name)
            results[name] = True

        return results

    def delete_collections(self) -> Dict[str, bool]:
        """
        Delete all collections. Useful for testing/reset.

        Returns:
            Dict mapping collection name -> True if deleted/did not exist.
        """
        results: Dict[str, bool] = {}
        for name in COLLECTION_NAMES:
            try:
                self.client.delete_collection(collection_name=name)
                logger.info("Collection '%s' deleted.", name)
                results[name] = True
            except Exception as exc:
                # If collection doesn't exist, treat as success
                logger.warning("Collection '%s' deletion note: %s", name, exc)
                results[name] = True

        return results

    # ------------------------------------------------------------------
    # Payload Indexes
    # ------------------------------------------------------------------

    def create_payload_indexes(self) -> Dict[str, Dict[str, bool]]:
        """
        Create keyword (and other typed) payload indexes for all collections.
        Indexes are created only if they do not already exist (idempotent).

        Returns:
            Nested dict: {collection_name: {field_name: success_bool}}
        """
        results: Dict[str, Dict[str, bool]] = {}

        for name, cfg in COLLECTION_CONFIGS.items():
            results[name] = {}
            for field_name, field_type in cfg["payload_fields"].items():
                try:
                    index_params = self._payload_index_params(field_type)
                    self.client.create_payload_index(
                        collection_name=name,
                        field_name=field_name,
                        field_schema=index_params,
                    )
                    logger.info(
                        "Index '%s.%s' (%s) created.", name, field_name, field_type
                    )
                    results[name][field_name] = True
                except Exception as exc:
                    # Qdrant raises if index already exists — check message
                    if "already exists" in str(exc).lower():
                        logger.debug(
                            "Index '%s.%s' already exists — skipping.", name, field_name
                        )
                        results[name][field_name] = True
                    else:
                        logger.error(
                            "Failed to create index '%s.%s': %s", name, field_name, exc
                        )
                        results[name][field_name] = False

        return results

    # ------------------------------------------------------------------
    # Health Check
    # ------------------------------------------------------------------

    def health_check(self) -> Dict[str, Dict]:
        """
        Return status information for all collections.

        Returns:
            Dict mapping collection name -> {
                "exists": bool,
                "status": str | None,  # e.g. "green"
                "vectors_count": int,
                "indexed_vectors_count": int,
                "points_count": int,
            }
        """
        report: Dict[str, Dict] = {}
        existing = self._existing_collections()

        for name in COLLECTION_NAMES:
            if name not in existing:
                report[name] = {
                    "exists": False,
                    "status": None,
                    "vectors_count": 0,
                    "indexed_vectors_count": 0,
                    "points_count": 0,
                }
                continue

            info = self.client.get_collection(collection_name=name)
            report[name] = {
                "exists": True,
                "status": info.status.value if info.status else None,
                "vectors_count": info.vectors_count or 0,
                "indexed_vectors_count": info.indexed_vectors_count or 0,
                "points_count": info.points_count or 0,
            }

        return report

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _existing_collections(self) -> set:
        """Return a set of collection names that already exist."""
        collections: List[CollectionDescription] = self.client.get_collections().collections
        return {c.name for c in collections}

    @staticmethod
    def _payload_index_params(field_type: str):
        """Map a simple type string to Qdrant payload index params."""
        # Use raw type strings for broad compatibility across qdrant-client versions.
        # On newer servers these resolve to the same indexes as the explicit param
        # objects but avoid Pydantic validation issues.
        mapping = {
            "keyword": models.KeywordIndexParams(
                type=models.KeywordIndexType.KEYWORD,
            ),
            "integer": models.IntegerIndexParams(
                type=models.IntegerIndexType.INTEGER,
                lookup=True,
                range=True,
            ),
            "bool": models.BoolIndexParams(
                type=models.BoolIndexType.BOOL,
            ),
        }
        if field_type not in mapping:
            raise ValueError(f"Unsupported payload field type: {field_type}")
        return mapping[field_type]

    def close(self) -> None:
        """Close the underlying Qdrant client connection."""
        self.client.close()
