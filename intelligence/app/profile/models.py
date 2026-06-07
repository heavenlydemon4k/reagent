import os

os.makedirs("intelligence/app/chat", exist_ok=True)
os.makedirs("intelligence/app/profile", exist_ok=True)

files = {
    "intelligence/app/chat/__init__.py": '"""\n',
    "intelligence/app/profile/__init__.py": '"""\n',
    "intelligence/main.py": '''"""Decision Stack Intelligence Service — Chat-first API."""

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from intelligence.app.chat.router import router as chat_router
from intelligence.app.profile.router import router as profile_router

app = FastAPI(title="Decision Stack Intelligence", version="2.0.0")

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(chat_router, prefix="/chat", tags=["chat"])
app.include_router(profile_router, prefix="/profile", tags=["profile"])


@app.get("/health")
async def health():
    return {"status": "ok", "service": "intelligence"}
''',
    "intelligence/app/chat/session.py": '''"""Session management for chat conversations."""

import time
from dataclasses import dataclass, field
from typing import Optional
from uuid import uuid4


@dataclass
class ChatSession:
    id: str
    user_id: str
    title: str
    created_at: float
    updated_at: float
    messages: list = field(default_factory=list)
    metadata: dict = field(default_factory=dict)

    def add_message(self, role: str, content: str, message_type: str = "text", card_data: Optional[dict] = None):
        msg = {
            "id": str(uuid4()),
            "role": role,
            "content": content,
            "type": message_type,
            "card": card_data,
            "timestamp": time.time(),
        }
        self.messages.append(msg)
        self.updated_at = time.time()
        return msg


class SessionManager:
    """In-memory session store. Replace with Redis/DB in production."""

    def __init__(self):
        self._sessions: dict[str, ChatSession] = {}

    def create(self, user_id: str, title: str = "New Session", metadata: Optional[dict] = None) -> ChatSession:
        session = ChatSession(
            id=str(uuid4()),
            user_id=user_id,
            title=title,
            created_at=time.time(),
            updated_at=time.time(),
            metadata=metadata or {},
        )
        self._sessions[session.id] = session
        return session

    def get(self, session_id: str) -> Optional[ChatSession]:
        return self._sessions.get(session_id)

    def list_for_user(self, user_id: str) -> list:
        return [s for s in self._sessions.values() if s.user_id == user_id]

    def add_message(self, session_id: str, role: str, content: str, message_type: str = "text", card_data: Optional[dict] = None):
        session = self._sessions.get(session_id)
        if not session:
            raise ValueError("Session not found")
        return session.add_message(role, content, message_type, card_data)
''',
    "intelligence/app/chat/service.py": '''"""Chat service — agent responses, card rendering, session management."""

from typing import Optional

from intelligence.core import FallbackChain
from intelligence.app.chat.session import SessionManager


class ChatService:
    """Handles chat logic, agent responses, and card messages."""

    def __init__(self):
        self.sessions = SessionManager()
        self.llm = FallbackChain()

    def create_session(self, user_id: str, title: str = "New Session", context: Optional[dict] = None):
        """Start a new chat session about a specific decision or topic."""
        metadata = context or {}
        session = self.sessions.create(user_id, title, metadata)

        self.sessions.add_message(
            session.id, "agent",
            "Session started. I will help you with this decision.",
            message_type="system"
        )
        return session

    def send_message(self, session_id: str, user_id: str, content: str) -> dict:
        """Process user message and generate agent response."""
        self.sessions.add_message(session_id, "user", content)

        session = self.sessions.get(session_id)
        history = self._format_history(session.messages[-10:])

        system = self._build_system_prompt(session)
        response = self.llm.route(
            f"{history}\nUser: {content}\nAgent:",
            system=system
        )

        agent_msg = self.sessions.add_message(session_id, "agent", response.text)

        return {
            "session_id": session_id,
            "message": agent_msg,
            "cost_usd": response.meter.total_cost_usd,
            "model": response.model,
        }

    def send_card(self, session_id: str, card_data: dict) -> dict:
        """Render a decision card as a chat message."""
        card_text = self._render_card(card_data)
        msg = self.sessions.add_message(
            session_id, "agent", card_text,
            message_type="card", card_data=card_data
        )
        return msg

    def get_history(self, session_id: str) -> list:
        session = self.sessions.get(session_id)
        if not session:
            return []
        return session.messages

    def list_sessions(self, user_id: str) -> list:
        sessions = self.sessions.list_for_user(user_id)
        return [
            {
                "id": s.id,
                "title": s.title,
                "created_at": s.created_at,
                "updated_at": s.updated_at,
                "message_count": len(s.messages),
            }
            for s in sessions
        ]

    def _format_history(self, messages: list) -> str:
        lines = []
        for m in messages:
            lines.append(f"{m['role']}: {m['content']}")
        return "\n".join(lines)

    def _build_system_prompt(self, session) -> str:
        base = "You are a decision assistant. Help the user make clear, fast decisions."
        if session.metadata.get("decision_type"):
            base += f" This session is about: {session.metadata['decision_type']}."
        if session.metadata.get("deadline"):
            base += f" Deadline: {session.metadata['deadline']}."
        return base

    def _render_card(self, card: dict) -> str:
        title = card.get("title", "Decision")
        summary = card.get("summary", "")
        actions = card.get("actions", [])
        deadline = card.get("deadline", "")

        text = f"**{title}**\n\n{summary}\n\n"
        if deadline:
            text += f"Deadline: {deadline}\n\n"
        if actions:
            text += "Actions: " + " | ".join(actions)
        return text
''',
    "intelligence/app/chat/router.py": '''"""Chat API routes — sessions, messages, WebSocket."""

from fastapi import APIRouter, WebSocket, WebSocketDisconnect
from typing import Optional

from intelligence.app.chat.service import ChatService

router = APIRouter()
chat_service = ChatService()


@router.post("/sessions")
async def create_session(user_id: str, title: Optional[str] = "New Session", context: Optional[dict] = None):
    session = chat_service.create_session(user_id, title, context)
    return {"id": session.id, "title": session.title, "created_at": session.created_at}


@router.get("/sessions")
async def list_sessions(user_id: str):
    return chat_service.list_sessions(user_id)


@router.get("/sessions/{session_id}")
async def get_session(session_id: str):
    history = chat_service.get_history(session_id)
    return {"session_id": session_id, "messages": history}


@router.post("/sessions/{session_id}/messages")
async def send_message(session_id: str, user_id: str, content: str):
    result = chat_service.send_message(session_id, user_id, content)
    return result


@router.post("/sessions/{session_id}/cards")
async def send_card(session_id: str, card_data: dict):
    msg = chat_service.send_card(session_id, card_data)
    return msg


@router.websocket("/ws/{session_id}")
async def chat_websocket(websocket: WebSocket, session_id: str):
    await websocket.accept()
    try:
        while True:
            data = await websocket.receive_json()
            user_id = data.get("user_id")
            content = data.get("content")
            result = chat_service.send_message(session_id, user_id, content)
            await websocket.send_json(result)
    except WebSocketDisconnect:
        pass
''',
    "intelligence/app/profile/models.py": '''"""Profile and personalization models."""

from pydantic import BaseModel
from typing import Optional, List


class UserProfile(BaseModel):
    user_id: str
    name: str = "User"
    email: str = ""
    timezone: str = "UTC"
    language: str = "en"

    agent_tone: str = "direct"
    agent_detail_level: str = "concise"
    auto_handle_confidence: float = 0.92

    preferred_models: List[str] = ["gpt-4o", "claude-3-sonnet"]
    voice_enabled: bool = False
    notifications_enabled: bool = True

    default_consult_contacts: List[str] = []
    working_hours_start: int = 9
    working_hours_end: int = 17


class ProfileUpdate(BaseModel):
    name: Optional[str] = None
    timezone: Optional[str] = None
    language: Optional[str] = None
    agent_tone: Optional[str] = None
    agent_detail_level: Optional[str] = None
    auto_handle_confidence: Optional[float] = None
    voice_enabled: Optional[bool] = None
    notifications_enabled: Optional[bool] = None
''',
    "intelligence/app/profile/service.py": '''"""Profile service — load/save user preferences."""

from typing import Optional

from intelligence.app.profile.models import UserProfile, ProfileUpdate


class ProfileService:
    """In-memory profile store. Replace with DB in production."""

    def __init__(self):
        self._profiles: dict[str, UserProfile] = {}

    def get_or_create(self, user_id: str, email: str = "", name: str = "") -> UserProfile:
        if user_id not in self._profiles:
            self._profiles[user_id] = UserProfile(
                user_id=user_id,
                email=email,
                name=name or "User",
            )
        return self._profiles[user_id]

    def update(self, user_id: str, update: ProfileUpdate) -> UserProfile:
        profile = self._profiles.get(user_id)
        if not profile:
            raise ValueError("Profile not found")

        data = update.model_dump(exclude_unset=True)
        for key, value in data.items():
            setattr(profile, key, value)

        return profile

    def get(self, user_id: str) -> Optional[UserProfile]:
        return self._profiles.get(user_id)
''',
    "intelligence/app/profile/router.py": '''"""Profile API routes."""

from fastapi import APIRouter

from intelligence.app.profile.service import ProfileService
from intelligence.app.profile.models import ProfileUpdate

router = APIRouter()
profile_service = ProfileService()


@router.get("/{user_id}")
async def get_profile(user_id: str):
    profile = profile_service.get_or_create(user_id)
    return profile.model_dump()


@router.put("/{user_id}")
async def update_profile(user_id: str, update: ProfileUpdate):
    profile = profile_service.update(user_id, update)
    return profile.model_dump()


@router.get("/{user_id}/preferences")
async def get_preferences(user_id: str):
    profile = profile_service.get_or_create(user_id)
    return {
        "agent_tone": profile.agent_tone,
        "agent_detail_level": profile.agent_detail_level,
        "auto_handle_confidence": profile.auto_handle_confidence,
        "voice_enabled": profile.voice_enabled,
        "notifications_enabled": profile.notifications_enabled,
    }
''',
}

for path, content in files.items():
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)
    print(f"Created: {path}")

print("\nDone. Run:")
print("  git add intelligence/")
print("  git commit -m 'feat: chat-first architecture'")
print("  git push origin main")