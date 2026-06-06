"""Tests for the PII log sanitizer."""

import os
import pytest

from .sanitizer import LogSanitizer, get_sanitizer, is_production, is_development


# ---------------------------------------------------------------------------
# Environment helpers
# ---------------------------------------------------------------------------


class TestEnvironmentHelpers:
    def test_is_production_true(self):
        os.environ["ENV"] = "production"
        assert is_production() is True
        del os.environ["ENV"]

    def test_is_production_staging(self):
        os.environ["ENV"] = "staging"
        assert is_production() is True
        del os.environ["ENV"]

    def test_is_production_false(self):
        os.environ["ENV"] = "development"
        assert is_production() is False
        del os.environ["ENV"]

    def test_is_development_true(self):
        os.environ["ENV"] = "development"
        assert is_development() is True
        del os.environ["ENV"]

    def test_is_development_local(self):
        os.environ["ENV"] = "local"
        assert is_development() is True
        del os.environ["ENV"]

    def test_is_development_empty(self):
        if "ENV" in os.environ:
            del os.environ["ENV"]
        assert is_development() is True


# ---------------------------------------------------------------------------
# LogSanitizer
# ---------------------------------------------------------------------------


class TestRedactSubject:
    @pytest.fixture(autouse=True)
    def force_production(self):
        os.environ["ENV"] = "production"
        yield
        del os.environ["ENV"]

    def test_short_subject_unchanged(self):
        s = LogSanitizer()
        subject = "Short subj"
        assert s.redact_subject(subject) == subject

    def test_long_subject_redacted(self):
        s = LogSanitizer()
        subject = "This is a very long email subject line that exceeds twenty characters"
        result = s.redact_subject(subject)
        assert result.startswith("This is a very long ")
        assert "... [" in result

    def test_development_passes_through(self):
        os.environ["ENV"] = "development"
        s = LogSanitizer()
        subject = "This is a very long email subject line that exceeds twenty characters"
        assert s.redact_subject(subject) == subject


class TestRedactEmail:
    @pytest.fixture(autouse=True)
    def force_production(self):
        os.environ["ENV"] = "production"
        yield
        del os.environ["ENV"]

    def test_valid_email_redacted(self):
        s = LogSanitizer()
        email = "john.doe@example.com"
        result = s.redact_email(email)
        assert "john.doe" not in result
        assert result.endswith("@example.com")
        assert result.startswith("[")

    def test_invalid_email(self):
        s = LogSanitizer()
        assert s.redact_email("not-an-email") == "[REDACTED]"

    def test_development_passes_through(self):
        os.environ["ENV"] = "development"
        s = LogSanitizer()
        email = "john.doe@example.com"
        assert s.redact_email(email) == email


class TestRedactBody:
    @pytest.fixture(autouse=True)
    def force_production(self):
        os.environ["ENV"] = "production"
        yield
        del os.environ["ENV"]

    def test_non_empty_body_redacted(self):
        s = LogSanitizer()
        body = "This is the body of an email with sensitive content."
        result = s.redact_body(body)
        assert result.startswith("[REDACTED:")
        assert "sensitive content" not in result

    def test_empty_body(self):
        s = LogSanitizer()
        assert s.redact_body("") == ""

    def test_development_passes_through(self):
        os.environ["ENV"] = "development"
        s = LogSanitizer()
        body = "This is the body of an email with sensitive content."
        assert s.redact_body(body) == body


class TestSanitizeDict:
    @pytest.fixture(autouse=True)
    def force_production(self):
        os.environ["ENV"] = "production"
        yield
        del os.environ["ENV"]

    def test_body_text_redacted(self):
        s = LogSanitizer()
        fields = {
            "body_text": "sensitive email body content here",
            "other_key": "safe value",
        }
        result = s.sanitize_dict(fields)
        assert result["body_text"].startswith("[REDACTED:")
        assert result["other_key"] == "safe value"

    def test_subject_redacted(self):
        s = LogSanitizer()
        fields = {"subject": "This is a very long email subject that should be truncated"}
        result = s.sanitize_dict(fields)
        assert "... [" in result["subject"]

    def test_sender_email_redacted(self):
        s = LogSanitizer()
        fields = {"sender_email": "alice@company.com"}
        result = s.sanitize_dict(fields)
        assert "alice" not in result["sender_email"]
        assert result["sender_email"].endswith("@company.com")

    def test_instruction_redacted(self):
        s = LogSanitizer()
        fields = {"instruction": "Please reply saying I accept the offer of $100k salary"}
        result = s.sanitize_dict(fields)
        assert "REDACTED" in result["instruction"]

    def test_development_passes_through(self):
        os.environ["ENV"] = "development"
        s = LogSanitizer()
        fields = {
            "body_text": "sensitive content",
            "subject": "long subject that would be truncated",
            "sender_email": "alice@company.com",
        }
        result = s.sanitize_dict(fields)
        assert result["body_text"] == "sensitive content"
        assert result["subject"] == "long subject that would be truncated"
        assert result["sender_email"] == "alice@company.com"


class TestRedactGeneric:
    @pytest.fixture(autouse=True)
    def force_production(self):
        os.environ["ENV"] = "production"
        yield
        del os.environ["ENV"]

    def test_generic_text_redacted(self):
        s = LogSanitizer()
        text = "This is a user instruction that should be redacted for privacy"
        result = s.redact_generic(text, 10)
        assert result.startswith("This is a ")
        assert "[REDACTED:" in result

    def test_short_text_unchanged(self):
        s = LogSanitizer()
        text = "short"
        assert s.redact_generic(text, 10) == text

    def test_empty_text(self):
        s = LogSanitizer()
        assert s.redact_generic("", 10) == ""


class TestGetSanitizer:
    def test_singleton(self):
        s1 = get_sanitizer()
        s2 = get_sanitizer()
        assert s1 is s2
