"""
Compression bounded context.

Handles the compression pipeline: consumes raw_email_ids, chunks and embeds
them, builds the relationship graph, and produces a decision card.

Public exports:
    Chunk           – semantic slice of an email
    ChunkBatch      – container for atomic upsert
    SemanticChunker – splits email bodies into chunks
    Embedder        – OpenAI embedding client
    ChunkStore      – Qdrant persistence layer
"""

from intelligence.app.compression.models import Chunk, ChunkBatch
from intelligence.app.compression.chunker import SemanticChunker
from intelligence.app.compression.embedder import Embedder
from intelligence.app.compression.store import ChunkStore

__all__ = [
    "Chunk",
    "ChunkBatch",
    "SemanticChunker",
    "Embedder",
    "ChunkStore",
]
