"""
Chat Service — persistent conversational interface with cross-thread context.

Unlike Consultation (scoped to a single card, max 10 turns), Chat provides
ongoing persistent conversations that can draw context from the full user
graph: relationships, threads, calendar, and linked cards.
"""

from __future__ import annotations

import logging
import re
import time
from datetime import datetime, timedelta
from typing import Any, AsyncIterator, Dict, List, Optional

from intelligence.app.chat.history import ConversationHistory
from intelligence.app.chat.models import (
    ChatMessage,
    ChatResponse,
    Conversation,
)
from intelligence.app.chat.retriever import ContextRetriever
from intelligence.core.fallback_chain import FallbackChain
from intelligence.core.llm_client import COST_TABLE, GenerationResult, compute_cost

# Optional import — calendar tools are only available when calendar service is configured
try:
    from intelligence.app.calendar_context.service import CalendarContextService
except Exception:
    CalendarContextService = None  # type: ignore[misc,assignment]

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Query complexity classifier — regex-based heuristics
# ---------------------------------------------------------------------------
#
# Score-based classification with pattern matching.
# Complex patterns override simple ones: if a query matches ANY complex
# pattern it is always routed to Sonnet (quality), even if it also matches
# a simple pattern.
#
# Simple queries (factual lookup, summarization, listing)
#   → Haiku via streaming   (target: first token <1s, full <2.5s)
# Complex queries (reasoning, strategy, drafting, negotiation)
#   → Sonnet non-streaming   (target: full response <5s)

_SIMPLE_PATTERNS: List[re.Pattern] = [
    # Question starters (factual)
    re.compile(
        r"^(what|when|who|where|did|does|is|was|were|has|have|had"
        r"|can|could|will|would|shall|should|may|might)\b",
        re.IGNORECASE,
    ),
    # Action keywords (information retrieval)
    re.compile(
        r"^\b(summarize|list|show|tell me|find|get|look up|search for)\b",
        re.IGNORECASE,
    ),
    # Reporting verbs (who said what)
    re.compile(
        r"\b(say|said|mention|mentioned|tell|ask|asked)\b",
        re.IGNORECASE,
    ),
]

_COMPLEX_PATTERNS: List[re.Pattern] = [
    # Reasoning / analysis verbs
    re.compile(
        r"\b(why|how should|how would|how do|how can|how to"
        r"|plan|strategy|strategize|compare|analyse|analyze|analysis|evaluate"
        r"|recommend|suggest)\b",
        re.IGNORECASE,
    ),
    # Business / negotiation language
    re.compile(
        r"\b(negotiate|negotiation|pricing|price|cost|budget"
        r"|proposal|contract|deal|terms)\b",
        re.IGNORECASE,
    ),
    # Drafting / composition requests
    re.compile(
        r"\b(draft|write|compose|create|generate|prepare)\s+"
        r"(?:an?\s+)?(?:email|message|reply|response)\b",
        re.IGNORECASE,
    ),
    # Advice / opinion seeking
    re.compile(
        r"\b(should I|what if|consider|think about|advice|opinion)\b",
        re.IGNORECASE,
    ),
]


def classify_query_complexity(message: str) -> str:
    """Classify a chat message as 'simple' or 'complex'.

    Simple: factual lookup, summarization, listing — Haiku (fast, cheap)
    Complex: reasoning, strategy, drafting, negotiation — Sonnet (quality)

    Complex patterns override simple ones (safety-first default).
    If no pattern matches, defaults to complex.

    Args:
        message: The raw user message text.

    Returns:
        'simple' or 'complex'.
    """
    simple_score = sum(1 for p in _SIMPLE_PATTERNS if p.search(message))
    complex_score = sum(1 for p in _COMPLEX_PATTERNS if p.search(message))

    # Complex patterns override simple ones (quality gate)
    if complex_score > 0:
        return "complex"
    if simple_score > 0:
        return "simple"
    # Default to complex for safety on ambiguous queries
    return "complex"


# ---------------------------------------------------------------------------
# Structured tool definitions for calendar & send actions
# ---------------------------------------------------------------------------

