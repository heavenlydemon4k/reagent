"""Decision stack — manages critical emails as cards for user sessions."""

import json
import time
from typing import List, Optional, Dict, Any
from dataclasses import dataclass, field
from uuid import uuid4

from intelligence.core import FallbackChain
from intelligence.app.email_kb.service import EmailKnowledgeBase, EmailContext
from intelligence.app.db import db_session
from intelligence.app.models import CardModel, DecisionModel


@dataclass
class DecisionCard:
    id: str
    email_id: str
    user_id: str
    session_id: Optional[str]
    title: str
    body: str
    source_email: Dict[str, Any]
    options: List[Dict[str, str]]
    status: str
    resolution: Optional[Dict[str, Any]] = None
    created_at: float = field(default_factory=time.time)
    activated_at: Optional[float] = None
    resolved_at: Optional[float] = None


class DecisionStackService:
    """Receives classified critical emails, generates cards, manages stack order."""

    def __init__(self, kb: Optional[EmailKnowledgeBase] = None):
        self.kb = kb or EmailKnowledgeBase()
        self.llm = FallbackChain()

    async def receive_critical_email(self, user_id: str, email: EmailContext) -> DecisionCard:
        """Called by NATS consumer when Classification emits a critical email."""
        thread = self.kb.thread_context(email.email_id)
        card = self._generate_card(user_id, email, thread)
        async with db_session() as db:
            from datetime import datetime
            db_card = CardModel(
                id=card.id,
                user_id=user_id,
                email_id=email.email_id,
                card_type="decision",
                payload_json={
                    "title": card.title,
                    "body": card.body,
                    "options": card.options,
                    "source_email": card.source_email,
                },
                status="queued",
                created_at=datetime.utcnow(),
            )
            db.add(db_card)
        return card

    def _generate_card(self, user_id: str, email: EmailContext, thread: List[EmailContext]) -> DecisionCard:
        context = self.kb.summarize_for_agent(thread)
        prompt = f"""You are an email decision assistant. The user received this email and needs to decide what to do.

Email thread context:
{context}

Generate a JSON decision card with this exact shape:
{{
    "title": "Short, specific title",
    "body": "1-2 sentence summary of what the email is asking",
    "options": [
        {{"id": "reply", "label": "Reply", "style": "primary"}},
        {{"id": "forward", "label": "Forward", "style": "default"}},
        {{"id": "archive", "label": "Archive", "style": "default"}},
        {{"id": "snooze", "label": "Snooze", "style": "default"}},
        {{"id": "delegate", "label": "Delegate", "style": "default"}}
    ]
}}

Only return valid JSON. No markdown, no explanation."""

        response = self.llm.route(prompt, complexity="complex")
        try:
            parsed = json.loads(response.text)
        except json.JSONDecodeError:
            parsed = {
                "title": email.subject,
                "body": f"From {email.from_address}: {email.body_text[:200]}",
                "options": [
                    {"id": "reply", "label": "Reply", "style": "primary"},
                    {"id": "archive", "label": "Archive", "style": "default"},
                    {"id": "snooze", "label": "Snooze", "style": "default"},
                ],
            }

        return DecisionCard(
            id=str(uuid4()),
            email_id=email.email_id,
            user_id=user_id,
            session_id=None,
            title=parsed.get("title", email.subject),
            body=parsed.get("body", ""),
            source_email={
                "id": email.email_id,
                "subject": email.subject,
                "from": email.from_address,
                "to": email.to_addresses,
                "body_text": email.body_text,
                "received_at": email.received_at,
            },
            options=parsed.get("options", []),
            status="queued",
        )

    async def activate_next(self, user_id: str, session_id: str) -> Optional[DecisionCard]:
        """Get next queued card for user, mark active, assign session."""
        async with db_session() as db:
            from sqlalchemy import select
            from intelligence.app.models import CardModel
            result = await db.execute(
                select(CardModel)
                .where(CardModel.user_id == user_id, CardModel.status == "queued")
                .order_by(CardModel.created_at)
                .limit(1)
            )
            row = result.scalar_one_or_none()
            if not row:
                return None
            row.status = "active"
            row.session_id = session_id
            row.activated_at = __import__("datetime").datetime.utcnow()
            return self._row_to_card(row)

    async def resolve_card(self, card_id: str, action_id: str, payload: Optional[Dict] = None) -> DecisionCard:
        async with db_session() as db:
            from sqlalchemy import select
            from intelligence.app.models import CardModel
            result = await db.execute(select(CardModel).where(CardModel.id == card_id))
            row = result.scalar_one_or_none()
            if not row:
                raise ValueError(f"Card {card_id} not found")
            row.status = "resolved"
            row.resolution_json = {"action_id": action_id, "payload": payload or {}}
            row.resolved_at = __import__("datetime").datetime.utcnow()
            return self._row_to_card(row)

    async def get_active(self, session_id: str) -> Optional[DecisionCard]:
        async with db_session() as db:
            from sqlalchemy import select
            from intelligence.app.models import CardModel
            result = await db.execute(
                select(CardModel)
                .where(CardModel.session_id == session_id, CardModel.status == "active")
            )
            row = result.scalar_one_or_none()
            return self._row_to_card(row) if row else None

    async def get_stack(self, user_id: str) -> List[DecisionCard]:
        async with db_session() as db:
            from sqlalchemy import select
            from intelligence.app.models import CardModel
            result = await db.execute(
                select(CardModel)
                .where(CardModel.user_id == user_id)
                .order_by(CardModel.created_at)
            )
            rows = result.scalars().all()
            return [self._row_to_card(r) for r in rows]

    async def stack_length(self, user_id: str) -> int:
        async with db_session() as db:
            from sqlalchemy import select, func
            from intelligence.app.models import CardModel
            result = await db.execute(
                select(func.count()).select_from(CardModel).where(CardModel.user_id == user_id, CardModel.status == "queued")
            )
            return result.scalar() or 0

    async def get_card_by_id(self, card_id: str) -> Optional[DecisionCard]:
        async with db_session() as db:
            from sqlalchemy import select
            from intelligence.app.models import CardModel
            result = await db.execute(select(CardModel).where(CardModel.id == card_id))
            row = result.scalar_one_or_none()
            return self._row_to_card(row) if row else None

    async def send_and_resolve(self, card_id: str, user_id: str) -> Dict[str, Any]:
        """Send draft via Ingestion API, then resolve card."""
        async with db_session() as db:
            from sqlalchemy import select
            from intelligence.app.models import CardModel, DecisionModel
            result = await db.execute(select(CardModel).where(CardModel.id == card_id))
            card = result.scalar_one_or_none()
            if not card:
                return {"error": "Card not found"}

            dec_result = await db.execute(
                select(DecisionModel)
                .where(DecisionModel.card_id == card_id)
                .order_by(DecisionModel.created_at.desc())
                .limit(1)
            )
            decision = dec_result.scalar_one_or_none()
            if not decision:
                return {"error": "No draft found for this card"}

            sent = await self._call_ingestion_send(decision)
            if not sent.get("success"):
                return {"error": sent.get("error", "Send failed")}

            card.status = "resolved"
            card.resolved_at = __import__("datetime").datetime.utcnow()
            decision.sent_at = __import__("datetime").datetime.utcnow()
            decision.sent_message_id = sent.get("message_id")

            return {"success": True, "message_id": sent.get("message_id")}

    async def _call_ingestion_send(self, decision: DecisionModel) -> Dict[str, Any]:
        """POST to Ingestion send endpoint."""
        import os, httpx
        ingestion_url = os.getenv("INGESTION_URL", "http://localhost:8080")
        try:
            async with httpx.AsyncClient(timeout=30.0) as client:
                resp = await client.post(
                    f"{ingestion_url}/api/v1/send",
                    json={
                        "to": decision.draft_text.split("
")[0] if decision.draft_text else "",
                        "subject": "Re: " + (decision.draft_text[:50] if decision.draft_text else ""),
                        "body": decision.draft_text,
                        "draft_id": str(decision.id),
                    },
                )
                resp.raise_for_status()
                return {"success": True, "message_id": resp.json().get("message_id")}
        except Exception as e:
            return {"success": False, "error": str(e)}

    def to_message_payload(self, card: DecisionCard) -> Dict[str, Any]:
        return {
            "type": "card",
            "card_type": "decision",
            "title": card.title,
            "body": card.body,
            "source_email_id": card.email_id,
            "options": card.options,
            "metadata": {"card_id": card.id, "email_id": card.email_id},
        }

    def _row_to_card(self, row) -> DecisionCard:
        payload = row.payload_json or {}
        return DecisionCard(
            id=row.id,
            email_id=row.email_id,
            user_id=row.user_id,
            session_id=row.session_id,
            title=payload.get("title", ""),
            body=payload.get("body", ""),
            source_email=payload.get("source_email", {}),
            options=payload.get("options", []),
            status=row.status,
            resolution=row.resolution_json,
            created_at=row.created_at.timestamp() if row.created_at else 0,
            activated_at=row.activated_at.timestamp() if row.activated_at else None,
            resolved_at=row.resolved_at.timestamp() if row.resolved_at else None,
        )
