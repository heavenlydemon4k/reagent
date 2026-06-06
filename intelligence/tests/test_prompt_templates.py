"""
Tests for prompt templates (prompt_templates/).

Covers:
- Template loading via the public API
- All templates render without error
- Version registry is consistent
"""

import pytest
from intelligence.core.prompt_templates import (
    load_template,
    get_template_version,
    list_templates,
)


class TestLoadTemplate:
    def test_load_compression(self):
        tmpl = load_template("compression")
        assert tmpl is not None
        rendered = tmpl.render(
            chunks=[
                {"chunk_id": "c1", "text": "Let's discuss the proposal."},
                {"chunk_id": "c2", "text": "The deadline is Friday."},
            ],
            relationship_context="Long-term vendor relationship",
        )
        assert "Decision Stack's intelligence engine" in rendered
        assert "[c1]" in rendered
        assert "[c2]" in rendered
        assert "RELATIONSHIP CONTEXT:" in rendered
        assert "they_want" in rendered
        assert "need_from_user" in rendered

    def test_load_drafting(self):
        tmpl = load_template("drafting")
        assert tmpl is not None
        rendered = tmpl.render(
            decision_context="Vendor wants a contract renewal by Friday",
            user_intent="agree but negotiate terms",
            user_style="Professional, concise",
            prior_emails=[
                {"sender": "vendor@example.com", "date": "2024-01-01", "body": "Hi, let's renew."},
            ],
        )
        assert "email drafting assistant" in rendered
        assert "DECISION CONTEXT:" in rendered
        assert "USER INTENT:" in rendered

    def test_load_consultation(self):
        tmpl = load_template("consultation")
        assert tmpl is not None
        rendered = tmpl.render(
            chunks=[
                {"chunk_id": "c1", "text": "The contract expires on March 1."},
            ],
            question="When does the contract expire?",
        )
        assert "consultation assistant" in rendered
        assert "When does the contract expire?" in rendered
        assert "[c1]" in rendered

    def test_load_intent_parsing(self):
        tmpl = load_template("intent_parsing")
        assert tmpl is not None
        rendered = tmpl.render(
            user_message="Draft a reply to the vendor about the contract",
            active_cards=[
                {"card_id": "card_1", "they_want": "Contract renewal", "need_from_user": "Decision on renewal"},
            ],
        )
        assert "intent parser" in rendered
        assert "Draft a reply" in rendered
        assert "ALLOWED ACTIONS:" in rendered
        assert "draft_email" in rendered

    def test_unknown_template_raises(self):
        with pytest.raises(ValueError, match="Unknown template"):
            load_template("nonexistent")


class TestVersions:
    def test_all_templates_have_versions(self):
        templates = list_templates()
        assert "compression" in templates
        assert "drafting" in templates
        assert "consultation" in templates
        assert "intent_parsing" in templates

    def test_versions_are_semver(self):
        templates = list_templates()
        for name, version in templates.items():
            parts = version.split(".")
            assert len(parts) == 3
            assert all(p.isdigit() for p in parts)

    def test_get_version(self):
        assert get_template_version("compression") == "1.0.0"
        assert get_template_version("nonexistent") is None


class TestTemplateStructure:
    """Verify each template contains expected structural elements."""

    @pytest.mark.parametrize("name,expected_phrases", [
        ("compression", ["RULES:", "JSON", "they_want", "need_from_user"]),
        ("drafting", ["RULES:", "DECISION CONTEXT:", "Output the email body"]),
        ("consultation", ["RULES:", "USER QUESTION:", "Respond in plain text"]),
        ("intent_parsing", ["RULES:", "ALLOWED ACTIONS:", "JSON"]),
    ])
    def test_template_has_structure(self, name, expected_phrases):
        tmpl = load_template(name)
        # Render with minimal data and check both template and rendered output
        rendered = tmpl.render(
            chunks=[{"chunk_id": "c1", "text": "sample"}],
            relationship_context="sample context",
            question="sample question",
            user_message="sample message",
            decision_context="sample decision",
            user_intent="sample intent",
            active_cards=[],
            conversation_history=[],
        ) if name != "compression" else tmpl.render(
            chunks=[{"chunk_id": "c1", "text": "sample"}],
            relationship_context="sample context",
        )
        for phrase in expected_phrases:
            assert phrase in rendered, f"Template '{name}' missing '{phrase}'"
