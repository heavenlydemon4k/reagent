"""Profile service — user personalization and agent behavior settings."""

from typing import Optional, Dict, Any
from dataclasses import dataclass, field


@dataclass
class Profile:
    user_id: str
    system_prompt_suffix: str = ""
    preferences_json: Optional[Dict[str, Any]] = None


class ProfileService:
    """Manages user profiles. Creates default if none exists."""

    def get_or_create(self, user_id: str) -> Profile:
        """Fetch profile from DB or return defaults."""
        return Profile(user_id=user_id)

    def update(self, user_id: str, updates: Dict[str, Any]) -> Profile:
        """Update profile fields."""
        profile = self.get_or_create(user_id)
        if "system_prompt_suffix" in updates:
            profile.system_prompt_suffix = updates["system_prompt_suffix"]
        if "preferences" in updates:
            profile.preferences_json = updates["preferences"]
        return profile
