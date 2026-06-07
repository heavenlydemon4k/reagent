"""Decision stack — manages critical emails as cards for user sessions."""

import json
import time
from typing import List, Optional, Dict, Any
from dataclasses import dataclass, field
from uuid import uuid4

from intelligence.core import FallbackChain
from intelligence.app.email_kb.service import EmailKnowledgeBase, EmailContext


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
        self._cards: Dict[str, DecisionCard] = {}
        self._user_stacks: Dict[str, List[str]] = {}

    def receive_critical_email(self, user_id: str, email: EmailContext) -> DecisionCard:
        thread = self.kb.thread_context(email.email_id)
        card = self._generate_card(user_id, email, thread)
        self._cards[card.id] = card
        self._user_stacks.setdefault(user_id, []).append(card.id)
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

    def activate_next(self, user_id: str, session_id: str) -> Optional[DecisionCard]:
        stack = self._user_stacks.get(user_id, [])
        for card_id in stack:
            card = self._cards[card_id]
            if card.status == "queued":
                card.status = "active"
                card.session_id = session_id
                card.activated_at = time.time()
                return card
        return None

    def resolve_card(self, card_id: str, action_id: str, payload: Optional[Dict] = None) -> DecisionCard:
        card = self._cards.get(card_id)
        if not card:
            raise ValueError(f"Card {card_id} not found")
        card.status = "resolved"
        card.resolution = {"action_id": action_id, "payload": payload or {}}
        card.resolved_at = time.time()
        return card

    def get_stack(self, user_id: str) -> List[DecisionCard]:
        stack = self._user_stacks.get(user_id, [])
        return [self._cards[cid] for cid in stack if cid in self._cards]

    def get_active(self, session_id: str) -> Optional[DecisionCard]:
        for card in self._cards.values():
            if card.session_id == session_id and card.status == "active":
                return card
        return None

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
