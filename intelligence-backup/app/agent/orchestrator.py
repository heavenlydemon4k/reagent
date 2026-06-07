"""Agent orchestrator — routes user intent between chat, KB, decision stack, and drafting."""

import json
import re
from typing import Optional, Dict, Any, List

from intelligence.core import FallbackChain
from intelligence.app.email_kb.service import EmailKnowledgeBase
from intelligence.app.decision_stack.service import DecisionStackService, DecisionCard
from intelligence.app.drafting.service import DraftingService
from intelligence.app.profile.service import ProfileService
from intelligence.app.chat.session import SessionManager, ChatSession


class AgentOrchestrator:
    """Central brain. Receives user messages, determines intent, coordinates subsystems."""

    def __init__(
        self,
        llm: Optional[FallbackChain] = None,
        kb: Optional[EmailKnowledgeBase] = None,
        stack: Optional[DecisionStackService] = None,
        drafting: Optional[DraftingService] = None,
        profile: Optional[ProfileService] = None,
    ):
        self.llm = llm or FallbackChain()
        self.kb = kb or EmailKnowledgeBase()
        self.stack = stack or DecisionStackService(kb=self.kb)
        self.drafting = drafting or DraftingService(llm=self.llm, kb=self.kb, profile=profile)
        self.profile = profile

    async def handle_message(
        self,
        user_id: str,
        session_id: str,
        content: str,
        session_manager: SessionManager,
    ) -> Dict[str, Any]:
        intent = self._classify_intent(content)

        if intent == "decision_action":
            return await self._handle_decision_action(user_id, session_id, content, session_manager)
        elif intent == "kb_query":
            return await self._handle_kb_query(user_id, session_id, content, session_manager)
        elif intent == "draft_request":
            return await self._handle_draft_request(user_id, session_id, content, session_manager)
        elif intent == "stack_control":
            return await self._handle_stack_control(user_id, session_id, content, session_manager)
        else:
            return await self._handle_general_chat(user_id, session_id, content, session_manager)

    async def handle_card_action(
        self,
        user_id: str,
        session_id: str,
        card_id: str,
        action_id: str,
        payload: Optional[dict],
        session_manager: SessionManager,
    ) -> Dict[str, Any]:
        """Handle card actions: send, edit, discard from preview cards."""
        if action_id == "send":
            return await self._handle_send_approval(user_id, session_id, card_id, session_manager)
        elif action_id == "edit":
            return await self._handle_edit_request(user_id, session_id, card_id, payload, session_manager)
        elif action_id == "discard":
            return await self._handle_discard(user_id, session_id, card_id, session_manager)
        else:
            return self._agent_response(session_id, f"Action '{action_id}' not recognized.", session_manager)

    def _classify_intent(self, content: str) -> str:
        text = content.lower().strip()
        decision_actions = ["reply", "approve", "reject", "archive", "forward", "delegate", "snooze", "request info"]
        if any(text.startswith(a) for a in decision_actions):
            return "decision_action"
        stack_commands = ["start stack", "begin stack", "work through emails", "let\'s go", "pause", "resume", "next card", "skip"]
        if any(cmd in text for cmd in stack_commands):
            return "stack_control"
        draft_signals = ["draft", "write", "compose", "prepare a", "create a reply"]
        if any(s in text for s in draft_signals):
            return "draft_request"
        kb_signals = [
            "what did", "did anyone", "show me", "find", "search", "look for",
            "from", "about", "regarding", "last email", "unread", "recent",
        ]
        if any(s in text for s in kb_signals):
            return "kb_query"
        return "general_chat"

    async def _handle_kb_query(self, user_id, session_id, content, session_manager):
        search_results = self.kb.semantic_search(user_id, content, limit=5)
        if not search_results:
            return self._agent_response(session_id, "I couldn\'t find any emails matching that query.", session_manager)

        context = self.kb.summarize_for_agent(search_results)
        profile = self.profile.get_or_create(user_id) if self.profile else None
        tone = profile.agent_tone if profile else "professional"
        name = profile.agent_name if profile else "Reagent"

        prompt = f"""You are {name}. Tone: {tone}.

The user asked: "{content}"

Here are the relevant emails from their knowledgebase:
{context}

Answer their question directly. Reference specific emails by ID when making claims. Include a [Source] marker after each claim.

Format: Your answer here [Source: email_id]."""

        response = self.llm.route(prompt, complexity="complex")
        text = response.text.strip()
        source_ids = self._extract_source_ids(text)

        return self._agent_response(
            session_id, text, session_manager,
            source_email_ids=source_ids, cost=response.meter.total_cost_usd
        )

    async def _handle_decision_action(self, user_id, session_id, content, session_manager):
        active_card = await self.stack.get_active(session_id)
        if not active_card:
            return self._agent_response(
                session_id, "No active decision card. Start a stack session first.", session_manager
            )

        action = self._parse_action(content)

        if action in ["archive", "snooze"]:
            await self.stack.resolve_card(active_card.id, action)
            return self._agent_response(
                session_id,
                f"Done. I\'ve {action}ed the email from {active_card.source_email[\'from\']}.",
                session_manager,
                card_resolved={"card_id": active_card.id, "action": action},
            )

        draft = self.drafting.draft_reply(
            user_id=user_id,
            email_id=active_card.email_id,
            decision_action=action,
            user_instruction=content if len(content) > 10 else None,
        )

        preview_payload = {
            "type": "card",
            "card_type": "confirm",
            "title": f"Draft: {active_card.title}",
            "body": draft["draft_text"],
            "source_email_id": active_card.email_id,
            "options": [
                {"id": "send", "label": "Send", "style": "primary"},
                {"id": "edit", "label": "Edit", "style": "default"},
                {"id": "discard", "label": "Discard", "style": "danger"},
            ],
            "metadata": {
                "card_id": active_card.id,
                "draft": draft,
                "preview": True,
            },
        }

        return self._agent_response(
            session_id,
            "Here\'s the draft. Review before sending.",
            session_manager,
            card_payload=preview_payload,
            cost=draft["cost_usd"],
        )

    async def _handle_stack_control(self, user_id, session_id, content, session_manager):
        text = content.lower()

        if any(cmd in text for cmd in ["start", "begin", "let\'s go", "work through"]):
            card = await self.stack.activate_next(user_id, session_id)
            if not card:
                return self._agent_response(
                    session_id, "Your decision stack is empty. No critical emails right now.", session_manager
                )
            payload = self.stack.to_message_payload(card)
            stack_len = await self.stack.stack_length(user_id)
            return self._agent_response(
                session_id,
                f"Starting stack. Card 1 of {stack_len}:",
                session_manager,
                card_payload=payload,
            )

        if "pause" in text:
            return self._agent_response(
                session_id, "Stack paused. Say \'resume\' when you\'re ready to continue.", session_manager
            )

        if "next" in text or "skip" in text:
            active = await self.stack.get_active(session_id)
            if active:
                await self.stack.resolve_card(active.id, "skipped")
            card = await self.stack.activate_next(user_id, session_id)
            if not card:
                return self._agent_response(
                    session_id, "Stack complete. No more critical emails.", session_manager, stack_complete=True
                )
            payload = self.stack.to_message_payload(card)
            return self._agent_response(session_id, "Next card:", session_manager, card_payload=payload)

        return self._agent_response(session_id, "Stack command not recognized.", session_manager)

    async def _handle_draft_request(self, user_id, session_id, content, session_manager):
        return self._agent_response(
            session_id,
            "To draft a reply, either work through your decision stack or reference a specific email by ID.",
            session_manager,
        )

    async def _handle_general_chat(self, user_id, session_id, content, session_manager):
        session = session_manager.get(session_id)
        history = session.messages[-10:] if session else []
        history_str = "\n".join([f"{m[\'role\']}: {m[\'content\']}" for m in history])

        profile = self.profile.get_or_create(user_id) if self.profile else None
        tone = profile.agent_tone if profile else "professional"
        name = profile.agent_name if profile else "Reagent"
        suffix = profile.system_prompt_suffix if profile else ""

        prompt = f"""You are {name}. Tone: {tone}. {suffix}

You are an email agent assistant. You have access to the user\'s inbox but they haven\'t asked a specific question about it right now.

Recent chat history:
{history_str}

User: {content}
Agent:"""

        response = self.llm.route(prompt, complexity="auto")
        return self._agent_response(
            session_id, response.text.strip(), session_manager, cost=response.meter.total_cost_usd
        )

    async def _handle_send_approval(self, user_id, session_id, card_id, session_manager):
        """User clicked Send on a preview card. Send via Ingestion, mark resolved, next card."""
        from intelligence.app.decision_stack.service import DecisionStackService
        stack = DecisionStackService(kb=self.kb)
        result = await stack.send_and_resolve(card_id, user_id)
        if result.get("error"):
            return self._agent_response(session_id, f"Send failed: {result[\'error\']}", session_manager)

        # Next card
        card = await stack.activate_next(user_id, session_id)
        if not card:
            return self._agent_response(
                session_id, "Sent. Stack complete — no more critical emails.", session_manager, stack_complete=True
            )
        payload = stack.to_message_payload(card)
        return self._agent_response(
            session_id, "Sent. Next card:", session_manager, card_payload=payload
        )

    async def _handle_edit_request(self, user_id, session_id, card_id, payload, session_manager):
        """User wants to edit a draft."""
        edit_text = (payload or {}).get("edit_text", "")
        if not edit_text:
            return self._agent_response(session_id, "No edit text provided.", session_manager)
        draft = self.drafting.edit_draft(card_id, edit_text)
        preview_payload = {
            "type": "card",
            "card_type": "confirm",
            "title": "Edited Draft",
            "body": draft["draft_text"],
            "options": [
                {"id": "send", "label": "Send", "style": "primary"},
                {"id": "edit", "label": "Edit", "style": "default"},
                {"id": "discard", "label": "Discard", "style": "danger"},
            ],
            "metadata": {"card_id": card_id, "draft": draft, "preview": True},
        }
        return self._agent_response(session_id, "Updated draft:", session_manager, card_payload=preview_payload)

    async def _handle_discard(self, user_id, session_id, card_id, session_manager):
        """User discarded a draft. Return to decision card."""
        from intelligence.app.decision_stack.service import DecisionStackService
        stack = DecisionStackService(kb=self.kb)
        card = await stack.get_card_by_id(card_id)
        if not card:
            return self._agent_response(session_id, "Card not found.", session_manager)
        # Reactivate the card
        card.status = "active"
        payload = stack.to_message_payload(card)
        return self._agent_response(
            session_id, "Draft discarded. Here\'s the card again:", session_manager, card_payload=payload
        )

    def _agent_response(
        self,
        session_id: str,
        text: str,
        session_manager: SessionManager,
        source_email_ids: Optional[List[str]] = None,
        card_payload: Optional[Dict] = None,
        card_resolved: Optional[Dict] = None,
        stack_complete: bool = False,
        cost: Optional[float] = None,
    ) -> Dict[str, Any]:
        msg = session_manager.add_message(
            session_id, "agent", text,
            message_type="card" if card_payload else "text",
            card_data=card_payload,
        )
        result = {
            "session_id": session_id,
            "message": msg,
            "source_email_ids": source_email_ids or [],
            "stack_complete": stack_complete,
        }
        if cost is not None:
            result["cost_usd"] = cost
        if card_resolved:
            result["card_resolved"] = card_resolved
        return result

    def _extract_source_ids(self, text: str) -> List[str]:
        matches = re.findall(r"\[Source:\s*([a-f0-9\-]+)\]", text)
        return matches

    def _parse_action(self, content: str) -> str:
        text = content.lower().strip()
        if text.startswith("approve"): return "approve"
        if text.startswith("reject"): return "reject"
        if text.startswith("reply"): return "reply"
        if text.startswith("forward"): return "forward"
        if text.startswith("delegate"): return "delegate"
        if text.startswith("archive"): return "archive"
        if text.startswith("snooze"): return "snooze"
        if text.startswith("request"): return "request_info"
        return "reply"
