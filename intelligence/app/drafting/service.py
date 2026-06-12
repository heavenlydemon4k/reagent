"""Drafting engine — generates email replies for user approval."""

from typing import Optional, Dict, Any, List

from intelligence.core import FallbackChain
from intelligence.app.email_kb.service import EmailKnowledgeBase, EmailContext
from intelligence.app.profile.service import ProfileService
from intelligence.app.db import db_session
from intelligence.app.models import DecisionModel


class DraftingService:
    """Drafts email responses based on user decision + context + profile."""

    def __init__(
        self,
        llm: Optional[FallbackChain] = None,
        kb: Optional[EmailKnowledgeBase] = None,
        profile: Optional[ProfileService] = None,
    ):
        self.llm = llm or FallbackChain()
        self.kb = kb or EmailKnowledgeBase()
        self.profile = profile

    async def draft_reply(
        self,
        user_id: str,
        email_id: str,
        decision_action: str,
        user_instruction: Optional[str] = None,
        thread_context: Optional[List[EmailContext]] = None,
        card_id: Optional[str] = None,
    ) -> Dict[str, Any]:
        profile = (await self.profile.get_or_create(user_id)) if self.profile else None
        if thread_context is None:
            thread_context = self.kb.thread_context(email_id)
        email = thread_context[0] if thread_context else None
        if not email:
            raise ValueError(f"Email {email_id} not found")

        context_str = self.kb.summarize_for_agent(thread_context)
        suffix = profile.system_prompt_suffix if profile else ""
        instruction = user_instruction or self._default_instruction(decision_action)

        prompt = f"""You are an email agent drafting a reply on behalf of the user. Be direct and concise. Do not send — the user reviews first. {suffix}


Thread context:
{context_str}

User instruction: {instruction}

Draft a concise, natural email reply. Include a subject line prefixed with "Subject: ".

Return ONLY the draft text. No JSON, no markdown, no explanation."""

        response = await self.llm.route(prompt, complexity="complex")
        draft_text = response.text.strip()
        subject = email.subject
        if draft_text.startswith("Subject:"):
            parts = draft_text.split("\n", 1)
            subject = parts[0].replace("Subject:", "").strip()
            draft_text = parts[1].strip() if len(parts) > 1 else draft_text

        await self._persist_draft(
            user_id=user_id,
            card_id=card_id or email_id,
            action_type=decision_action,
            draft_text=draft_text,
            to_address=email.from_address,
            subject=subject,
            thread_id=email.thread_id,
            account_id=getattr(email, "account_id", None),
        )

        return {
            "draft_text": draft_text,
            "subject": subject,
            "to": email.from_address,
            "source_email_id": email_id,
            "cost_usd": response.meter.total_cost_usd,
            "model": response.model,
        }

    async def draft_forward(
        self,
        user_id: str,
        email_id: str,
        forward_to: str,
        note: Optional[str] = None,
    ) -> Dict[str, Any]:
        thread = self.kb.thread_context(email_id)
        email = thread[0] if thread else None
        if not email:
            raise ValueError(f"Email {email_id} not found")
        prompt = f"""You are an email agent. Be direct and concise.

Draft a forwarding email to {forward_to}. Include the original email below your note.

Original subject: {email.subject}
Original from: {email.from_address}

{f"User note: {note}" if note else ""}

Return ONLY the draft text."""

        response = await self.llm.route(prompt, complexity="complex")
        return {
            "draft_text": response.text.strip(),
            "subject": f"Fwd: {email.subject}",
            "to": forward_to,
            "source_email_id": email_id,
            "cost_usd": response.meter.total_cost_usd,
            "model": response.model,
        }

    async def edit_draft(self, card_id: str, edit_text: str) -> Dict[str, Any]:
        """Apply user edits to a draft. Loads the current draft so the model can
        edit what it can see, then persists the updated text."""
        current = await self._load_latest_draft(card_id)
        current_draft = (current or {}).get("draft_text", "") or ""
        current_subject = (current or {}).get("subject", "") or ""

        prompt = f"""The user wants to revise the email draft below. Apply their instruction and return the full updated draft — keep everything they did not ask to change.

Current draft:
Subject: {current_subject}
{current_draft}

User edit instruction: {edit_text}

Return ONLY the updated draft text. Include a "Subject: " line."""

        response = await self.llm.route(prompt, complexity="complex")
        draft_text = response.text.strip()
        subject = current_subject
        if draft_text.startswith("Subject:"):
            parts = draft_text.split("\n", 1)
            subject = parts[0].replace("Subject:", "").strip()
            draft_text = parts[1].strip() if len(parts) > 1 else draft_text

        if current and current.get("id"):
            await self._update_draft(current["id"], draft_text, subject)

        return {
            "draft_text": draft_text,
            "subject": subject,
            "cost_usd": response.meter.total_cost_usd,
            "model": response.model,
        }

    async def _load_latest_draft(self, card_id: str) -> Optional[Dict[str, Any]]:
        """Return the most recent persisted draft for a card as a plain dict
        (values copied inside the session to avoid detached-instance access)."""
        async with db_session() as db:
            from sqlalchemy import select
            result = await db.execute(
                select(DecisionModel)
                .where(DecisionModel.card_id == card_id)
                .order_by(DecisionModel.created_at.desc())
                .limit(1)
            )
            row = result.scalar_one_or_none()
            if not row:
                return None
            return {
                "id": row.id,
                "draft_text": row.draft_text,
                "subject": row.subject,
                "to_address": row.to_address,
                "thread_id": row.thread_id,
                "account_id": row.account_id,
            }

    async def _update_draft(self, decision_id: str, draft_text: str, subject: Optional[str]) -> None:
        async with db_session() as db:
            from sqlalchemy import select
            result = await db.execute(select(DecisionModel).where(DecisionModel.id == decision_id))
            dec = result.scalar_one_or_none()
            if dec:
                dec.draft_text = draft_text
                dec.subject = subject

    async def send_draft(self, draft_id: str) -> Dict[str, Any]:
        """Send an approved draft via Ingestion API."""
        async with db_session() as db:
            from sqlalchemy import select
            result = await db.execute(select(DecisionModel).where(DecisionModel.id == draft_id))
            decision = result.scalar_one_or_none()
            if not decision:
                return {"error": "Draft not found"}

        import os, httpx
        ingestion_url = os.getenv("INGESTION_URL", "http://localhost:8080")
        try:
            async with httpx.AsyncClient(timeout=30.0) as client:
                resp = await client.post(
                    f"{ingestion_url}/api/v1/send",
                    json={
                        "user_id": decision.user_id,
                        "to": decision.to_address or "",
                        "subject": decision.subject or "",
                        "body": decision.draft_text or "",
                        "thread_id": decision.thread_id,
                        "account_id": decision.account_id,
                    },
                )
                resp.raise_for_status()
                data = resp.json()
                async with db_session() as db:
                    from sqlalchemy import select
                    result = await db.execute(select(DecisionModel).where(DecisionModel.id == draft_id))
                    dec = result.scalar_one()
                    dec.sent_at = __import__("datetime").datetime.utcnow()
                    dec.sent_message_id = data.get("message_id")
                return {"success": True, "message_id": data.get("message_id")}
        except Exception as e:
            return {"success": False, "error": str(e)}

    async def _persist_draft(
        self,
        user_id: str,
        card_id: str,
        action_type: str,
        draft_text: str,
        to_address: Optional[str] = None,
        subject: Optional[str] = None,
        thread_id: Optional[str] = None,
        account_id: Optional[str] = None,
    ) -> None:
        async with db_session() as db:
            from datetime import datetime
            from intelligence.app.models import DecisionModel
            decision = DecisionModel(
                id=str(__import__("uuid").uuid4()),
                user_id=user_id,
                card_id=card_id,
                action_type=action_type,
                draft_text=draft_text,
                to_address=to_address,
                subject=subject,
                thread_id=thread_id,
                account_id=account_id,
                created_at=datetime.utcnow(),
            )
            db.add(decision)

    def _default_instruction(self, action: str) -> str:
        mapping = {
            "reply": "Draft a polite, helpful reply.",
            "approve": "Draft a brief approval confirmation.",
            "reject": "Draft a polite rejection.",
            "request_info": "Draft a request for additional information.",
            "delegate": "Draft a hand-off message to the delegatee.",
        }
        return mapping.get(action, "Draft an appropriate response.")
