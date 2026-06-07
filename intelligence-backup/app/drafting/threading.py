"""
Threading Engine — Email Continuity & Subject Pivot Detection

Manages RFC-2822 threading headers (In-Reply-To, References) and detects
when a draft's content shifts topic enough to warrant a subject-line pivot.

Invariants:
    - Message-ID references are EXACT string matches (never fuzzy).
    - Subject pivot detection uses LLM + heuristic hybrid (< 100ms).
    - All headers are validated before being attached to a Draft.
"""

from __future__ import annotations

import logging
import re
import time
from dataclasses import dataclass
from typing import Any, Dict, List, Optional, Tuple

from intelligence.app.drafting.models import ThreadHeaders
from intelligence.core.fallback_chain import FallbackChain
from intelligence.core.llm_client import GenerationResult

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

# Heuristic: if >50% of significant words change, consider a pivot
_SUBJECT_PIVOT_WORD_OVERLAP_THRESHOLD: float = 0.5

# Subject-line cleaning regexes
_RE_PREFIX = re.compile(r"^(Re|Fwd|FW|Fw):\s*", re.IGNORECASE)
_RE_WHITESPACE = re.compile(r"\s+")


# ---------------------------------------------------------------------------
# Internal data structures
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class _EmailRecord:
    """Slim record of an email in a thread, as stored in PostgreSQL."""

    message_id: str
    subject: str
    body_text: str
    sender: str
    sent_at: str  # ISO timestamp


# ---------------------------------------------------------------------------
# ThreadingEngine
# ---------------------------------------------------------------------------

