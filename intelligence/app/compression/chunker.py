"""
Semantic chunking engine for email bodies.

Pipeline:
    1. Strip / detect signature blocks (regex heuristics).
    2. Split at paragraph boundaries (\n\n).
    3. Merge undersized paragraphs (< min_tokens) forward.
    4. Split oversized paragraphs at sentence boundaries.
    5. Apply token overlap between consecutive chunks.
    6. Package into Chunk models.

Token counting uses tiktoken (cl100k_base) when available; otherwise falls
back to a fast whitespace approximation.
"""

from __future__ import annotations

import logging
import re
from datetime import datetime
from typing import Callable, List, Optional, Tuple
from uuid import UUID

from intelligence.app.compression.models import Chunk

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Signature heuristics (compiled once)
# ---------------------------------------------------------------------------

_SIGNATURE_RE = re.compile(
    r"(?:"
    # Common closings followed by a name block
    r"(?:(?:Best\s+(?:regards|wishes)|Kind\s+regards|Regards|Cheers|Sincerely|"
    r"Thanks(?:\s+again)?|Thank\s+(?:you|u)|Warm\s+regards|Yours\s+(?:truly|sincerely)|"
    r"All\s+the\s+best|Take\s+care|Talk\s+soon|See\s+you|Sent\s+from\s+my.*)"
    r"[,.]?\s*\n+[\s\S]{0,200})"
    r"|"
    # "Sent from my iPhone / Android / mobile device"
    r"(?:^Sent\s+from\s+my\s+\S+.*$)"
    r"|"
    # Horizontal rule followed by short name block
    r"(?:^[-–—=]{3,}\s*\n+[A-Z][a-z]+(?:\s+[A-Z][a-z]+){0,3}\s*$)"
    r")",
    re.MULTILINE | re.IGNORECASE,
)

_NAME_DOMAIN_RE = re.compile(
    r"^\s*--\s*\n+\s*[A-Z][a-zA-Z\s]+\n+.*@.*$", re.MULTILINE
)

# ---------------------------------------------------------------------------
# Token counter
# ---------------------------------------------------------------------------

def _make_token_counter() -> Callable[[str], int]:
    """Return a function that counts tokens in a string."""
    try:
        import tiktoken  # type: ignore[import-untyped]

        enc = tiktoken.get_encoding("cl100k_base")
        return lambda text: len(enc.encode(text, disallowed_special=()))
    except Exception:
        logger.debug("tiktoken not available; using whitespace token approximation")
        return lambda text: max(1, int(len(text.split()) * 1.3))


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


