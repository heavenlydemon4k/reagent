"""
Prompt Templates for the Intelligence Layer.

All prompts are version-controlled Jinja2 templates.  Load them via:

    from intelligence.core.prompt_templates import load_template
    tmpl = load_template("compression")
    rendered = tmpl.render(chunks=chunks, relationship_context=ctx)

Template versions follow SemVer — bump MINOR on rule changes, PATCH on wording tweaks.
"""

from __future__ import annotations

import os
from typing import Optional

import jinja2

# ---------------------------------------------------------------------------
# Template loader
# ---------------------------------------------------------------------------

_TEMPLATE_DIR = os.path.dirname(__file__)

_jinja_env = jinja2.Environment(
    loader=jinja2.FileSystemLoader(_TEMPLATE_DIR),
    autoescape=False,
    trim_blocks=True,
    lstrip_blocks=True,
)


# Template version registry — bump when a template changes
_TEMPLATE_VERSIONS = {
    "compression": "1.0.0",
    "drafting": "1.0.0",
    "consultation": "1.0.0",
    "intent_parsing": "1.0.0",
}


def load_template(name: str) -> jinja2.Template:
    """
    Load a Jinja2 template by name (without ``.jinja2`` extension).

    Args:
        name: Template name — one of ``compression``, ``drafting``,
              ``consultation``, ``intent_parsing``.

    Returns:
        A Jinja2 ``Template`` object ready for ``.render()``.

    Raises:
        ValueError: If the template name is unknown.
    """
    if name not in _TEMPLATE_VERSIONS:
        raise ValueError(
            f"Unknown template '{name}'. "
            f"Available: {list(_TEMPLATE_VERSIONS.keys())}"
        )
    return _jinja_env.get_template(f"{name}.jinja2")


def get_template_version(name: str) -> Optional[str]:
    """Return the SemVer version of a template, or ``None`` if unknown."""
    return _TEMPLATE_VERSIONS.get(name)


def list_templates() -> dict:
    """Return a mapping of template name → version."""
    return dict(_TEMPLATE_VERSIONS)