CALENDAR_TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "get_calendar_events",
            "description": "Get the user's calendar events for a date range",
            "parameters": {
                "type": "object",
                "properties": {
                    "days": {"type": "integer", "description": "Number of days to look ahead", "default": 7}
                }
            }
        }
    },
    {
        "type": "function",
        "function": {
            "name": "check_free_slots",
            "description": "Find free time slots on a specific date",
            "parameters": {
                "type": "object",
                "properties": {
                    "date": {"type": "string", "description": "Date in YYYY-MM-DD format"},
                    "duration_minutes": {"type": "integer", "description": "Minimum slot duration", "default": 30}
                },
                "required": ["date"]
            }
        }
    },
    {
        "type": "function",
        "function": {
            "name": "create_calendar_event",
            "description": "Create a new calendar event",
            "parameters": {
                "type": "object",
                "properties": {
                    "title": {"type": "string"},
                    "start_time": {"type": "string", "description": "ISO 8601 datetime"},
                    "end_time": {"type": "string", "description": "ISO 8601 datetime"},
                    "attendees": {"type": "array", "items": {"type": "string"}, "description": "Email addresses"}
                },
                "required": ["title", "start_time", "end_time"]
            }
        }
    },
    {
        "type": "function",
        "function": {
            "name": "send_draft",
            "description": "Send an approved email draft immediately",
            "parameters": {
                "type": "object",
                "properties": {
                    "draft_id": {"type": "string"}
                },
                "required": ["draft_id"]
            }
        }
    }
]