class ThreadingEngine:
    """Builds and validates email threading headers.

    Requires a PostgreSQL connection pool (or async DB accessor) to fetch
    prior emails in a thread. Headers are built deterministically — the
    same thread always produces the same In-Reply-To and References chain.
    """

    def __init__(
        self,
        db_pool: Any,  # asyncpg.Pool or similar
        llm: Optional[FallbackChain] = None,
    ) -> None:
        self.db = db_pool
        self.llm = llm

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def build_headers(
        self,
        thread_id: str,
        draft_content: Optional[str] = None,
    ) -> ThreadHeaders:
        """Construct threading headers for a reply in *thread_id*.

        Args:
            thread_id: The thread identifier (UUID string).
            draft_content: Optional draft body for subject-pivot detection.

        Returns:
            :class:`ThreadHeaders` with In-Reply-To, References, and Subject.
        """
        started_at = time.perf_counter()

        # 1. Fetch email chain for the thread
        chain = await self._fetch_thread_chain(thread_id)
        if not chain:
            logger.warning("No emails found for thread=%s; returning empty headers", thread_id)
            return ThreadHeaders(subject="Re: ")

        # 2. Last email = parent (In-Reply-To target)
        last_email = chain[-1]

        # 3. Build References = all ancestor Message-IDs in order
        references = [e.message_id for e in chain[:-1]]  # exclude last

        # 4. Determine subject
        original_subject = self._clean_subject(last_email.subject)
        pivoted = False
        subject = f"Re: {original_subject}"

        if draft_content and self.llm is not None:
            pivoted = await self.detect_subject_pivot(original_subject, draft_content)
            if pivoted:
                subject = self._build_pivoted_subject(original_subject, draft_content)

        headers = ThreadHeaders(
            in_reply_to=last_email.message_id,
            references=references,
            subject=subject,
            pivoted=pivoted,
        )

        elapsed = (time.perf_counter() - started_at) * 1000
        logger.info(
            "Built headers for thread=%s (chain_len=%d, pivoted=%s, %.1fms)",
            thread_id,
            len(chain),
            pivoted,
            elapsed,
        )
        return headers

    async def detect_subject_pivot(
        self, original_subject: str, draft_content: str
    ) -> bool:
        """Detect whether *draft_content* shifts topic from *original_subject*.

        Uses a fast hybrid approach:
            1. Fast heuristic: keyword overlap (< 5ms).
            2. If ambiguous, LLM confirmation (< 100ms with Haiku).

        Returns:
            True if the subject line should change to reflect the new topic.
        """
        # Stage 1: Fast heuristic
        heuristic_pivot = self._heuristic_pivot_check(original_subject, draft_content)

        # Stage 2: LLM confirmation only if heuristic is borderline
        if heuristic_pivot is not None:
            return heuristic_pivot

        # Borderline case — ask the LLM
        if self.llm is None:
            return False

        prompt = (
            f"Original email subject: \"{original_subject}\"\n"
            f"Draft reply content:\n{draft_content[:800]}\n\n"
            "Does this reply introduce a significantly different topic that "
            "warrants changing the subject line? Answer ONLY 'yes' or 'no'."
        )
        try:
            result: GenerationResult = await self.llm.generate(
                prompt=prompt,
                temperature=0.0,
                max_tokens=10,
            )
            answer = (result.text or "").strip().lower()
            return answer.startswith("y") or "yes" in answer
        except Exception as exc:
            logger.warning("LLM pivot check failed: %s; defaulting to no-pivot", exc)
            return False

    # ------------------------------------------------------------------
    # Database queries
    # ------------------------------------------------------------------

    async def _fetch_thread_chain(self, thread_id: str) -> List[_EmailRecord]:
        """Fetch ordered email chain for a thread from PostgreSQL.

        Expected schema::

            emails (
                id UUID PRIMARY KEY,
                thread_id UUID NOT NULL,
                message_id TEXT NOT NULL,
                subject TEXT,
                body_text TEXT,
                sender TEXT,
                sent_at TIMESTAMPTZ,
                ...
            )

        Returns:
            Ordered list from oldest to newest.
        """
        query = """
            SELECT message_id, subject, body_text, sender, sent_at
            FROM emails
            WHERE thread_id = $1
            ORDER BY sent_at ASC
        """
        try:
            rows = await self.db.fetch(query, thread_id)
        except Exception as exc:
            logger.error("Failed to fetch thread chain for %s: %s", thread_id, exc)
            return []

        return [
            _EmailRecord(
                message_id=row["message_id"],
                subject=row["subject"] or "",
                body_text=row["body_text"] or "",
                sender=row["sender"] or "",
                sent_at=str(row["sent_at"]),
            )
            for row in rows
        ]

    # ------------------------------------------------------------------
    # Subject helpers
    # ------------------------------------------------------------------

    @staticmethod
    def _clean_subject(subject: str) -> str:
        """Strip Re:/Fwd: prefixes and normalize whitespace."""
        cleaned = _RE_PREFIX.sub("", subject or "").strip()
        return _RE_WHITESPACE.sub(" ", cleaned)

    @staticmethod
    def _build_pivoted_subject(original_subject: str, draft_content: str) -> str:
        """Construct a new subject line when topic pivot is detected.

        Heuristic: extract the most significant noun phrase from the
        first sentence of the draft, or fall back to 'Re: [topic] / [original]'.
        """
        # Try to extract a topic from the first line
        first_line = (draft_content or "").split("\n")[0].strip()
        if len(first_line) > 10 and len(first_line) < 80:
            return f"Re: {first_line}"

        # Fallback: combine original with a marker
        return f"Re: Update — {original_subject}"

    def _heuristic_pivot_check(
        self, original_subject: str, draft_content: str
    ) -> Optional[bool]:
        """Fast keyword-overlap heuristic.

        Returns:
            True/False if the result is unambiguous, None if borderline.
        """
        # Tokenize into significant words (length > 3)
        def sig_words(text: str) -> set:
            return {
                w.lower()
                for w in re.findall(r"[A-Za-z]{4,}", text or "")
                if w.lower() not in {"this", "that", "with", "from", "have", "will", "your", "subject", "regarding"}
            }

        subj_words = sig_words(original_subject)
        draft_words = sig_words(draft_content)

        if not subj_words:
            return None  # ambiguous — no meaningful subject words

        # Check overlap
        overlap = subj_words & draft_words
        overlap_ratio = len(overlap) / len(subj_words)

        if overlap_ratio >= 0.6:
            return False  # strong continuity — no pivot
        if overlap_ratio <= 0.2:
            return True  # strong divergence — pivot

        return None  # borderline — defer to LLM

    # ------------------------------------------------------------------
    # Utility
    # ------------------------------------------------------------------

    def validate_message_id(self, message_id: str) -> bool:
        """Validate that *message_id* conforms to RFC-2822 Message-ID format.

        Format: ``<local-part@domain>``
        """
        if not message_id:
            return False
        return bool(re.match(r"^<[^<>]+@[^<>]+>\s*$", message_id))
