"""
Drafting Service — Main Orchestrator

Transforms a user's one-line decision into a full, voice-calibrated email draft.

Pipeline (8 steps):
    1. Parse user intent (Claude 3 Haiku — fast, cheap)
    2. Check intent cache (Redis — <2s fast path for common intents)
    3. Retrieve voice examples (Qdrant top-3 with recency boost)
    4. Get relationship context (Neo4j — optional)
    5. Get thread context (PostgreSQL + chunk store)
    6. Build drafting prompt (Jinja2 template)
    7. Generate draft (Claude 3.5 Sonnet — quality, temp=0.4)
    8. Extract threading headers (ThreadingEngine)
    9. Return Draft with full metadata & provenance

Invariants:
    - Every draft cites the voice_examples used (provenance).
    - Threading headers are EXACT Message-ID matches.
    - User can always edit before approve (this service does NOT send).
    - Intent parsing via Haiku (fast), drafting via Sonnet (quality).
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import time
from pathlib import Path
from typing import Any, Dict, List, Optional
from uuid import UUID

import jinja2

from intelligence.app.drafting.intent_parser import IntentParser
from intelligence.app.drafting.models import (
    Draft,
    Intent,
    ThreadHeaders,
    VoiceExample,
    VoiceProfile,
)
from intelligence.app.drafting.threading import ThreadingEngine
from intelligence.app.drafting.voice_retriever import VoiceRetriever
from intelligence.core.fallback_chain import FallbackChain
from intelligence.core.llm_client import GenerationResult
from intelligence.core.redis_client import get_redis

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Default template path (overridable via constructor)
# ---------------------------------------------------------------------------

_DEFAULT_TEMPLATE_PATH = Path(
    __file__
).parent.parent.parent / "core" / "prompt_templates" / "drafting.jinja2"

# ---------------------------------------------------------------------------
# Pre-defined templates for common intents (cache warming targets)
# ---------------------------------------------------------------------------

PREDEFINED_TEMPLATES: Dict[str, str] = {
    "approve": (
        "Hi {contact_name},\n\n"
        "Thanks for sending this over — I'm happy to move forward. "
        "{approval_details}\n\n"
        "Let me know if you need anything else from my side.\n\n"
        "Best,\n{user_name}"
    ),
    "decline": (
        "Hi {contact_name},\n\n"
        "Thanks for thinking of me on this. After giving it some thought, "
        "I don't think this is the right fit for us right now. "
        "{decline_reason}\n\n"
        "I appreciate the opportunity and hope we can find something to "
        "collaborate on in the future.\n\n"
        "Best,\n{user_name}"
    ),
    "suggest_next_week": (
        "Hi {contact_name},\n\n"
        "Thanks for reaching out. I'd love to connect — how does sometime "
        "next week work for you? I'm generally free {availability_window}.\n\n"
        "Let me know what works best and I'll send over a calendar invite.\n\n"
        "Best,\n{user_name}"
    ),
    "send_calendar_link": (
        "Hi {contact_name},\n\n"
        "Thanks for your interest in meeting. Here's my calendar link: "
        "{calendar_link}\n\n"
        "Feel free to book any slot that works for you — looking forward to it.\n\n"
        "Best,\n{user_name}"
    ),
    "ask_for_more_info": (
        "Hi {contact_name},\n\n"
        "Thanks for this. Before I can give you a firm answer, could you "
        "share a bit more detail on {info_request}?\n\n"
        "Once I have that, I'll be able to get back to you quickly.\n\n"
        "Best,\n{user_name}"
    ),
}

# Similarity threshold for cache hit (>0.92 means "same enough")
_INTENT_CACHE_SIMILARITY_THRESHOLD: float = 0.92
# Cache TTL for draft templates: 24 hours
_DRAFT_TEMPLATE_CACHE_TTL: int = 86400


# ---------------------------------------------------------------------------
# DraftingService
# ---------------------------------------------------------------------------

class DraftingService:
    """Transforms user decisions into full, voice-calibrated email drafts.

    Usage::

        service = DraftingService(
            llm=fallback_chain,
            voice=voice_retriever,
            threading=threading_engine,
            intent_parser=IntentParser(fallback_chain),
        )
        draft = await service.draft(
            user_id="uuid",
            card_id="uuid",
            thread_id="uuid",
            user_input="9500, two weeks",
        )
    """

    def __init__(
        self,
        llm: FallbackChain,
        voice: VoiceRetriever,
        threading: ThreadingEngine,
        intent_parser: Optional[IntentParser] = None,
        template_path: Optional[Path] = None,
        neo4j_client: Optional[Any] = None,
        chunk_store: Optional[Any] = None,
        user_name: str = "",
        calendar_link: str = "",
    ) -> None:
        """Initialize the drafting service.

        Args:
            llm: FallbackChain with Sonnet primary / Haiku fallback.
            voice: VoiceRetriever for past email examples from Qdrant.
            threading: ThreadingEngine for RFC-2822 headers.
            intent_parser: Optional custom IntentParser (default created internally).
            template_path: Path to drafting.jinja2 (auto-discovered if None).
            neo4j_client: Optional Neo4j async driver for relationship context.
            chunk_store: Optional ChunkStore for thread context retrieval.
            user_name: Default user name for template substitution.
            calendar_link: Default calendar link for template substitution.
        """
        self.llm = llm
        self.voice = voice
        self.threading = threading
        self.intent_parser = intent_parser or IntentParser(llm)
        self.neo4j = neo4j_client
        self.chunk_store = chunk_store
        self._user_name = user_name
        self._calendar_link = calendar_link

        # Jinja2 environment
        tpl_path = template_path or _DEFAULT_TEMPLATE_PATH
        self.env = jinja2.Environment(
            loader=jinja2.FileSystemLoader(str(tpl_path.parent)),
            autoescape=False,
            trim_blocks=True,
            lstrip_blocks=True,
        )
        self.template_name = tpl_path.name

    # ------------------------------------------------------------------
    # Core pipeline
    # ------------------------------------------------------------------

    async def draft(
        self,
        user_id: str,
        card_id: str,
        thread_id: str,
        user_input: str,
    ) -> Draft:
        """Transform a one-line decision into a full email draft.

        Args:
            user_id: The authenticated user's UUID (string).
            card_id: The decision card's UUID (string).
            thread_id: The email thread's UUID (string).
            user_input: User's one-line instruction (e.g., ``"9500, two weeks"``).

        Returns:
            A :class:`Draft` with body, headers, tone profile, and provenance.
        """
        pipeline_start = time.perf_counter()
        card_uuid = UUID(card_id) if isinstance(card_id, str) else card_id

        # ------------------------------------------------------------------
        # Step 1: Parse user intent (Claude 3 Haiku — fast, cheap)
        # ------------------------------------------------------------------
        intent = await self._parse_intent(user_input)
        logger.info(
            "Intent parsed: action=%s tone=%s (input='%s')",
            intent.action,
            intent.tone_modifier,
            user_input,
        )

        # ------------------------------------------------------------------
        # Step 2: Check intent cache (Redis fast path)
        # ------------------------------------------------------------------
        intent_hash = self._compute_intent_hash(intent)
        cache_key = f"draft_intent:{user_id}:{intent_hash}"

        cached_template = await self._get_cached_draft_template(cache_key)
        if cached_template:
            # Check similarity against stored template's intent signature
            similarity = self._compute_intent_similarity(intent, cached_template.get("intent_signature", {}))
            if similarity >= _INTENT_CACHE_SIMILARITY_THRESHOLD:
                logger.info(
                    "Intent cache HIT: action=%s similarity=%.3f key=%s",
                    intent.action, similarity, cache_key,
                )
                # Fast path: template substitution (<2s)
                draft_body = self._render_from_template(
                    cached_template["template"], intent, user_id, thread_id
                )
                headers = await self.threading.build_headers(
                    thread_id=thread_id,
                    draft_content=draft_body,
                )
                total_latency = (time.perf_counter() - pipeline_start) * 1000
                logger.info(
                    "Draft cache fast path: %.1fms (action=%s)",
                    total_latency, intent.action,
                )
                return Draft(
                    card_id=card_uuid,
                    draft_body=draft_body,
                    subject_line=headers.subject,
                    in_reply_to=headers.in_reply_to,
                    references=headers.references,
                    tone_profile=cached_template.get("tone_profile", intent.tone_modifier or "professional"),
                    model_used="cache_hit",
                    tokens_used=0,
                    voice_examples_used=cached_template.get("voice_examples_used", []),
                    intent=intent,
                    latency_ms=total_latency,
                )
            else:
                logger.info(
                    "Intent cache similarity too low: %.3f < %.3f (action=%s)",
                    similarity, _INTENT_CACHE_SIMILARITY_THRESHOLD, intent.action,
                )
        else:
            logger.info("Intent cache MISS: action=%s key=%s", intent.action, cache_key)

        # ------------------------------------------------------------------
        # Steps 3-5: Retrieve context concurrently
        # ------------------------------------------------------------------
        # These three are independent — run them in parallel
        voice_task = self.voice.retrieve(
            user_id=user_id,
            thread_id=thread_id,
            user_input=user_input,
            limit=3,
        )
        rel_task = self._get_relationship_context(user_id, thread_id)
        thread_task = self._get_thread_context(thread_id, user_id)

        voice_examples, rel_ctx, thread_ctx = await self._gather(
            voice_task, rel_task, thread_task
        )

        logger.info(
            "Context gathered: voice=%d examples rel=%s thread=%d emails",
            len(voice_examples),
            "present" if rel_ctx else "absent",
            len(thread_ctx.get("prior_emails", [])),
        )

        # ------------------------------------------------------------------
        # Step 6: Build drafting prompt (Jinja2)
        # ------------------------------------------------------------------
        prompt = self._render_draft_prompt(
            intent=intent,
            voice_examples=voice_examples,
            rel_ctx=rel_ctx,
            thread_ctx=thread_ctx,
        )

        # ------------------------------------------------------------------
        # Step 7: Generate draft (Claude 3.5 Sonnet — quality)
        # ------------------------------------------------------------------
        result = await self.llm.generate(
            prompt=prompt,
            temperature=0.4,
            max_tokens=1500,
            user_id=user_id,
        )

        if not result.is_success:
            logger.error(
                "Draft generation failed for user=%s card=%s: %s",
                user_id,
                card_id,
                result.error_message,
            )
            # Return an error-marked draft rather than raising
            return Draft(
                card_id=card_uuid,
                draft_body=(
                    f"[Draft generation failed — please try again. "
                    f"Error: {result.error_message}]"
                ),
                subject_line="",
                tone_profile="",
                model_used=result.model or "error",
                tokens_used=result.tokens_used,
                voice_examples_used=[],
                intent=intent,
                latency_ms=(time.perf_counter() - pipeline_start) * 1000,
            )

        # ------------------------------------------------------------------
        # Step 8: Extract threading headers
        # ------------------------------------------------------------------
        headers = await self.threading.build_headers(
            thread_id=thread_id,
            draft_content=result.text,
        )

        # ------------------------------------------------------------------
        # Step 9: Build and return Draft
        # ------------------------------------------------------------------
        tone_profile = self._extract_tone(voice_examples)
        voice_hashes = [self._hash_example(ex) for ex in voice_examples]

        total_latency = (time.perf_counter() - pipeline_start) * 1000

        draft = Draft(
            card_id=card_uuid,
            draft_body=result.text,
            subject_line=headers.subject,
            in_reply_to=headers.in_reply_to,
            references=headers.references,
            tone_profile=tone_profile,
            model_used=result.model or "unknown",
            tokens_used=result.tokens_used,
            voice_examples_used=voice_hashes,
            intent=intent,
            latency_ms=total_latency,
        )

        # Cache the template for future reuse
        await self._cache_draft_template(
            cache_key=cache_key,
            template=draft.draft_body,
            intent=intent,
            tone_profile=tone_profile,
            voice_examples_used=voice_hashes,
        )

        logger.info(
            "Draft complete: card=%s model=%s tokens=%d voice=%d latency=%.1fms",
            card_id,
            draft.model_used,
            draft.tokens_used,
            len(voice_hashes),
            total_latency,
        )
        return draft

    # ------------------------------------------------------------------
    # Intent caching (new)
    # ------------------------------------------------------------------

    @staticmethod
    def _compute_intent_hash(intent: Intent) -> str:
        """Compute a deterministic hash from an Intent for cache keying.

        Uses action + price + timeline for the hash base so that intents
        with the same structure hit the same cache bucket.
        """
        base = f"{intent.action}:{intent.price or ''}:{intent.timeline or ''}:{intent.condition or ''}"
        return hashlib.sha256(base.encode("utf-8")).hexdigest()[:16]

    @staticmethod
    def _compute_intent_similarity(intent: Intent, signature: Dict[str, Any]) -> float:
        """Compute a simple similarity score between an intent and a cached signature.

        Returns a float in [0.0, 1.0]. 1.0 means exact match.
        """
        scores: List[float] = []
        weights: List[float] = []

        # Action match (weight 0.4)
        weights.append(0.4)
        scores.append(1.0 if intent.action == signature.get("action", "") else 0.0)

        # Price match (weight 0.2)
        weights.append(0.2)
        scores.append(
            1.0 if (intent.price or "") == signature.get("price", "") else 0.0
        )

        # Timeline match (weight 0.2)
        weights.append(0.2)
        scores.append(
            1.0 if (intent.timeline or "") == signature.get("timeline", "") else 0.0
        )

        # Condition match (weight 0.1)
        weights.append(0.1)
        scores.append(
            1.0 if (intent.condition or "") == signature.get("condition", "") else 0.0
        )

        # Tone modifier match (weight 0.1)
        weights.append(0.1)
        scores.append(
            1.0 if (intent.tone_modifier or "") == signature.get("tone_modifier", "") else 0.0
        )

        total_weight = sum(weights)
        if total_weight == 0:
            return 0.0
        weighted_score = sum(s * w for s, w in zip(scores, weights)) / total_weight
        return weighted_score

    async def _get_cached_draft_template(self, cache_key: str) -> Optional[Dict[str, Any]]:
        """Check Redis for a cached draft template."""
        try:
            redis = await get_redis()
            cached = await redis.get(cache_key)
            if cached:
                return json.loads(cached)
        except Exception as exc:
            logger.warning("Draft cache read failed (non-blocking): %s", exc)
        return None

    async def _cache_draft_template(
        self,
        cache_key: str,
        template: str,
        intent: Intent,
        tone_profile: str,
        voice_examples_used: List[str],
    ) -> None:
        """Store a draft template in Redis with 24h TTL."""
        try:
            redis = await get_redis()
            payload = {
                "template": template,
                "intent_signature": {
                    "action": intent.action,
                    "price": intent.price or "",
                    "timeline": intent.timeline or "",
                    "condition": intent.condition or "",
                    "tone_modifier": intent.tone_modifier or "",
                },
                "tone_profile": tone_profile,
                "voice_examples_used": voice_examples_used,
                "cached_at": time.time(),
            }
            await redis.setex(cache_key, _DRAFT_TEMPLATE_CACHE_TTL, json.dumps(payload))
            logger.debug(
                "Cached draft template: key=%s action=%s ttl=%ds",
                cache_key, intent.action, _DRAFT_TEMPLATE_CACHE_TTL,
            )
        except Exception as exc:
            logger.warning("Draft cache write failed (non-blocking): %s", exc)

    def _render_from_template(
        self,
        template: str,
        intent: Intent,
        user_id: str,
        thread_id: str,
    ) -> str:
        """Render a draft body from a cached template using variable substitution.

        Falls back to a predefined template if the cached template is empty.
        """
        # Try to get contact info from Neo4j if available
        contact_name = "there"
        # Use a simple synchronous approach for the fast path
        variables = {
            "contact_name": contact_name,
            "user_name": self._user_name or "there",
            "calendar_link": self._calendar_link or "",
            "approval_details": intent.condition or "This looks good.",
            "decline_reason": intent.condition or "",
            "info_request": intent.condition or "this",
            "availability_window": intent.timeline or "during business hours",
            "price": intent.price or "",
            "timeline": intent.timeline or "",
            "condition": intent.condition or "",
        }

        try:
            return template.format(**variables)
        except (KeyError, ValueError):
            # If template uses Jinja2 or has mismatched variables, return as-is
            return template

    # ------------------------------------------------------------------
    # Cache warming
    # ------------------------------------------------------------------

    async def prewarm_intent_cache(self) -> int:
        """Pre-warm the global intent cache with common templates at startup.

        Caches intent templates (approve, decline, suggest_time, ask_info)
        in Redis so that the intent fast-path can serve common actions
        without calling the LLM.

        Returns:
            Number of templates successfully cached.
        """
        common_intents = [
            {
                "action": "approve",
                "template": (
                    "Hi {name},\n\n"
                    "That works for me. {details}\n\n"
                    "Best,\n{signature}"
                ),
            },
            {
                "action": "decline",
                "template": (
                    "Hi {name},\n\n"
                    "Thanks for thinking of me, but I can't make this work. "
                    "{reason}\n\n"
                    "Best,\n{signature}"
                ),
            },
            {
                "action": "suggest_time",
                "template": (
                    "Hi {name},\n\n"
                    "How about {time}? Let me know if that works.\n\n"
                    "Best,\n{signature}"
                ),
            },
            {
                "action": "ask_info",
                "template": (
                    "Hi {name},\n\n"
                    "Could you clarify {question}?\n\n"
                    "Thanks,\n{signature}"
                ),
            },
        ]

        redis = await get_redis()
        cached_count = 0

        for intent in common_intents:
            key = f"intent_template:{intent['action']}"
            try:
                await redis.setex(key, 86400, json.dumps(intent))
                cached_count += 1
                logger.debug(
                    "Pre-warmed intent template: action=%s key=%s",
                    intent["action"],
                    key,
                )
            except Exception as exc:
                logger.warning(
                    "Failed to pre-warm intent template %s: %s",
                    intent["action"],
                    exc,
                )

        logger.info(
            "Pre-warmed %d/%d global intent templates",
            cached_count,
            len(common_intents),
        )
        return cached_count

    async def prewarm_intent_cache_for_user(self, user_id: str) -> int:
        """Pre-warm the intent cache with common templates for a specific user.

        This is a user-scoped variant that caches under the user's namespace,
        suitable for calling during per-user cache warming.

        Returns the number of templates cached.
        """
        count = 0
        for action, template in PREDEFINED_TEMPLATES.items():
            # Build a synthetic intent for each common action
            synthetic_intent = Intent(
                action=action if action != "suggest_next_week" else "propose_time",
                price=None,
                timeline="next week" if action == "suggest_next_week" else None,
                condition=None,
                deadline=None,
                tone_modifier="friendly",
            )
            intent_hash = self._compute_intent_hash(synthetic_intent)
            cache_key = f"draft_intent:{user_id}:{intent_hash}"

            payload = {
                "template": template,
                "intent_signature": {
                    "action": synthetic_intent.action,
                    "price": "",
                    "timeline": synthetic_intent.timeline or "",
                    "condition": "",
                    "tone_modifier": synthetic_intent.tone_modifier or "",
                },
                "tone_profile": synthetic_intent.tone_modifier or "professional",
                "voice_examples_used": [],
                "cached_at": time.time(),
                "predefined": True,
            }

            try:
                redis = await get_redis()
                await redis.setex(cache_key, _DRAFT_TEMPLATE_CACHE_TTL, json.dumps(payload))
                count += 1
                logger.debug("Pre-warmed cache: action=%s key=%s", action, cache_key)
            except Exception as exc:
                logger.warning("Cache pre-warm failed for action=%s: %s", action, exc)

        logger.info("Pre-warmed %d intent templates for user=%s", count, user_id)
        return count

    # ------------------------------------------------------------------
    # Pipeline steps (private)
    # ------------------------------------------------------------------

    async def _parse_intent(self, user_input: str) -> Intent:
        """Step 1: Parse user's one-liner into structured Intent."""
        return await self.intent_parser.parse(user_input)

    async def _get_relationship_context(
        self, user_id: str, thread_id: str
    ) -> Optional[Dict[str, Any]]:
        """Step 4: Get relationship context from Neo4j (optional).

        Returns a dict with relationship metadata, or None if Neo4j
        is not configured or the query fails.
        """
        if self.neo4j is None:
            return None

        try:
            # Cypher: find the contact associated with this thread
            # and return relationship metadata
            query = """
                MATCH (u:User {id: $user_id})-[:HAS_THREAD]->(t:Thread {id: $thread_id})
                      -[:WITH_CONTACT]->(c:Contact)
                OPTIONAL MATCH (u)-[r:KNOWS]->(c)
                RETURN c.email AS contact_email,
                       c.name AS contact_name,
                       r.relationship_type AS rel_type,
                       r.seniority AS seniority,
                       r.communication_pref AS comm_pref,
                       r.last_interaction AS last_interaction,
                       r.tone_history AS tone_history
            """
            result = await self.neo4j.run(query, user_id=user_id, thread_id=thread_id)
            record = result.single()
            if record:
                return {
                    "contact_email": record["contact_email"],
                    "contact_name": record["contact_name"],
                    "relationship_type": record["rel_type"] or "professional",
                    "seniority": record["seniority"] or "peer",
                    "communication_preference": record["comm_pref"] or "email",
                    "last_interaction": record["last_interaction"],
                    "tone_history": record["tone_history"] or [],
                }
        except Exception as exc:
            logger.warning("Neo4j relationship query failed (non-blocking): %s", exc)

        return None

    async def _get_thread_context(
        self, thread_id: str, user_id: str
    ) -> Dict[str, Any]:
        """Step 5: Get thread context — prior emails and decision card info.

        Returns:
            Dict with ``prior_emails`` list and ``decision_context`` string.
        """
        prior_emails: List[Dict[str, str]] = []
        decision_context = ""

        if self.chunk_store is not None:
            try:
                chunks = await self.chunk_store.get_chunks_by_thread(
                    thread_id, user_id
                )
                # Deduplicate by email_id, keep ordering
                seen_ids = set()
                for chunk in chunks:
                    email_id = str(chunk.email_id)
                    if email_id not in seen_ids and chunk.content_snippet:
                        seen_ids.add(email_id)
                        prior_emails.append({
                            "sender": chunk.sender_email or "unknown",
                            "date": chunk.timestamp.isoformat() if chunk.timestamp else "",
                            "body": chunk.content_snippet,
                        })
            except Exception as exc:
                logger.warning(
                    "Thread context retrieval failed (non-blocking): %s", exc
                )

        return {
            "prior_emails": prior_emails,
            "decision_context": decision_context,
        }

    def _render_draft_prompt(
        self,
        intent: Intent,
        voice_examples: List[VoiceExample],
        rel_ctx: Optional[Dict[str, Any]],
        thread_ctx: Dict[str, Any],
    ) -> str:
        """Step 6: Render the Jinja2 drafting prompt template.

        Maps our structured data to the template variables expected by
        ``drafting.jinja2``.
        """
        template = self.env.get_template(self.template_name)

        # Build decision_context from intent + relationship
        decision_parts = [f"Action: {intent.action}"]
        if intent.price:
            decision_parts.append(f"Price: {intent.price}")
        if intent.timeline:
            decision_parts.append(f"Timeline: {intent.timeline}")
        if intent.condition:
            decision_parts.append(f"Condition: {intent.condition}")
        if intent.deadline:
            decision_parts.append(f"Deadline: {intent.deadline}")
        if intent.tone_modifier:
            decision_parts.append(f"Tone: {intent.tone_modifier}")

        if rel_ctx:
            decision_parts.append(
                f"Relationship: {rel_ctx.get('relationship_type', 'professional')} "
                f"with {rel_ctx.get('contact_name', 'contact')}"
            )

        decision_context = "\n".join(decision_parts)

        # Build user_style from voice examples
        user_style = None
        if voice_examples:
            tone_tags: set = set()
            avg_words = 0.0
            for ex in voice_examples:
                tone_tags.update(ex.tone_tags)
                avg_words += len(ex.reply_text.split())
            avg_words = avg_words / len(voice_examples) if voice_examples else 0

            style_parts = [
                f"Observed tones: {', '.join(sorted(tone_tags))}",
                f"Average reply length: {avg_words:.0f} words",
                "Recent examples:",
            ]
            for i, ex in enumerate(voice_examples, 1):
                snippet = ex.reply_text[:200].replace("\n", " ")
                style_parts.append(f"  [{i}] {snippet}...")
            user_style = "\n".join(style_parts)

        # Format user_intent
        user_intent = intent.model_dump_json(indent=2, exclude_none=True)

        # Render
        try:
            rendered = template.render(
                decision_context=decision_context,
                user_intent=user_intent,
                user_style=user_style,
                prior_emails=thread_ctx.get("prior_emails", []),
            )
        except jinja2.TemplateError as exc:
            logger.error("Template rendering failed: %s", exc)
            # Fallback: bare prompt
            rendered = (
                f"Write a professional email based on this decision:\n\n"
                f"{decision_context}\n\n"
                f"User intent: {user_intent}\n\n"
                f"Email body:"
            )

        return rendered

    # ------------------------------------------------------------------
    # Utility
    # ------------------------------------------------------------------

    @staticmethod
    def _extract_tone(voice_examples: List[VoiceExample]) -> str:
        """Build a concise tone-profile string from voice examples.

        Returns a comma-separated string of dominant tone tags.
        """
        if not voice_examples:
            return "professional, neutral"

        tag_freq: Dict[str, int] = {}
        for ex in voice_examples:
            for tag in ex.tone_tags:
                tag_freq[tag] = tag_freq.get(tag, 0) + 1

        if not tag_freq:
            return "professional, neutral"

        sorted_tags = sorted(tag_freq.items(), key=lambda x: x[1], reverse=True)
        return ", ".join(tag for tag, _ in sorted_tags[:5])

    @staticmethod
    def _hash_example(example: VoiceExample) -> str:
        """Create a deterministic hash of a voice example for provenance.

        Uses SHA-256 over the reply text + sent_at timestamp.
        """
        content = f"{example.reply_text}:{example.sent_at.isoformat()}"
        return hashlib.sha256(content.encode("utf-8")).hexdigest()[:16]

    @staticmethod
    async def _gather(*awaitables):
        """Convenience wrapper for asyncio.gather with exception handling."""
        import asyncio
        results = await asyncio.gather(*awaitables, return_exceptions=True)
        cleaned = []
        for r in results:
            if isinstance(r, Exception):
                logger.warning("Concurrent task failed: %s", r)
                cleaned.append(None)
            else:
                cleaned.append(r)
        return cleaned

    # ------------------------------------------------------------------
    # Diagnostics
    # ------------------------------------------------------------------

    def describe(self) -> Dict[str, Any]:
        """Return a human-readable description of service configuration."""
        return {
            "llm": self.llm.describe(),
            "voice_retriever": {
                "collection": self.voice.collection_name,
                "half_life_days": self.voice.half_life_days,
                "similarity_floor": self.voice.similarity_floor,
            },
            "template": str(self.template_name),
            "intent_parser": "IntentParser (Haiku)",
            "neo4j_enabled": self.neo4j is not None,
            "chunk_store_enabled": self.chunk_store is not None,
            "intent_cache_similarity_threshold": _INTENT_CACHE_SIMILARITY_THRESHOLD,
            "predefined_templates": list(PREDEFINED_TEMPLATES.keys()),
        }
