"""
Tests for the chunking + embedding pipeline.

Covers:
  - Chunk & ChunkBatch model validation
  - SemanticChunker paragraph splitting, merging, overlap, signature detection
  - ChunkStore payload round-trip
"""

from __future__ import annotations

import sys
import uuid
from datetime import datetime
from pathlib import Path

# Ensure the project root is on sys.path
PROJECT_ROOT = Path(__file__).resolve().parents[1]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

import pytest

from intelligence.app.compression.models import Chunk, ChunkBatch
from intelligence.app.compression.chunker import SemanticChunker
from intelligence.app.compression.store import ChunkStore

try:
    from intelligence.app.compression.embedder import Embedder
except ImportError:
    Embedder = None  # type: ignore[misc,assignment]


# ===================================================================
# Fixtures
# ===================================================================


@pytest.fixture
def chunker() -> SemanticChunker:
    return SemanticChunker(max_tokens=50, overlap_tokens=5, min_tokens=5)


@pytest.fixture
def email_ids() -> tuple[uuid.UUID, uuid.UUID, uuid.UUID]:
    return uuid.uuid4(), uuid.uuid4(), uuid.uuid4()


# ===================================================================
# Model tests
# ===================================================================


class TestChunkModel:
    def test_create_minimal(self, email_ids: tuple) -> None:
        email_id, thread_id, user_id = email_ids
        c = Chunk(
            email_id=email_id,
            thread_id=thread_id,
            user_id=user_id,
            sender_email="alice@example.com",
            content="Hello world",
            paragraph_index=0,
            timestamp=datetime.utcnow(),
        )
        assert c.content_snippet == ""
        assert c.is_signature is False
        assert c.token_count == 0

    def test_snippet_truncation(self, email_ids: tuple) -> None:
        email_id, thread_id, user_id = email_ids
        long_text = "x" * 500
        c = Chunk(
            email_id=email_id,
            thread_id=thread_id,
            user_id=user_id,
            sender_email="bob@example.com",
            content=long_text,
            content_snippet=long_text[:200],
            timestamp=datetime.utcnow(),
        )
        assert len(c.content_snippet) == 200

    def test_json_serializable(self, email_ids: tuple) -> None:
        email_id, thread_id, user_id = email_ids
        c = Chunk(
            email_id=email_id,
            thread_id=thread_id,
            user_id=user_id,
            sender_email="x@y.com",
            content="test",
            timestamp=datetime.utcnow(),
        )
        import json

        json.loads(c.model_dump_json())


