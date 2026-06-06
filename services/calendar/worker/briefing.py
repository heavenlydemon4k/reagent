"""
Pre-event Briefing Generator — creates contextual pre-event briefing cards.

Pulls context from:
  - Neo4j relationship graph (contact info, last interaction, relationship strength)
  - Email thread history (if event linked to a thread)
  - Pipeline / deal context (monetary value)

Formats:
  "Your 2pm with Sarah Chen starts in 15 min.
   Context: Website Redesign proposal review.
   Last contact: 2 days ago. $50K pipeline."
"""

from __future__ import annotations

import logging
from datetime import datetime, timedelta
from typing import List, Optional
from uuid import UUID

from .models import (
    BriefingContext,
    CalendarEvent,
    ContactContext,
    ThreadContext,
)

logger = logging.getLogger("calendar_worker.briefing")


# ---------------------------------------------------------------------------
# Neo4j queries (Cypher)
# ---------------------------------------------------------------------------

_CYPHER_GET_CONTACT = """
    MATCH (u:User {id: $user_id})-[r:KNOWS]-(c:Contact {email: $email})
    RETURN c.name AS name, c.company AS company, c.role AS role,
           r.last_contact_date AS last_contact_date,
           r.strength AS relationship_strength,
           r.monetary_value AS monetary_value,
           r.notes AS notes
    LIMIT 1
"""

_CYPHER_GET_THREAD = """
    MATCH (t:Thread {id: $thread_id})<-[:PART_OF]-(m:Message)
    RETURN t.subject AS subject, count(m) AS message_count,
           max(m.date) AS last_message_date
    LIMIT 1
"""

_CYPHER_GET_LAST_INTERACTION = """
    MATCH (u:User {id: $user_id})-[r:KNOWS]-(c:Contact)
    WHERE c.email IN $emails
    WITH c, r
    ORDER BY r.last_contact_date DESC
    LIMIT 1
    RETURN c.email AS email, r.last_contact_date AS last_contact_date,
           r.summary AS interaction_summary
"""

_CYPHER_GET_ACTION_ITEMS = """
    MATCH (u:User {id: $user_id})-[r:KNOWS]-(c:Contact)
    WHERE c.email IN $emails
    OPTIONAL MATCH (c)-[:HAS_ACTION]->(a:ActionItem)
    WHERE a.status = 'open' AND a.due_date > datetime()
    RETURN a.description AS description
    LIMIT 5
"""


# ---------------------------------------------------------------------------
# Briefing Generator
# ---------------------------------------------------------------------------

