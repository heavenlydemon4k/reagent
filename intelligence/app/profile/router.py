"""Profile API routes — user personalization and agent behavior."""

from fastapi import APIRouter, Depends
from typing import Optional

from intelligence.app.auth import get_current_user
from intelligence.app.profile.service import ProfileService

router = APIRouter()
profile_service = ProfileService()


@router.get("/me")
async def get_my_profile(user_id: str = Depends(get_current_user)):
    """Get current user's profile."""
    profile = profile_service.get_or_create(user_id)
    return {
        "user_id": profile.user_id,
        "system_prompt_suffix": profile.system_prompt_suffix,
        "preferences": profile.preferences_json or {},
    }


@router.put("/me")
async def update_my_profile(
    updates: dict,
    user_id: str = Depends(get_current_user),
):
    """Update current user's profile."""
    profile = profile_service.update(user_id, updates)
    return {
        "user_id": profile.user_id,
        "system_prompt_suffix": profile.system_prompt_suffix,
        "preferences": profile.preferences_json or {},
    }


@router.get("/me/preferences")
async def get_my_preferences(user_id: str = Depends(get_current_user)):
    """Get agent behavior preferences."""
    profile = profile_service.get_or_create(user_id)
    prefs = profile.preferences_json or {}
    return {
        "agent_detail_level": prefs.get("agent_detail_level", "concise"),
        "auto_handle_confidence": prefs.get("auto_handle_confidence", 0.85),
        "voice_enabled": prefs.get("voice_enabled", False),
        "notifications_enabled": prefs.get("notifications_enabled", True),
    }