class TestChunkBatch:
    def test_empty(self) -> None:
        b = ChunkBatch(chunks=[])
        assert b.is_embedded() is False

    def test_is_embedded(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        c = Chunk(
            email_id=eid,
            thread_id=tid,
            user_id=uid,
            sender_email="a@b.com",
            content="hi",
            timestamp=datetime.utcnow(),
        )
        b = ChunkBatch(chunks=[c], embeddings=[[0.1] * 1024])
        assert b.is_embedded() is True


# ===================================================================
# Chunker tests
# ===================================================================


class TestSemanticChunker:
    def test_empty_body(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        c = SemanticChunker()
        assert c.chunk_email(eid, tid, uid, "a@b.com", "", datetime.utcnow()) == []

    def test_single_small_paragraph(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        c = SemanticChunker()
        chunks = c.chunk_email(
            eid, tid, uid, "a@b.com", "Hello world.", datetime.utcnow()
        )
        assert len(chunks) == 1
        assert chunks[0].content == "Hello world."
        assert chunks[0].is_signature is False

    def test_paragraph_splitting(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        c = SemanticChunker()
        body = "First paragraph.\n\nSecond paragraph.\n\nThird paragraph."
        chunks = c.chunk_email(eid, tid, uid, "a@b.com", body, datetime.utcnow())
        assert len(chunks) >= 1
        # Content should be present in at least one chunk
        all_text = " ".join(ch.content for ch in chunks)
        assert "First" in all_text
        assert "Third" in all_text

    def test_signature_detection(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        c = SemanticChunker()
        body = "Hello, how are you?\n\nBest regards,\nAlice"
        chunks = c.chunk_email(eid, tid, uid, "a@b.com", body, datetime.utcnow())
        sig_chunks = [ch for ch in chunks if ch.is_signature]
        assert len(sig_chunks) == 1
        assert "Best regards" in sig_chunks[0].content

    def test_sent_from_my_detection(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        c = SemanticChunker()
        body = "Meeting at 3pm.\nSent from my iPhone"
        chunks = c.chunk_email(eid, tid, uid, "a@b.com", body, datetime.utcnow())
        sig_chunks = [ch for ch in chunks if ch.is_signature]
        assert len(sig_chunks) == 1

    def test_undersized_merge(self, chunker: SemanticChunker, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        # Two tiny paragraphs that should merge
        body = "Hi.\n\nBye."
        chunks = chunker.chunk_email(eid, tid, uid, "a@b.com", body, datetime.utcnow())
        # With min_tokens=5, both should merge into one chunk
        assert len(chunks) == 1
        assert "Hi." in chunks[0].content
        assert "Bye." in chunks[0].content

    def test_chunk_ordering(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        c = SemanticChunker()
        body = "Para A.\n\nPara B.\n\nPara C."
        chunks = c.chunk_email(eid, tid, uid, "a@b.com", body, datetime.utcnow())
        indices = [ch.paragraph_index for ch in chunks]
        assert indices == sorted(indices)

    def test_content_snippet_populated(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        c = SemanticChunker()
        body = "This is a moderately long paragraph that should be chunked."
        chunks = c.chunk_email(eid, tid, uid, "a@b.com", body, datetime.utcnow())
        assert all(ch.content_snippet for ch in chunks)
        assert all(len(ch.content_snippet) <= 200 for ch in chunks)

    def test_no_signature_false_positives(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        c = SemanticChunker()
        body = "In my humble opinion, the best regards are those that are sincere."
        chunks = c.chunk_email(eid, tid, uid, "a@b.com", body, datetime.utcnow())
        sig_chunks = [ch for ch in chunks if ch.is_signature]
        # Should NOT be flagged as signature
        assert len(sig_chunks) == 0


# ===================================================================
# Store round-trip tests (payload only, no live Qdrant)
# ===================================================================


class TestPayloadRoundTrip:
    def test_payload_to_chunk(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        cid = uuid.uuid4()
        now = datetime.utcnow()
        payload = {
            "chunk_id": str(cid),
            "email_id": str(eid),
            "thread_id": str(tid),
            "user_id": str(uid),
            "sender_email": "test@example.com",
            "content": "Hello world",
            "content_snippet": "Hello world",
            "paragraph_index": 2,
            "is_signature": False,
            "timestamp": int(now.timestamp()),
        }
        chunk = ChunkStore._payload_to_chunk(payload)
        assert chunk.chunk_id == cid
        assert chunk.paragraph_index == 2
        assert chunk.is_signature is False

    def test_payload_iso_timestamp(self, email_ids: tuple) -> None:
        eid, tid, uid = email_ids
        cid = uuid.uuid4()
        now = datetime.utcnow()
        payload = {
            "chunk_id": str(cid),
            "email_id": str(eid),
            "thread_id": str(tid),
            "user_id": str(uid),
            "sender_email": "t@t.com",
            "content": "x",
            "content_snippet": "x",
            "paragraph_index": 0,
            "is_signature": False,
            "timestamp": now.isoformat(),
        }
        chunk = ChunkStore._payload_to_chunk(payload)
        assert isinstance(chunk.timestamp, datetime)


# ===================================================================
# Integration smoke test
# ===================================================================


class TestPipelineSmoke:
    def test_end_to_end_no_crash(self, email_ids: tuple) -> None:
        """Verify the full pipeline can be instantiated and called."""
        eid, tid, uid = email_ids
        chunker = SemanticChunker()
        body = (
            "Hello team,\n\n"
            "I wanted to follow up on the proposal we discussed last week. "
            "The numbers look promising and I think we should move forward.\n\n"
            "Please let me know your thoughts by end of week.\n\n"
            "Best regards,\nAlice"
        )
        chunks = chunker.chunk_email(
            eid, tid, uid, "alice@example.com", body, datetime.utcnow()
        )
        assert len(chunks) >= 2  # body + signature
        sigs = [c for c in chunks if c.is_signature]
        assert len(sigs) == 1
        non_sigs = [c for c in chunks if not c.is_signature]
        assert len(non_sigs) >= 1
        assert "follow up" in " ".join(c.content for c in non_sigs)