class SemanticChunker:
    """Splits email text into semantic chunks for embedding."""

    # Sentence boundary pattern (period, exclamation, question followed by space or newline)
    _SENT_RE = re.compile(r'(?<=[.!?])(?:\s+|\n+)')

    def __init__(
        self,
        max_tokens: int = 800,
        overlap_tokens: int = 100,
        min_tokens: int = 50,
    ) -> None:
        self.max_tokens = max_tokens
        self.overlap = overlap_tokens
        self.min_size = min_tokens
        self._count_tokens: Callable[[str], int] = _make_token_counter()

    # ------------------------------------------------------------------
    # Main entry point
    # ------------------------------------------------------------------

    def chunk_email(
        self,
        email_id: UUID,
        thread_id: UUID,
        user_id: UUID,
        sender_email: str,
        body_text: str,
        received_at: datetime,
    ) -> List[Chunk]:
        """
        Convert an email body into an ordered list of Chunk objects.

        Steps:
            1. Detect and flag signature region.
            2. Split into paragraphs; merge small ones.
            3. Split oversized paragraphs at sentence boundaries.
            4. Emit chunks with overlap.
        """
        if not body_text or not body_text.strip():
            return []

        # 1. Signature detection
        body_without_sig, signature_text = self._extract_signature(body_text)

        # 2. Paragraph splitting + merge undersized
        paragraphs = self._paragraphs(body_without_sig)
        paragraphs = self._merge_undersized(paragraphs)

        # 3. Sentence-level splitting for oversized paragraphs
        pieces: List[Tuple[str, bool]] = []  # (text, is_signature)
        for para in paragraphs:
            if self._token_count(para) > self.max_tokens:
                pieces.extend((s, False) for s in self._split_sentences(para))
            else:
                pieces.append((para, False))

        # Add signature as its own piece if present
        if signature_text:
            pieces.append((signature_text, True))

        # 4. Build final chunks with overlap
        return self._build_chunks(
            pieces=pieces,
            email_id=email_id,
            thread_id=thread_id,
            user_id=user_id,
            sender_email=sender_email,
            received_at=received_at,
        )

    # ------------------------------------------------------------------
    # Signature detection
    # ------------------------------------------------------------------

    @classmethod
    def _extract_signature(cls, text: str) -> Tuple[str, Optional[str]]:
        """
        Strip the signature block from the email body.

        Returns:
            (body_without_signature, signature_or_None)
        """
        for pattern in (_SIGNATURE_RE, _NAME_DOMAIN_RE):
            match = pattern.search(text)
            if match:
                start = match.start()
                body = text[:start].rstrip()
                sig = text[start:].strip()
                return body, sig if sig else None
        return text, None

    # ------------------------------------------------------------------
    # Paragraph helpers
    # ------------------------------------------------------------------

    def _paragraphs(self, text: str) -> List[str]:
        """Split text at blank lines, preserving order."""
        raw = [p.strip() for p in text.split("\n\n")]
        return [p for p in raw if p]

    def _merge_undersized(self, paragraphs: List[str]) -> List[str]:
        """
        Forward-merge paragraphs that are below ``min_size`` tokens.

        If the last paragraph is undersized and there is a predecessor,
        it is appended to that predecessor.
        """
        if not paragraphs:
            return paragraphs

        merged: List[str] = []
        buffer = paragraphs[0]

        for para in paragraphs[1:]:
            if self._token_count(buffer) < self.min_size:
                buffer = f"{buffer}\n\n{para}"
            else:
                merged.append(buffer)
                buffer = para

        # Handle trailing buffer
        if merged and self._token_count(buffer) < self.min_size:
            merged[-1] = f"{merged[-1]}\n\n{buffer}"
        else:
            merged.append(buffer)

        return merged

    # ------------------------------------------------------------------
    # Sentence splitting
    # ------------------------------------------------------------------

    def _split_sentences(self, text: str) -> List[str]:
        """
        Split *text* into sentence-sized fragments that each fit within
        ``max_tokens``.

        If a single sentence still exceeds ``max_tokens``, it is hard-split
        at whitespace boundaries.
        """
        sentences = [s.strip() for s in self._SENT_RE.split(text) if s.strip()]
        result: List[str] = []
        buffer = ""

        for sent in sentences:
            candidate = f"{buffer} {sent}".strip() if buffer else sent
            if self._token_count(candidate) > self.max_tokens:
                if buffer:
                    result.append(buffer)
                # If sentence alone is too long, hard-split it
                if self._token_count(sent) > self.max_tokens:
                    result.extend(self._hard_split(sent))
                    buffer = ""
                else:
                    buffer = sent
            else:
                buffer = candidate

        if buffer:
            result.append(buffer)

        return result

    def _hard_split(self, text: str) -> List[str]:
        """Force-split text at word boundaries to respect ``max_tokens``."""
        words = text.split()
        chunks: List[str] = []
        buffer: List[str] = []

        for w in words:
            buffer.append(w)
            if self._token_count(" ".join(buffer)) >= self.max_tokens:
                chunks.append(" ".join(buffer))
                buffer = []

        if buffer:
            tail = " ".join(buffer)
            if chunks and self._token_count(tail) < self.min_size:
                # Merge tiny tail back into last chunk if it fits
                combined = f"{chunks[-1]} {tail}"
                if self._token_count(combined) <= self.max_tokens:
                    chunks[-1] = combined
                else:
                    chunks.append(tail)
            else:
                chunks.append(tail)

        return chunks

    # ------------------------------------------------------------------
    # Chunk assembly with overlap
    # ------------------------------------------------------------------

    def _build_chunks(
        self,
        pieces: List[Tuple[str, bool]],
        email_id: UUID,
        thread_id: UUID,
        user_id: UUID,
        sender_email: str,
        received_at: datetime,
    ) -> List[Chunk]:
        """
        Build final Chunk list from (text, is_signature) pieces, applying
        overlap between consecutive non-signature chunks.
        """
        chunks: List[Chunk] = []
        paragraph_index = 0
        overlap_text = ""

        for text, is_sig in pieces:
            if is_sig:
                # Signatures are standalone chunks, no overlap
                chunks.append(
                    self._make_chunk(
                        content=text,
                        snippet=text[:200],
                        email_id=email_id,
                        thread_id=thread_id,
                        user_id=user_id,
                        sender_email=sender_email,
                        received_at=received_at,
                        paragraph_index=paragraph_index,
                        is_signature=True,
                    )
                )
                paragraph_index += 1
                overlap_text = ""
                continue

            # Prepend overlap from previous chunk
            if overlap_text:
                content = f"{overlap_text}\n\n{text}".strip()
            else:
                content = text

            # If still too long, hard-split (safety valve)
            if self._token_count(content) > self.max_tokens:
                sub_pieces = self._hard_split(content)
                for sub in sub_pieces:
                    chunks.append(
                        self._make_chunk(
                            content=sub,
                            snippet=sub[:200],
                            email_id=email_id,
                            thread_id=thread_id,
                            user_id=user_id,
                            sender_email=sender_email,
                            received_at=received_at,
                            paragraph_index=paragraph_index,
                            is_signature=False,
                        )
                    )
                    paragraph_index += 1
                    overlap_text = self._compute_overlap(sub)
                continue

            chunks.append(
                self._make_chunk(
                    content=content,
                    snippet=content[:200],
                    email_id=email_id,
                    thread_id=thread_id,
                    user_id=user_id,
                    sender_email=sender_email,
                    received_at=received_at,
                    paragraph_index=paragraph_index,
                    is_signature=False,
                )
            )
            paragraph_index += 1
            overlap_text = self._compute_overlap(content)

        return chunks

    def _make_chunk(
        self,
        content: str,
        snippet: str,
        email_id: UUID,
        thread_id: UUID,
        user_id: UUID,
        sender_email: str,
        received_at: datetime,
        paragraph_index: int,
        is_signature: bool,
    ) -> Chunk:
        """Factory for a single Chunk with all fields populated."""
        return Chunk(
            email_id=email_id,
            thread_id=thread_id,
            user_id=user_id,
            sender_email=sender_email,
            content=content,
            content_snippet=snippet[:200],
            paragraph_index=paragraph_index,
            is_signature=is_signature,
            token_count=self._token_count(content),
            timestamp=received_at,
        )

    def _compute_overlap(self, text: str) -> str:
        """
        Return the trailing ``overlap`` tokens of *text* to be prepended to
        the next chunk.
        """
        if self.overlap <= 0:
            return ""
        tokens = text.split()
        # Approximate: ~1.3 tokens per word; overlap words needed
        word_overlap = int(self.overlap / 1.3)
        overlap_words = tokens[-word_overlap:] if len(tokens) > word_overlap else tokens
        return " ".join(overlap_words)

    # ------------------------------------------------------------------
    # Token counting
    # ------------------------------------------------------------------

    def _token_count(self, text: str) -> int:
        """Return estimated token count for *text*."""
        if not text:
            return 0
        return self._count_tokens(text)
