"""Profile service — user personalization and agent behavior settings.

Backed by the `profiles` table so the user's name and tone preferences actually
reach the system prompt. Reads are non-destructive (a missing profile returns
defaults without inserting, avoiding a write-on-read and any users-table FK
risk); writes upsert.
"""

from typing import Optional, Dict, Any
from dataclasses import dataclass

from intelligence.app.db import db_session
from intelligence.app.models import ProfileModel


@dataclass
class Profile:
    user_id: str
    system_prompt_suffix: str = ""
    preferences_json: Optional[Dict[str, Any]] = None


class ProfileService:
    """Manages user profiles. Reads from the DB; returns defaults if none exists."""

    async def get_or_create(self, user_id: str) -> Profile:
        """Fetch the profile from the DB. Returns defaults (without inserting) when
        the user has no stored profile yet."""
        async with db_session() as db:
            from sqlalchemy import select
            result = await db.execute(select(ProfileModel).where(ProfileModel.user_id == user_id))
            row = result.scalar_one_or_none()
            if row is None:
                return Profile(user_id=user_id)
            return Profile(
                user_id=row.user_id,
                system_prompt_suffix=row.system_prompt_suffix or "",
                preferences_json=row.preferences_json,
            )

    async def update(self, user_id: str, updates: Dict[str, Any]) -> Profile:
        """Upsert the user's profile fields and return the updated profile."""
        async with db_session() as db:
            from sqlalchemy import select
            result = await db.execute(select(ProfileModel).where(ProfileModel.user_id == user_id))
            row = result.scalar_one_or_none()
            if row is None:
                row = ProfileModel(user_id=user_id)
                db.add(row)
            if "system_prompt_suffix" in updates:
                row.system_prompt_suffix = updates["system_prompt_suffix"]
            if "preferences" in updates:
                row.preferences_json = updates["preferences"]
            return Profile(
                user_id=user_id,
                system_prompt_suffix=row.system_prompt_suffix or "",
                preferences_json=row.preferences_json,
            )
