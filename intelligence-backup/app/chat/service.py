"""Chat service — integrates with agent orchestrator, email KB, and decision stack."""

from typing import Optional, Dict, Any
import time
from datetime import datetime
from uuid import uuid4

from intelligence.app.agent.orchestrator import AgentOrchestrator
from intelligence.app.chat.session import SessionManager
from intelligence.app.profile.service import ProfileService
from intelligence.app.db import db_session
from intelligence.app.models import ChatSessionModel, MessageModel


def _to_client_msg(msg: dict) -> dict:
    """Convert internal message format to client-expected format."""
    return {
        "id": msg.get("id", str(uuid4())),
        "sender_type": msg.get("role", "agent"),
        "message_type": msg.get("type", "text"),
        "content_text": msg.get("content", ""),
        "card_payload": msg.get("card"),
        "created_at": datetime.fromtimestamp(msg.get("timestamp", time.time())).isoformat(),
    }


class ChatService:
    """Handles chat logic, agent responses, and card messages."""

    def __init__(self):
        self.sessions = SessionManager()
        self.orchestrator = AgentOrchestrator(profile=ProfileService())

    async def create_session(self, user_id: str, title: str = "New Session", context: Optional[dict] = None):
        """Start a new chat session — persist to DB, cache in memory."""
        metadata = context or {}
        session = self.sessions.create(user_id, title, metadata)
        # Persist to DB
        async with db_session() as db:
            db_session_obj = ChatSessionModel(
                id=session.id,
                user_id=user_id,
                title=title,
                status="active",
                metadata_json=metadata,
                created_at=datetime.utcnow(),
                updated_at=datetime.utcnow(),
            )
            db.add(db_session_obj)
        # Add system message
        sys_msg = self.sessions.add_message(
            session.id, "agent",
            "Session started. I'm here to help with your inbox. Ask me anything or say 'start stack' to work through critical emails.",
            message_type="system"
        )
        await self._persist_message(session.id, "agent", sys_msg["content"], "system")
        return session

    async def send_message(self, session_id: str, user_id: str, content: str) -> dict:
        """Process user message and generate agent response via orchestrator."""
        self.sessions.add_message(session_id, "user", content)
        await self._persist_message(session_id, "user", content, "text")
        result = await self.orchestrator.handle_message(user_id, session_id, content, self.sessions)
        # Persist agent response
        if result.get("message"):
            msg = result["message"]
            await self._persist_message(
                session_id, "agent", msg.get("content", ""),
                msg.get("type", "text"),
                card_payload=msg.get("card"),
                cost_usd=result.get("cost_usd"),
                source_email_id=(result.get("source_email_ids") or [None])[0],
            )
        # Transform for client
        if "message" in result:
            result["message"] = _to_client_msg(result["message"])
        return result

    async def send_card(self, session_id: str, card_data: dict) -> dict:
        """Render a decision card as a chat message."""
        msg = self.sessions.add_message(
            session_id, "agent", card_data.get("title", ""),
            message_type="card", card_data=card_data
        )
        await self._persist_message(session_id, "agent", card_data.get("title", ""), "card", card_payload=card_data)
        return _to_client_msg(msg)

    async def handle_card_action(self, session_id: str, user_id: str, card_id: str, action_id: str, payload: Optional[dict] = None) -> dict:
        """Handle a card action (send, edit, discard, etc.) from the user."""
        result = await self.orchestrator.handle_card_action(user_id, session_id, card_id, action_id, payload, self.sessions)
        if "message" in result:
            result["message"] = _to_client_msg(result["message"])
        return result

    async def handle_source_request(self, session_id: str, email_id: str) -> dict:
        """Fetch source email for verification."""
        from intelligence.app.email_kb.service import EmailKnowledgeBase
        kb = EmailKnowledgeBase()
        thread = kb.thread_context(email_id)
        if not thread:
            return {"error": "Email not found"}
        email = thread[0]
        return {
            "id": email.email_id,
            "subject": email.subject,
            "from": email.from_address,
            "to": email.to_addresses,
            "body_text": email.body_text,
            "received_at": email.received_at,
            "labels": email.labels,
        }

    def get_history(self, session_id: str) -> list:
        session = self.sessions.get(session_id)
        if not session:
            return []
        return [_to_client_msg(m) for m in session.messages]

    async def list_sessions(self, user_id: str) -> list:
        # Check memory first, then DB
        memory_sessions = self.sessions.list_for_user(user_id)
        if memory_sessions:
            return [
                {
                    "id": s.id,
                    "title": s.title,
                    "created_at": s.created_at,
                    "updated_at": s.updated_at,
                    "message_count": len(s.messages),
                }
                for s in memory_sessions
            ]
        # Fallback to DB
        async with db_session() as db:
            from sqlalchemy import select
            from intelligence.app.models import ChatSessionModel
            result = await db.execute(
                select(ChatSessionModel).where(ChatSessionModel.user_id == user_id).order_by(ChatSessionModel.updated_at.desc())
            )
            rows = result.scalars().all()
            return [
                {
                    "id": r.id,
                    "title": r.title,
                    "created_at": r.created_at.timestamp() if r.created_at else 0,
                    "updated_at": r.updated_at.timestamp() if r.updated_at else 0,
                    "message_count": len(r.messages),
                }
                for r in rows
            ]

    async def _persist_message(self, session_id: str, sender_type: str, content: str, message_type: str,
                                 card_payload: Optional[dict] = None, cost_usd: Optional[float] = None,
                                 source_email_id: Optional[str] = None):
        async with db_session() as db:
            msg = MessageModel(
                id=str(uuid4()),
                session_id=session_id,
                sender_type=sender_type,
                message_type=message_type,
                content_text=content,
                card_payload_json=card_payload,
                cost_usd=cost_usd,
                source_email_id=source_email_id,
                created_at=datetime.utcnow(),
            )
            db.add(msg)
            # Update session last_message_at
            from sqlalchemy import select
            from intelligence.app.models import ChatSessionModel
            result = await db.execute(select(ChatSessionModel).where(ChatSessionModel.id == session_id))
            session = result.scalar_one_or_none()
            if session:
                session.last_message_at = datetime.utcnow()
                session.updated_at = datetime.utcnow()
