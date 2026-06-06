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


class ChatService:
    """Persistent conversational interface with cross-thread context."""

    def __init__(
        self,
        chain: FallbackChain,
        retriever: ContextRetriever,
        history: ConversationHistory,
        neo4j_client=None,
        redis_client=None,
    ) -> None:
        self.chain = chain
        self.retriever = retriever
        self.history = history
        self.neo4j = neo4j_client
        self.redis = redis_client

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

        # 7. Extract suggested action (if any)
        action = self._detect_action(result.text)

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

        # Calendar context
        events = context.get("events", [])
        if events:
            lines.append("\nUpcoming events:")
            for e in events[:3]:
                lines.append(
                    f"- {e.get('title', 'Untitled')} @ "
                    f"{e.get('start_time', 'TBD')}"
                )

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
