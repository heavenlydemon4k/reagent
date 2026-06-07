"""Drafting engine — generates email replies for user approval."""

from typing import Optional, Dict, Any, List

from intelligence.core import FallbackChain
from intelligence.app.email_kb.service import EmailKnowledgeBase, EmailContext
from intelligence.app.profile.service import ProfileService


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

    def draft_reply(
        self,
        user_id: str,
        email_id: str,
        decision_action: str,
        user_instruction: Optional[str] = None,
        thread_context: Optional[List[EmailContext]] = None,
    ) -> Dict[str, Any]:
        profile = self.profile.get_or_create(user_id) if self.profile else None
        if thread_context is None:
            thread_context = self.kb.thread_context(email_id)
        email = thread_context[0] if thread_context else None
        if not email:
            raise ValueError(f"Email {email_id} not found")

        context_str = self.kb.summarize_for_agent(thread_context)
        tone = profile.agent_tone if profile else "professional"
        name = profile.agent_name if profile else "Reagent"
        suffix = profile.system_prompt_suffix if profile else ""
        instruction = user_instruction or self._default_instruction(decision_action)

        prompt = f"""You are {name}. Tone: {tone}. {suffix}

You are drafting an email reply on behalf of the user. Do not send it yet — the user will review first.

Thread context:
{context_str}

User instruction: {instruction}

Draft a concise, natural email reply. Include a subject line prefixed with "Subject: ".

Return ONLY the draft text. No JSON, no markdown, no explanation."""

        response = self.llm.route(prompt, complexity="complex")
        draft_text = response.text.strip()
        subject = email.subject
        if draft_text.startswith("Subject:"):
            parts = draft_text.split("\n", 1)
            subject = parts[0].replace("Subject:", "").strip()
            draft_text = parts[1].strip() if len(parts) > 1 else draft_text

        return {
            "draft_text": draft_text,
            "subject": subject,
            "to": email.from_address,
            "source_email_id": email_id,
            "cost_usd": response.meter.total_cost_usd,
            "model": response.model,
        }

    def draft_forward(
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
        profile = self.profile.get_or_create(user_id) if self.profile else None
        tone = profile.agent_tone if profile else "professional"
        name = profile.agent_name if profile else "Reagent"

        prompt = f"""You are {name}. Tone: {tone}.

Draft a forwarding email to {forward_to}. Include the original email below your note.

Original subject: {email.subject}
Original from: {email.from_address}

{f"User note: {note}" if note else ""}

Return ONLY the draft text."""

        response = self.llm.route(prompt, complexity="complex")
        return {
            "draft_text": response.text.strip(),
            "subject": f"Fwd: {email.subject}",
            "to": forward_to,
            "source_email_id": email_id,
            "cost_usd": response.meter.total_cost_usd,
            "model": response.model,
        }

    def _default_instruction(self, action: str) -> str:
        mapping = {
            "reply": "Draft a polite, helpful reply.",
            "approve": "Draft a brief approval confirmation.",
            "reject": "Draft a polite rejection.",
            "request_info": "Draft a request for additional information.",
            "delegate": "Draft a hand-off message to the delegatee.",
        }
        return mapping.get(action, "Draft an appropriate response.")
