"""Session management for chat conversations."""

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
