"""Chat service — integrates with agent orchestrator, email KB, and decision stack."""

from typing import Optional
import asyncio

from intelligence.app.agent.orchestrator import AgentOrchestrator
from intelligence.app.chat.session import SessionManager
from intelligence.app.profile.service import ProfileService


class ChatService:
    """Handles chat logic, agent responses, and card messages."""

    def __init__(self):
        self.sessions = SessionManager()
        self.orchestrator = AgentOrchestrator(profile=ProfileService())

    def create_session(self, user_id: str, title: str = "New Session", context: Optional[dict] = None):
        """Start a new chat session."""
        metadata = context or {}
        session = self.sessions.create(user_id, title, metadata)
        self.sessions.add_message(
            session.id, "agent",
            "Session started. I'm here to help with your inbox. Ask me anything or say 'start stack' to work through critical emails.",
            message_type="system"
        )
        return session

    def send_message(self, session_id: str, user_id: str, content: str) -> dict:
        """Process user message and generate agent response via orchestrator."""
        self.sessions.add_message(session_id, "user", content)
        result = asyncio.run(self.orchestrator.handle_message(user_id, session_id, content, self.sessions))
        return result

    def send_card(self, session_id: str, card_data: dict) -> dict:
        """Render a decision card as a chat message."""
        msg = self.sessions.add_message(
            session_id, "agent", card_data.get("title", ""),
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