class BriefingGenerator:
    """Generates pre-event briefing cards with rich context."""

    def __init__(self, neo4j_driver, db_pool):
        self.neo4j = neo4j_driver
        self.db = db_pool

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------

    async def generate(self, event: CalendarEvent, user_id: UUID) -> str:
        """
        Generate a pre-event briefing notification string.

        1. Load contact context from Neo4j for each attendee
        2. Load thread context if event linked to a thread
        3. Load last interaction summary
        4. Load open action items
        5. Render formatted briefing
        """
        ctx = BriefingContext(event=event)

        # --- contact context ---
        contacts = await self._load_contacts(user_id, event.attendee_emails)
        ctx.all_contacts = contacts
        if contacts:
            ctx.primary_contact = contacts[0]  # first attendee is primary

        # --- thread context ---
        if event.thread_id:
            thread = await self._load_thread(event.thread_id)
            ctx.thread = thread

        # --- last interaction ---
        if event.attendee_emails:
            summary = await self._load_last_interaction(user_id, event.attendee_emails)
            ctx.last_interaction_summary = summary

        # --- action items ---
        if event.attendee_emails:
            action_items = await self._load_action_items(user_id, event.attendee_emails)
            ctx.action_items = action_items

        # --- render ---
        return self._render(ctx)

    # ------------------------------------------------------------------
    # Data loaders
    # ------------------------------------------------------------------

    async def _load_contacts(
        self, user_id: UUID, emails: List[str]
    ) -> List[ContactContext]:
        """Pull contact records from Neo4j relationship graph."""
        contacts: List[ContactContext] = []
        for email in emails:
            try:
                async with self.neo4j.session() as session:
                    result = await session.run(
                        _CYPHER_GET_CONTACT,
                        user_id=str(user_id),
                        email=email,
                    )
                    record = await result.single()
                    if record:
                        contacts.append(ContactContext(
                            email=email,
                            name=record.get("name", ""),
                            company=record.get("company"),
                            role=record.get("role"),
                            last_contact_date=_parse_datetime(
                                record.get("last_contact_date")
                            ),
                            relationship_strength=record.get("relationship_strength", 0.0) or 0.0,
                            monetary_value=record.get("monetary_value"),
                            notes=record.get("notes"),
                        ))
                    else:
                        # Unknown contact — minimal entry
                        contacts.append(ContactContext(email=email))
            except Exception as exc:
                logger.warning("neo4j contact lookup failed for %s: %s", email, exc)
                contacts.append(ContactContext(email=email))
        return contacts

    async def _load_thread(self, thread_id: UUID) -> Optional[ThreadContext]:
        """Pull email thread context from Neo4j."""
        try:
            async with self.neo4j.session() as session:
                result = await session.run(
                    _CYPHER_GET_THREAD, thread_id=str(thread_id),
                )
                record = await result.single()
                if record:
                    return ThreadContext(
                        thread_id=thread_id,
                        subject=record.get("subject", ""),
                        last_message_date=_parse_datetime(
                            record.get("last_message_date")
                        ),
                        message_count=record.get("message_count", 0),
                    )
        except Exception as exc:
            logger.warning("neo4j thread lookup failed for %s: %s", thread_id, exc)
        return None

    async def _load_last_interaction(
        self, user_id: UUID, emails: List[str]
    ) -> Optional[str]:
        """Get summary of the most recent interaction with any of the attendees."""
        try:
            async with self.neo4j.session() as session:
                result = await session.run(
                    _CYPHER_GET_LAST_INTERACTION,
                    user_id=str(user_id),
                    emails=emails,
                )
                record = await result.single()
                if record:
                    return record.get("interaction_summary")
        except Exception as exc:
            logger.warning("neo4j last-interaction lookup failed: %s", exc)
        return None

    async def _load_action_items(
        self, user_id: UUID, emails: List[str]
    ) -> List[str]:
        """Get open action items related to the attendees."""
        try:
            async with self.neo4j.session() as session:
                result = await session.run(
                    _CYPHER_GET_ACTION_ITEMS,
                    user_id=str(user_id),
                    emails=emails,
                )
                records = await result.data()
                return [
                    r["description"] for r in records
                    if r.get("description")
                ][:5]
        except Exception as exc:
            logger.warning("neo4j action-items lookup failed: %s", exc)
        return []

    # ------------------------------------------------------------------
    # Rendering
    # ------------------------------------------------------------------

    def _render(self, ctx: BriefingContext) -> str:
        """Render the final briefing text."""
        event = ctx.event
        contact = ctx.primary_contact
        thread = ctx.thread

        # --- line 1: time + attendee ---
        event_time = event.start_at.strftime("%I:%M%p").lstrip("0").lower()
        minutes_until = self._minutes_until(event.start_at)

        if contact and contact.name:
            name = contact.name
        elif contact:
            name = contact.display_name
        else:
            name = ctx.all_contacts[0].display_name if ctx.all_contacts else "someone"

        lines: List[str] = [
            f"Your {event_time} with {name} starts in {minutes_until} min."
        ]

        # --- line 2: context (thread subject or event description) ---
        context = None
        if thread and thread.subject:
            context = thread.subject
        elif event.description:
            context = event.description[:80]

        if context:
            lines.append(f"Context: {context}.")

        # --- line 3: last contact ---
        if contact and contact.last_contact_date:
            lines.append(f"Last contact: {contact.last_contact_human}.")

        # --- line 4: monetary value ---
        if contact and contact.monetary_value:
            lines.append(f"Pipeline: {contact.monetary_value}.")

        # --- line 5: relationship strength indicator ---
        if contact and contact.relationship_strength > 0:
            strength = self._strength_label(contact.relationship_strength)
            if strength:
                lines.append(f"Relationship: {strength}.")

        # --- line 6: action items ---
        if ctx.action_items:
            items_str = "; ".join(ctx.action_items[:3])
            lines.append(f"Action items: {items_str}.")

        return " ".join(lines)

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _minutes_until(self, start_at: datetime) -> int:
        """Calculate minutes from now until event start."""
        delta = start_at - datetime.utcnow()
        if delta.total_seconds() <= 0:
            return 0
        return max(1, int(delta.total_seconds() / 60))

    def _strength_label(self, strength: float) -> str:
        """Convert relationship strength to human label."""
        if strength >= 0.8:
            return "strong"
        if strength >= 0.5:
            return "warm"
        if strength >= 0.3:
            return "developing"
        if strength > 0:
            return "new"
        return ""


# ---------------------------------------------------------------------------
# Utilities
# ---------------------------------------------------------------------------

def _parse_datetime(value) -> Optional[datetime]:
    """Parse a Neo4j datetime return into Python datetime."""
    if value is None:
        return None
    if isinstance(value, datetime):
        return value
    if isinstance(value, str):
        try:
            # ISO format with possible 'Z'
            v = value.replace("Z", "+00:00")
            return datetime.fromisoformat(v)
        except ValueError:
            return None
    # Neo4j DateTime object
    if hasattr(value, "year"):
        return datetime(
            value.year, value.month, value.day,
            getattr(value, "hour", 0),
            getattr(value, "minute", 0),
            getattr(value, "second", 0),
        )
    return None
