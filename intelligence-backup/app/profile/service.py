"""Profile service — user personalization and agent behavior settings."""

from typing import Optional, Dict, Any
from dataclasses import dataclass, field

from intelligence.app.db import db_session
from intelligence.app.models import ProfileModel


@dataclass
class Profile:
    user_id: str
    agent_name: str = "Reagent"
    agent_tone: str = "professional"
    system_prompt_suffix: str = ""
    preferences_json: Optional[Dict[str, Any]] = None


class ProfileService:
    """Manages user profiles. Creates default if none exists."""

    def get_or_create(self, user_id: str) -> Profile:
        """Fetch profile from DB or return defaults."""
        # For now, return in-memory defaults. DB persistence can be added later.
        return Profile(user_id=user_id)

    def update(self, user_id: str, updates: Dict[str, Any]) -> Profile:
        """Update profile fields."""
        profile = self.get_or_create(user_id)
        if "agent_name" in updates:
            profile.agent_name = updates["agent_name"]
        if "agent_tone" in updates:
            profile.agent_tone = updates["agent_tone"]
        if "system_prompt_suffix" in updates:
            profile.system_prompt_suffix = updates["system_prompt_suffix"]
        if "preferences" in updates:
            profile.preferences_json = updates["preferences"]
        return profile
