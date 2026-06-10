"""Profile and personalization models."""

from pydantic import BaseModel
from typing import Optional, List


class UserProfile(BaseModel):
    user_id: str
    name: str = "User"
    email: str = ""
    timezone: str = "UTC"
    language: str = "en"

    agent_detail_level: str = "concise"
    auto_handle_confidence: float = 0.92

    preferred_models: List[str] = ["claude-sonnet-4-6", "gpt-4o"]
    voice_enabled: bool = False
    notifications_enabled: bool = True

    default_consult_contacts: List[str] = []
    working_hours_start: int = 9
    working_hours_end: int = 17


class ProfileUpdate(BaseModel):
    name: Optional[str] = None
    timezone: Optional[str] = None
    language: Optional[str] = None
    agent_detail_level: Optional[str] = None
    auto_handle_confidence: Optional[float] = None
    voice_enabled: Optional[bool] = None
    notifications_enabled: Optional[bool] = None
