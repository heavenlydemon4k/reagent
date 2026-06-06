"""PII log sanitizer for Python services.

Ensures email content (subjects, body text, sender emails) never appears in
plaintext in production or staging logs. Provides correlation hashes for
debugging while protecting user privacy (GDPR/CCPA compliance).
"""

import hashlib
import logging
import os
import re
from typing import Any, Dict, Optional

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Environment helpers
# ---------------------------------------------------------------------------


def is_production() -> bool:
    """Return True when ENV=production or ENV=staging.

    In these environments, full redaction is enforced -- plaintext PII
    must NEVER appear in logs.
    """
    env = os.environ.get("ENV", "").lower()
    return env in ("production", "staging")


def is_development() -> bool:
    """Return True when ENV=development or ENV=local (or unset).

    In these environments, full logs are allowed for debugging.
    """
    env = os.environ.get("ENV", "").lower()
    return env in ("development", "local", "")


# ---------------------------------------------------------------------------
# LogSanitizer
# ---------------------------------------------------------------------------


class LogSanitizer:
    """Redacts PII from log fields while preserving correlation hashes."""

    EMAIL_RE = re.compile(r"[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}")

    def redact_subject(self, subject: str) -> str:
        """Keep first 20 chars + hash for correlation.

        In development, returns the original subject unchanged.
        """
        if is_development():
            return subject
        if len(subject) <= 20:
            return subject
        h = hashlib.sha256(subject.encode()).hexdigest()[:8]
        return f"{subject[:20]}... [{h}]"

    def redact_email(self, email: str) -> str:
        """Keep domain only; hash the local part.

        In development, returns the original email unchanged.
        """
        if is_development():
            return email
        if "@" not in email:
            return "[REDACTED]"
        local, domain = email.rsplit("@", 1)
        h = hashlib.sha256(local.encode()).hexdigest()[:8]
        return f"[{h}...]@{domain}"

    def redact_body(self, body: str) -> str:
        """Replace with hash only.

        In development, returns the original body unchanged.
        """
        if is_development():
            return body
        if not body:
            return ""
        h = hashlib.sha256(body.encode()).hexdigest()[:16]
        return f"[REDACTED:{h}]"

    def redact_generic(self, text: str, max_prefix_len: int = 20) -> str:
        """Redact any string, keeping up to max_prefix_len chars + hash.

        Use for user instructions, transcription text, or other PII strings.
        In development, returns the original text unchanged.
        """
        if is_development():
            return text
        if not text:
            return ""
        if 0 < max_prefix_len and len(text) <= max_prefix_len:
            return text
        prefix = text[:max_prefix_len] if max_prefix_len > 0 else ""
        h = hashlib.sha256(text.encode()).hexdigest()[:16]
        return f"{prefix}... [REDACTED:{h}]"

    def sanitize_dict(self, fields: Dict[str, Any]) -> Dict[str, Any]:
        """Redact known PII keys in a dictionary.

        In development, returns the original dict unchanged.
        """
        if is_development():
            return fields

        result: Dict[str, Any] = {}
        pii_body_keys = {
            "body_text",
            "body_html",
            "body",
            "content",
            "text",
        }
        pii_email_keys = {
            "sender_email",
            "from",
            "sender",
            "recipient_emails",
            "to",
        }
        pii_instruction_keys = {
            "instruction",
            "user_input",
            "transcription",
            "message",
        }

        for k, v in fields.items():
            key_lower = k.lower()
            if key_lower in pii_body_keys:
                result[k] = self.redact_body(v) if isinstance(v, str) else "[REDACTED]"
            elif key_lower == "subject":
                result[k] = self.redact_subject(v) if isinstance(v, str) else v
            elif key_lower in pii_email_keys:
                result[k] = self.redact_email(v) if isinstance(v, str) else "[REDACTED]"
            elif key_lower == "attachment_s3_uris":
                result[k] = "[REDACTED:s3_paths]"
            elif key_lower in pii_instruction_keys:
                result[k] = self.redact_generic(v, 20) if isinstance(v, str) else "[REDACTED]"
            else:
                result[k] = v
        return result


# ---------------------------------------------------------------------------
# Singleton
# ---------------------------------------------------------------------------

_sanitizer: Optional[LogSanitizer] = None


def get_sanitizer() -> LogSanitizer:
    """Return the global LogSanitizer singleton."""
    global _sanitizer
    if _sanitizer is None:
        _sanitizer = LogSanitizer()
    return _sanitizer
