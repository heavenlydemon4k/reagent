"""CitationVerifier — zero-tolerance citation verification.

Hallucinated citations are treated as system failures. Every claim in a
decision card MUST be grounded in an actual chunk stored in Qdrant.

Algorithm:
1. Existence check: chunk_id must exist in Qdrant for (thread_id, user_id).
2. Verbatim check: citation["verbatim"] must fuzzy-match the chunk text
   using Levenshtein distance < 10% of verbatim length.
3. Any failure → FAILED with full diagnostics.
"""
from __future__ import annotations

import logging
from typing import Any

from intelligence.app.compression.models import VerificationResult
from intelligence.app.compression.store import ChunkStore

logger = logging.getLogger(__name__)


class CitationVerifier:
    """Zero-tolerance citation verification. Hallucinated citations = system failure."""

    # Threshold: Levenshtein distance must be < 10% of verbatim length
    _FUZZY_THRESHOLD_RATIO: float = 0.10

    def __init__(self, chunk_store: ChunkStore) -> None:
        self.chunks = chunk_store

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def verify(
        self,
        citations: list[dict[str, Any]],
        thread_id: str,
        user_id: str,
    ) -> VerificationResult:
        """Verify every citation against Qdrant-stored chunks.

        Args:
            citations: Raw citation dicts from LLM JSON output.
                Each must have ``chunk_id`` and ``verbatim`` keys.
            thread_id: Scope verification to this thread.
            user_id: Scope verification to this user.

        Returns:
            VerificationResult: passed=True only if ALL citations validate.
        """
        if not citations:
            logger.warning("No citations to verify — treating as failure")
            return VerificationResult(
                passed=False,
                failed_citations=[
                    {"reason": "No citations provided — every claim must cite evidence"}
                ],
                total_checked=0,
                pass_count=0,
            )

        failed: list[dict[str, Any]] = []
        passed_count = 0

        for idx, citation in enumerate(citations):
            chunk_id = citation.get("chunk_id", "<missing>")
            verbatim = citation.get("verbatim", "<missing>")
            claim = citation.get("claim", "<unspecified>")

            # ---- 1. Existence check ----
            exists = await self._chunk_exists(chunk_id, thread_id, user_id)
            if not exists:
                failed.append({
                    "index": idx,
                    "chunk_id": chunk_id,
                    "claim": claim,
                    "reason": f"chunk_id '{chunk_id}' not found in Qdrant for thread={thread_id} user={user_id}",
                })
                logger.error("Citation %d FAILED existence: chunk_id=%s", idx, chunk_id)
                continue

            # ---- 2. Verbatim fuzzy match ----
            chunk = await self.chunks.get_chunk_by_id(chunk_id, thread_id, user_id)
            assert chunk is not None  # guarded by _chunk_exists
            verbatim_match = await self._verbatim_matches(verbatim, chunk.text)

            if not verbatim_match:
                failed.append({
                    "index": idx,
                    "chunk_id": chunk_id,
                    "verbatim": verbatim,
                    "chunk_text_preview": chunk.text[:200],
                    "claim": claim,
                    "reason": (
                        f"verbatim snippet does not fuzzy-match chunk text "
                        f"(Levenshtein ratio >= {self._FUZZY_THRESHOLD_RATIO:.0%})"
                    ),
                })
                logger.error(
                    "Citation %d FAILED verbatim match: chunk_id=%s verbatim='%s...'",
                    idx,
                    chunk_id,
                    verbatim[:60],
                )
                continue

            # ---- Passed ----
            passed_count += 1

        total = len(citations)
        all_passed = len(failed) == 0

        if all_passed:
            logger.info("All %d citations PASSED verification", total)
        else:
            logger.warning(
                "Citation verification FAILED: %d/%d passed, %d failed",
                passed_count,
                total,
                len(failed),
            )

        return VerificationResult(
            passed=all_passed,
            failed_citations=failed,
            total_checked=total,
            pass_count=passed_count,
        )

    # ------------------------------------------------------------------
    # Internal checks
    # ------------------------------------------------------------------

    async def _chunk_exists(
        self,
        chunk_id: str,
        thread_id: str,
        user_id: str,
    ) -> bool:
        """Check that a chunk_id exists in Qdrant for the given thread and user.

        Returns True iff exactly one point matches all three dimensions.
        """
        return await self.chunks.chunk_exists(chunk_id, thread_id, user_id)

    async def _verbatim_matches(
        self,
        verbatim: str,
        chunk_text: str,
    ) -> bool:
        """Fuzzy-match a citation's verbatim snippet against the actual chunk text.

        Uses normalized Levenshtein distance: distance / len(verbatim) must be
        strictly less than ``_FUZZY_THRESHOLD_RATIO`` (10%).

        The verbatim snippet is located as a substring within the chunk text
        using a sliding-window approach, and the best match is retained.
        """
        if not verbatim or not chunk_text:
            return False

        # Short-circuit: exact containment
        if verbatim in chunk_text:
            return True

        # Sliding-window fuzzy match: scan chunk_text for the best window
        v_len = len(verbatim)
        best_distance = self._levenshtein(verbatim, chunk_text)

        # Only slide if chunk is longer than verbatim
        if len(chunk_text) > v_len:
            for start in range(len(chunk_text) - v_len + 1):
                window = chunk_text[start : start + v_len]
                dist = self._levenshtein(verbatim, window)
                if dist < best_distance:
                    best_distance = dist
                    # Early exit if we find an exact match
                    if best_distance == 0:
                        return True

        ratio = best_distance / v_len if v_len > 0 else 1.0
        return ratio < self._FUZZY_THRESHOLD_RATIO

    # ------------------------------------------------------------------
    # Levenshtein (Wagner-Fischer with space optimization)
    # ------------------------------------------------------------------

    @staticmethod
    def _levenshtein(a: str, b: str) -> int:
        """Compute Levenshtein distance between two strings."""
        if len(a) < len(b):
            return CitationVerifier._levenshtein(b, a)
        if len(b) == 0:
            return len(a)

        # Use two rows instead of full matrix
        previous_row = list(range(len(b) + 1))
        current_row = [0] * (len(b) + 1)

        for i, char_a in enumerate(a):
            current_row[0] = i + 1

            for j, char_b in enumerate(b):
                insertions = previous_row[j + 1] + 1
                deletions = current_row[j] + 1
                substitutions = previous_row[j] + (0 if char_a == char_b else 1)
                current_row[j + 1] = min(insertions, deletions, substitutions)

            previous_row, current_row = current_row, previous_row

        return previous_row[len(b)]
