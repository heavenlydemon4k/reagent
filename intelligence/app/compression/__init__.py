"""
Compression bounded context.

Handles the compression pipeline: consumes raw_email_ids, chunks and embeds
them, builds the relationship graph, and produces a decision card.

Public exports:
    Chunk                   -- semantic slice of an email
    ChunkBatch              -- container for atomic upsert
    ThreadSummary           -- hierarchical map-reduce summary
    SummaryCacheEntry       -- Qdrant payload for cached summaries
    SemanticChunker         -- splits email bodies into chunks
    Embedder                -- OpenAI embedding client (requires openai package)
    ChunkStore              -- Qdrant persistence layer (requires qdrant-client package)
    SummaryCache            -- Qdrant cache for thread summaries
    HierarchicalSummarizer  -- map-reduce summarization for long threads
"""

from intelligence.app.compression.models import Chunk, ChunkBatch, ThreadSummary, SummaryCacheEntry
from intelligence.app.compression.chunker import SemanticChunker
from intelligence.app.compression.store import ChunkStore

__all__ = [
    "Chunk",
    "ChunkBatch",
    "ThreadSummary",
    "SummaryCacheEntry",
    "SemanticChunker",
    "ChunkStore",
]

# Optional exports -- only available when dependencies are installed
try:
    from intelligence.app.compression.summary_cache import SummaryCache
    __all__.append("SummaryCache")
except ImportError:
    pass

try:
    from intelligence.app.compression.hierarchical import HierarchicalSummarizer
    __all__.append("HierarchicalSummarizer")
except ImportError:
    pass