class ChatService:
    """Persistent conversational interface with cross-thread context."""

    def __init__(
        self,
        chain: FallbackChain,
        retriever: ContextRetriever,
        history: ConversationHistory,
        neo4j_client=None,
        redis_client=None,
        calendar_service: Optional[Any] = None,
    ) -> None:
        self.chain = chain
        self.retriever = retriever
        self.history = history
        self.neo4j = neo4j_client
        self.redis = redis_client
        self.calendar_service = calendar_service

    # ------------------------------------------------------------------
    # Query complexity router
    # ------------------------------------------------------------------

    @staticmethod
    def _classify_complexity(message: str) -> str:
        """
        Classify a user message as 'simple' or 'complex'.

        Delegates to the module-level ``classify_query_complexity`` so the
        heuristic can be shared between the service and the router layer.

        Args:
            message: The user's message text.

        Returns:
            'simple' or 'complex'.
        """
        return classify_query_complexity(message)

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def send_message(
        self,
        user_id: str,
        message: str,
        conversation_id: Optional[str] = None,
        linked_card_id: Optional[str] = None,
    ) -> ChatResponse:
        """
        Send a message in a conversation and get an assistant response.

        Pipeline:
            1. Get or create conversation.
            2. Save user message.
            3. Retrieve cross-source context.
            4. Classify query complexity + Redis pre-fetch (complex only).
            5. Build prompt with history + context.
            6. Generate response via LLM (routed by complexity).
            7. Extract suggested action (if any).
            8. Save assistant message.
            9. Return ChatResponse.

        Args:
            user_id: Multi-tenancy user identifier.
            conversation_id: Existing conversation UUID, or None to start new.
            message: The user's message text.
            linked_card_id: Optional card to scope context exclusively.

        Returns:
            ChatResponse with assistant message, citations, and suggested action.
        """
        t0 = time.perf_counter()

        # 1. Get or create conversation
        conv = await self.history.get_or_create(user_id, conversation_id)

        # 2. Save user message
        user_msg = ChatMessage(
            conversation_id=conv.id,
            role="user",
            content=message,
        )
        await self.history.add_message(user_msg)

        # 3. Retrieve context
        try:
            context = await self.retriever.retrieve(
                user_id=user_id,
                conversation=conv,
                message=message,
                linked_card_id=linked_card_id,
            )
        except Exception as exc:
            logger.error("Context retrieval failed: %s", exc, exc_info=True)
            context = {"contacts": [], "threads": [], "events": [], "citations": [], "chunks": []}

        # 4. Classify query complexity early (needed for routing + Redis pre-fetch)
        complexity = classify_query_complexity(message)

        # 4b. For complex queries: pre-fetch thread summary from Redis cache
        # to reduce LLM work and improve Sonnet response quality
        if complexity == "complex" and self.redis is not None:
            try:
                # Derive a thread-like key from conversation id
                conv_key = str(conv.id)
                cached_summary = await self.redis.get(f"thread_summary:{conv_key}")
                if cached_summary:
                    summary_text = (
                        cached_summary.decode("utf-8")
                        if isinstance(cached_summary, bytes)
                        else str(cached_summary)
                    )
                    context["thread_summary"] = summary_text
                    logger.debug(
                        "Redis cache hit: thread_summary for conv=%s (len=%d)",
                        conv_key,
                        len(summary_text),
                    )
            except Exception as exc:
                logger.warning("Redis pre-fetch failed (non-blocking): %s", exc)

        # 5. Build prompt with conversation history + context
        prompt = self._build_chat_prompt(conv, context, message)

        # 6. Generate response via LLM (routed by query complexity)
        #    complexity was computed in step 4 so we can pre-fetch Redis
        result: GenerationResult

        try:
            if complexity == "simple":
                # Route to fast/cheap fallback model (Haiku) via streaming
                logger.debug(
                    "Routing simple query to fallback model for user=%s", user_id
                )
                result = await self._generate_simple(prompt, user_id)
            else:
                # Route to primary model (Sonnet) via full fallback chain
                logger.debug(
                    "Routing complex query through fallback chain for user=%s", user_id
                )
                result = await self.chain.generate(
                    prompt=prompt,
                    system=self._system_prompt(),
                    temperature=0.4,
                    max_tokens=2000,
                    user_id=user_id,
                )
        except Exception as exc:
            logger.error("LLM generation failed: %s", exc, exc_info=True)
            latency_ms = int((time.perf_counter() - t0) * 1000)
            error_msg = ChatMessage(
                conversation_id=conv.id,
                role="assistant",
                content="I'm sorry, I encountered an error. Please try again.",
            )
            await self.history.add_message(error_msg)
            return ChatResponse(
                message=error_msg,
                conversation_id=conv.id,
                conversation_title=conv.title,
                latency_ms=latency_ms,
            )

        # 7. Extract suggested action and execute any tool calls
        action = self._detect_action(result.text)

        # 7b. Detect and execute structured tool calls
        tool_call = self._parse_tool_call(result.text)
        if tool_call:
            tool_result = await self._execute_tool(
                tool_call["name"],
                tool_call.get("arguments", {}),
                user_id,
            )
            # Append tool result to the response text
            result.text = (
                f"{result.text}\n\n"
                f"[Tool result: {tool_call['name']}]\n{tool_result}"
            )
            logger.info(
                "Executed tool %s for user=%s", tool_call["name"], user_id
            )

        # 8. Save assistant message
        citations = context.get("citations", [])
        assistant_msg = ChatMessage(
            conversation_id=conv.id,
            role="assistant",
            content=result.text,
            citations=citations,
            model_used=result.model or self.chain.primary.model_name,
            tokens_used=result.total_tokens,
        )
        await self.history.add_message(assistant_msg)

        latency_ms = int((time.perf_counter() - t0) * 1000)

        logger.info(
            "Chat response generated conv=%s complexity=%s model=%s latency=%dms tokens=%d cost=%.6f",
            conv.id,
            complexity,
            result.model or "unknown",
            latency_ms,
            result.total_tokens,
            result.cost_usd,
        )

        # 9. Return ChatResponse
        return ChatResponse(
            message=assistant_msg,
            conversation_id=conv.id,
            conversation_title=conv.title,
            suggested_action=action,
            citations=citations,
            tokens_input=result.tokens_input,
            tokens_output=result.tokens_output,
            latency_ms=latency_ms,
        )

    async def get_conversation(
        self,
        conversation_id: str,
        user_id: str,
    ) -> Optional[Conversation]:
        """Load a conversation with ownership check."""
        conv = await self.history.get_conversation(conversation_id)
        if conv is None:
            return None
        if str(conv.user_id) != user_id:
            logger.warning(
                "User %s attempted to access conversation %s owned by %s",
                user_id,
                conversation_id,
                conv.user_id,
            )
            return None
        return conv

    async def list_conversations(self, user_id: str) -> list:
        """List all conversations for a user."""
        return await self.history.list_conversations(user_id)

    # ------------------------------------------------------------------
    # Internal generation helpers
    # ------------------------------------------------------------------

    async def _generate_simple(
        self,
        prompt: str,
        user_id: Optional[str] = None,
    ) -> GenerationResult:
        """
        Generate a response for a simple query using the fast/cheap fallback.

        Uses generate_stream with preferred_model='fallback' (Haiku) for low
        latency and cost.  Collects streamed chunks and estimates token counts
        via a rough character heuristic so the rest of the pipeline receives a
        fully populated GenerationResult.

        Args:
            prompt: The fully built prompt (history + context + message).
            user_id: Optional user identifier for metering.

        Returns:
            GenerationResult with estimated token counts and cost.
        """
        # Collect streamed text chunks
        text_chunks: List[str] = []
        async for chunk in self.chain.generate_stream(
            prompt=prompt,
            system=self._system_prompt(),
            temperature=0.4,
            max_tokens=2000,
            user_id=user_id,
            preferred_model="fallback",
        ):
            text_chunks.append(chunk)

        text = "".join(text_chunks)

        # Estimate token counts (rough heuristic: ~4 chars/token)
        estimated_input_tokens = len(prompt) // 4
        estimated_output_tokens = max(len(text) // 4, 1)
        fallback_model = self.chain.fallback.model_name
        cost = compute_cost(
            fallback_model,
            estimated_input_tokens,
            estimated_output_tokens,
        )

        return GenerationResult(
            text=text,
            model=fallback_model,
            tokens_input=estimated_input_tokens,
            tokens_output=estimated_output_tokens,
            cost_usd=cost,
        )

    # ------------------------------------------------------------------
    # Streaming helpers (SSE)
    # ------------------------------------------------------------------

    async def stream_response(
        self,
        user_id: str,
        conversation_id: Optional[str],
        message: str,
        linked_card_id: Optional[str] = None,
    ) -> AsyncIterator[str]:
        """
        Stream an assistant response via SSE for simple queries.

        Pipeline mirrors ``send_message`` but yields token chunks instead of
        returning a single ChatResponse.  After the stream completes the
        assistant message is persisted so the conversation remains consistent.

        Args:
            user_id: Multi-tenancy user identifier.
            conversation_id: Existing conversation UUID, or None to start new.
            message: The user's message text.
            linked_card_id: Optional card to scope context exclusively.

        Yields:
            SSE-formatted text chunks (``data: <chunk>\\n\\n``).
            The first chunk is a JSON metadata line with the model name.
            The final chunk is a JSON metadata line with usage stats.
        """
        t0 = time.perf_counter()

        # 1. Get or create conversation
        conv = await self.history.get_or_create(user_id, conversation_id)

        # 2. Save user message
        user_msg = ChatMessage(
            conversation_id=conv.id,
            role="user",
            content=message,
        )
        await self.history.add_message(user_msg)

        # 3. Retrieve context
        try:
            context = await self.retriever.retrieve(
                user_id=user_id,
                conversation=conv,
                message=message,
                linked_card_id=linked_card_id,
            )
        except Exception as exc:
            logger.error("Context retrieval failed (stream): %s", exc, exc_info=True)
            context = {"contacts": [], "threads": [], "events": [], "citations": [], "chunks": []}

        # 4. Build prompt
        prompt = self._build_chat_prompt(conv, context, message)

        # 5. Stream response from Haiku (fastest model)
        fallback_model = self.chain.fallback.model_name
        text_chunks: List[str] = []

        # Emit model info as first SSE event
        yield f'data: {{"event": "model", "model": "{fallback_model}"}}\n\n'

        try:
            async for chunk in self.chain.generate_stream(
                prompt=prompt,
                system=self._system_prompt(),
                temperature=0.4,
                max_tokens=2000,
                user_id=user_id,
                preferred_model="fallback",
            ):
                text_chunks.append(chunk)
                yield f"data: {chunk}\n\n"
        except Exception as exc:
            logger.error("Streaming generation failed: %s", exc, exc_info=True)
            error_text = "I'm sorry, I encountered an error while streaming. Please try again."
            yield f"data: {error_text}\n\n"
            text_chunks.append(error_text)

        # 6. Persist assistant message so conversation history is consistent
        text = "".join(text_chunks)
        citations = context.get("citations", [])
        estimated_input_tokens = len(prompt) // 4
        estimated_output_tokens = max(len(text) // 4, 1)
        action = self._detect_action(text)

        # 6b. Detect and execute structured tool calls in streamed response
        tool_call = self._parse_tool_call(text)
        if tool_call:
            tool_result = await self._execute_tool(
                tool_call["name"],
                tool_call.get("arguments", {}),
                user_id,
            )
            tool_result_line = (
                f"\n\n[Tool result: {tool_call['name']}]\n{tool_result}"
            )
            text += tool_result_line
            yield f"data: {tool_result_line}\n\n"
            logger.info(
                "Executed tool %s in stream for user=%s",
                tool_call["name"],
                user_id,
            )

        assistant_msg = ChatMessage(
            conversation_id=conv.id,
            role="assistant",
            content=text,
            citations=citations,
            model_used=fallback_model,
            tokens_used=estimated_output_tokens,
        )
        await self.history.add_message(assistant_msg)

        latency_ms = int((time.perf_counter() - t0) * 1000)

        # Emit final metadata event
        yield f'data: {{"event": "done", "latency_ms": {latency_ms}, "tokens_output": {estimated_output_tokens}, "model": "{fallback_model}"}}\n\n'

        logger.info(
            "Chat stream completed conv=%s model=%s latency=%dms tokens=%d",
            conv.id,
            fallback_model,
            latency_ms,
            estimated_output_tokens,
        )

    # ------------------------------------------------------------------
    # Prompting
    # ------------------------------------------------------------------

    def _system_prompt(self) -> str:
        """Return the system prompt for general chat."""
        return (
            "You are an expert executive assistant with deep knowledge of the user's "
            "email, relationships, and schedule. Answer helpfully using the provided "
            "context. When referencing emails, cite the sender and date. "
            "If you detect an actionable item (scheduling, delegation, follow-up), "
            "suggest it clearly at the end of your response using the format: "
            "[ACTION: action_name]. Possible actions: clear_batch, view_card, "
            "schedule, send_draft, add_contact, create_reminder. "
            "\n\nYou also have access to the following tools. "
            "To call a tool, output JSON inside <tool_call> tags like this:\n"
            '<tool_call>{"name": "tool_name", "arguments": {"key": "value"}}</tool_call>\n'
            "Available tools:\n"
            "- get_calendar_events(days=7): Get calendar events for the next N days\n"
            "- check_free_slots(date=YYYY-MM-DD, duration_minutes=30): Find free slots\n"
            "- create_calendar_event(title, start_time, end_time, attendees=[]): Create event\n"
            "- send_draft(draft_id): Send an approved draft immediately\n"
            "Be concise, professional, and proactive."
        )

    def _build_chat_prompt(
        self,
        conv: Conversation,
        context: Dict[str, Any],
        message: str,
    ) -> str:
        """
        Build a prompt from conversation history + cross-source context.

        Includes up to the last 10 messages for context window management.
        """
        lines: List[str] = []

        # Context header
        lines.append("=== Context ===")

        # Pre-fetched thread summary (injected for complex queries when cached)
        thread_summary = context.get("thread_summary")
        if thread_summary:
            lines.append(f"\nThread summary:\n{thread_summary}")

        # Contact context
        contacts = context.get("contacts", [])
        if contacts:
            lines.append("\nRelevant contacts:")
            for c in contacts[:5]:
                lines.append(f"- {c.get('name', 'Unknown')} ({c.get('email', 'N/A')}) — {c.get('company', '')}")

        # Thread/email context
        chunks = context.get("chunks", [])
        if chunks:
            lines.append("\nRelevant emails:")
            for i, chunk in enumerate(chunks[:5], 1):
                snippet = chunk.content_snippet or chunk.content[:250]
                lines.append(
                    f"[{i}] From: {chunk.sender_email} | "
                    f"{chunk.timestamp.strftime('%Y-%m-%d %H:%M')}\n"
                    f"    {snippet}"
                )

        # Calendar context — upcoming events (generic)
        events = context.get("events", [])
        if events:
            lines.append("\nUpcoming events:")
            for e in events[:3]:
                lines.append(
                    f"- {e.get('title', 'Untitled')} @ "
                    f"{e.get('start_time', 'TBD')}"
                )

        # Calendar context — scheduling-intent rich context (when detected)
        calendar_ctx = context.get("calendar")
        if calendar_ctx:
            lines.append(f"\n{calendar_ctx}")

        # Calendar conflicts for proposed times
        calendar_conflicts = context.get("calendar_conflicts")
        if calendar_conflicts:
            if calendar_conflicts.has_conflicts:
                lines.append("\n⚠️  CONFLICT WARNING for proposed time:")
                for c in calendar_conflicts.all_conflicts:
                    lines.append(f"   - {c.description}")
            else:
                lines.append("\n✅ No conflicts detected for the proposed time.")

        lines.append("\n=== Conversation ===")

        # Recent conversation history (last 10 messages)
        recent = conv.messages[-10:] if len(conv.messages) > 10 else conv.messages
        for msg in recent:
            prefix = "User" if msg.role == "user" else "Assistant"
            lines.append(f"{prefix}: {msg.content}")

        # Current message
        lines.append(f"User: {message}")
        lines.append("Assistant:")

        return "\n".join(lines)

    # ------------------------------------------------------------------
    # Action detection
    # ------------------------------------------------------------------

    def _detect_action(self, text: str) -> Optional[str]:
        """
        Detect a suggested action embedded in the assistant's response.

        Looks for [ACTION: action_name] pattern at the end of the text.
        Strips the action marker from the returned text is handled by
        the caller if needed.

        Returns:
            Action name string, or None.
        """
        match = re.search(r"\[ACTION:\s*([a-z_]+)\]", text, re.IGNORECASE)
        if match:
            action = match.group(1).lower()
            logger.debug("Detected suggested action: %s", action)
            return action
        return None

    # ------------------------------------------------------------------
    # Structured tool execution
    # ------------------------------------------------------------------

    async def _execute_tool(
        self,
        tool_name: str,
        tool_params: dict,
        user_id: str,
    ) -> str:
        """Execute a calendar/send tool and return the result."""
        if not self.calendar_service:
            return "Calendar service not available"

        if tool_name == "get_calendar_events":
            days = tool_params.get("days", 7)
            events = await self.calendar_service.get_events_next_7_days(user_id)
            return f"Found {len(events)} events in the next {days} days"

        elif tool_name == "check_free_slots":
            date_str = tool_params["date"]
            duration = timedelta(minutes=tool_params.get("duration_minutes", 30))
            target = datetime.strptime(date_str, "%Y-%m-%d").date()
            result = await self.calendar_service.get_free_slots(user_id, target, duration)
            slots_str = "\n".join(
                [
                    f"{s.start.strftime('%H:%M')}-{s.end.strftime('%H:%M')}"
                    for s in result.slots[:5]
                ]
            )
            return f"Free slots on {date_str}:\n{slots_str}"

        elif tool_name == "create_calendar_event":
            # Return instructions for event creation
            return (
                f"Event '{tool_params['title']}' "
                f"scheduled for {tool_params['start_time']}"
            )

        elif tool_name == "send_draft":
            # Return confirmation that draft will be sent
            return f"Draft {tool_params['draft_id']} queued for sending"

        return f"Unknown tool: {tool_name}"

    def _parse_tool_call(self, text: str) -> Optional[dict]:
        """Parse a tool call from LLM response text.

        Looks for JSON inside <tool_call> tags:
            <tool_call>{"name": "...", "arguments": {...}}</tool_call>

        Returns:
            Dict with 'name' and 'arguments' keys, or None.
        """
        import json

        match = re.search(
            r'<tool_call>\s*(\{.*?\})\s*</tool_call>', text, re.DOTALL
        )
        if match:
            try:
                payload = json.loads(match.group(1))
                if "name" in payload and "arguments" in payload:
                    logger.debug("Parsed tool call: %s", payload["name"])
                    return payload
            except json.JSONDecodeError:
                pass
        return None
