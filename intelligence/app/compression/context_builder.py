"""ContextBuilder — assembles the context block for the compression prompt.

Pulls relationship graph data from Neo4j and calendar/free-busy data from
PostgreSQL, formatting both into human-readable strings that the LLM consumes
inside the Jinja2 prompt template.

Extended with:
  - build_relationship_context  ( Neo4j participant + interaction graph )
  - build_calendar_context      ( PostgreSQL 7-day window )
  - build_thread_summary        ( Qdrant consultation_index cache )
"""
from __future__ import annotations

import logging
from datetime import datetime, timedelta, timezone
from typing import Any

from intelligence.core.qdrant_client import QdrantClientWrapper
from intelligence.infra.db.neo4j_client import Neo4jClient
from intelligence.infra.db.postgres_client import PostgresClient

logger = logging.getLogger(__name__)

# Threshold for triggering a cached thread summary
_THREAD_SUMMARY_EMAIL_THRESHOLD = 50


class ContextBuilder:
    """Builds the context block for the compression prompt."""

    def __init__(
        self,
        neo4j: Neo4jClient,
        postgres: PostgresClient,
        qdrant: QdrantClientWrapper | None = None,
    ) -> None:
        self.neo4j = neo4j
        self.postgres = postgres
        self.qdrant = qdrant

    # ------------------------------------------------------------------
    # Relationship context (Neo4j)
    # ------------------------------------------------------------------

    async def build_relationship_context(
        self,
        user_id: str,
        thread_id: str,
    ) -> str:
        """Query Neo4j for contact intelligence on thread participants.

        Executes two graph queries:
        1. Contact nodes linked via PARTICIPANT_IN to the thread, with
           aggregated stats (interaction_count, avg_response_hours,
           tone_history, total_monetary_value).
        2. Interaction nodes linked via HAS_INTERACTION, filtered by
           thread_id, ordered descending by date.

        Returns a formatted string with:
        - Contact info (name, email, company, role)
        - Interaction stats (count, avg response time, tone history)
        - Commercial signals (project, monetary value)
        - Prior commitments in this thread
        - Recent interaction history
        """
        lines: list[str] = []
        lines.append("--- Contact Graph ---")

        try:
            participants = await self._get_participants(user_id, thread_id)
            if not participants:
                lines.append("No prior relationship data found for thread participants.")
                return "\n".join(lines)

            for contact in participants:
                name = contact.get("name", "Unknown")
                email = contact.get("email", "N/A")
                company = contact.get("company", "N/A")
                role = contact.get("role", "N/A")

                lines.append(f"\nContact: {name} <{email}>")
                lines.append(f"  Company: {company} | Role: {role}")

                # Interaction stats
                stats = contact.get("stats", {})
                interaction_count = stats.get("interaction_count", 0)
                avg_response_hours = stats.get("avg_response_hours")
                tone_history = stats.get("tone_history", [])

                lines.append(f"  Interactions: {interaction_count}")
                if avg_response_hours is not None:
                    lines.append(f"  Avg response time: {avg_response_hours:.1f} hours")
                if tone_history:
                    lines.append(f"  Tone history: {', '.join(str(t) for t in tone_history[:5])}")

                # Commercial context
                last_project = contact.get("last_project")
                total_monetary_value = contact.get("total_monetary_value")
                if last_project:
                    lines.append(f"  Last project: {last_project}")
                if total_monetary_value is not None:
                    lines.append(f"  Lifetime value: ${total_monetary_value:,.2f}")

                # Recent interactions in this thread
                interactions = contact.get("recent_interactions", [])
                if interactions:
                    lines.append("  Recent interactions in thread:")
                    for ix in interactions[:5]:
                        ix_type = ix.get("type", "?")
                        ix_value = ix.get("value", "")
                        ix_date = ix.get("date", "?")
                        val_str = f" (${ix_value:,.2f})" if ix_value else ""
                        lines.append(f"    - [{ix_date}] {ix_type}{val_str}")

            # Prior commitments in this thread
            commitments = await self._get_thread_commitments(user_id, thread_id)
            if commitments:
                lines.append("\n--- Prior Commitments in Thread ---")
                for commit in commitments:
                    lines.append(f"  - [{commit.get('date', '?')}] {commit.get('text', '')}")

        except Exception as exc:
            logger.exception("Failed to build relationship context: %s", exc)
            lines.append(f"[Error loading relationship data: {exc}]")

        return "\n".join(lines)

    async def _get_participants(
        self,
        user_id: str,
        thread_id: str,
    ) -> list[dict[str, Any]]:
        """Query Neo4j for participants in a thread with relationship stats.

        Primary pattern (per spec):
            MATCH (c:Contact)-[:PARTICIPANT_IN]->(t:Thread {id: $thread_id})
            RETURN c.name, c.interaction_count, c.avg_response_hours,
                   c.tone_history, c.total_monetary_value

        Secondary pattern for interaction detail:
            MATCH (c)-[:HAS_INTERACTION]->(i:Interaction)
            WHERE i.thread_id = $thread_id
            RETURN i.type, i.value, i.date ORDER BY i.date DESC
        """
        # Primary query — participant stats
        cypher_participants = """
            MATCH (c:Contact)-[:PARTICIPANT_IN]->(t:Thread {id: $thread_id})
            OPTIONAL MATCH (c)-[:WORKS_AT]->(co:Company)
            OPTIONAL MATCH (c)-[:HAS_ROLE]->(r:Role)
            OPTIONAL MATCH (u:User {id: $user_id})-[:OWNS]->(t)
            OPTIONAL MATCH (u)-[i:INTERACTED_WITH]->(c)
            WITH c, co, r,
                 count(i) AS interaction_count,
                 avg(i.response_hours) AS avg_response_hours,
                 collect(DISTINCT i.tone) AS tone_history,
                 c.last_project AS last_project,
                 c.total_monetary_value AS total_monetary_value
            RETURN {
                name: c.name,
                email: c.email,
                company: co.name,
                role: r.title,
                stats: {
                    interaction_count: interaction_count,
                    avg_response_hours: avg_response_hours,
                    tone_history: tone_history
                },
                last_project: last_project,
                total_monetary_value: total_monetary_value
            } AS contact
        """
        records = await self.neo4j.query(
            cypher_participants,
            {"user_id": user_id, "thread_id": thread_id},
        )
        contacts: list[dict[str, Any]] = [r["contact"] for r in records]

        # Secondary query — recent interaction detail per contact
        cypher_interactions = """
            MATCH (c:Contact)-[:HAS_INTERACTION]->(i:Interaction)
            WHERE i.thread_id = $thread_id
            RETURN c.email AS contact_email,
                   i.type AS type,
                   i.value AS value,
                   i.date AS date
            ORDER BY i.date DESC
            LIMIT 20
        """
        ix_records = await self.neo4j.query(
            cypher_interactions,
            {"thread_id": thread_id},
        )

        # Attach interactions to their parent contacts
        ix_by_contact: dict[str, list[dict[str, Any]]] = {}
        for rec in ix_records:
            email = rec.get("contact_email")
            if email:
                ix_by_contact.setdefault(email, []).append({
                    "type": rec.get("type"),
                    "value": rec.get("value"),
                    "date": rec.get("date"),
                })

        for contact in contacts:
            email = contact.get("email")
            if email and email in ix_by_contact:
                contact["recent_interactions"] = ix_by_contact[email]

        return contacts

    async def _get_thread_commitments(
        self,
        user_id: str,
        thread_id: str,
    ) -> list[dict[str, str]]:
        """Query Neo4j for prior commitments in this thread."""
        cypher = """
            MATCH (u:User {id: $user_id})-[:OWNS]->(t:Thread {id: $thread_id})
                  -[:CONTAINS]->(e:Email)-[:CONTAINS_COMMITMENT]->(commit:Commitment)
            RETURN commit.text AS text, commit.date AS date
            ORDER BY commit.date DESC
        """
        return await self.neo4j.query(cypher, {"user_id": user_id, "thread_id": thread_id})

    # ------------------------------------------------------------------
    # Calendar context (PostgreSQL)
    # ------------------------------------------------------------------

    async def build_calendar_context(
        self,
        user_id: str,
    ) -> str:
        """Query PostgreSQL calendar_events for the next 7 days.

        Returns a formatted string with:
        - Upcoming events
        - Potential conflicts (events within 24h)
        - Free/busy indicators
        """
        lines: list[str] = []
        lines.append("--- Next 7 Days ---")

        try:
            now = datetime.now(timezone.utc)
            window_end = now + timedelta(days=7)

            events = await self._get_calendar_events(user_id, now, window_end)

            if not events:
                lines.append("No events scheduled for the next 7 days.")
                return "\n".join(lines)

            # Group by day
            by_day: dict[str, list[dict[str, Any]]] = {}
            for event in events:
                day_key = event["start_time"].strftime("%Y-%m-%d (%a)") if isinstance(event["start_time"], datetime) else str(event["start_time"])[:10]
                by_day.setdefault(day_key, []).append(event)

            for day in sorted(by_day.keys()):
                lines.append(f"\n{day}:")
                for event in by_day[day]:
                    start = event["start_time"]
                    time_str = start.strftime("%H:%M") if isinstance(start, datetime) else "?"
                    summary = event.get("summary", "Untitled")
                    event_type = event.get("event_type", "event")
                    status = event.get("status", "confirmed")
                    lines.append(f"  [{time_str}] {summary} ({event_type}, {status})")

            # Conflict warning (events in next 24h)
            next_24h = now + timedelta(hours=24)
            conflicts = [e for e in events if e["start_time"] <= next_24h]
            if conflicts:
                lines.append(f"\n⚠️  CONFLICT WARNING: {len(conflicts)} event(s) in next 24 hours")
                for c in conflicts:
                    lines.append(f"   - {c.get('summary', '?')} at {c['start_time']}")

            # Free/busy summary
            busy_slots = len(events)
            lines.append(f"\nBusy slots next 7 days: {busy_slots}")

        except Exception as exc:
            logger.exception("Failed to build calendar context: %s", exc)
            lines.append(f"[Error loading calendar data: {exc}]")

        return "\n".join(lines)

    async def _get_calendar_events(
        self,
        user_id: str,
        start: datetime,
        end: datetime,
    ) -> list[dict[str, Any]]:
        """Fetch calendar events from PostgreSQL."""
        sql = """
            SELECT title AS summary, start_at AS start_time, end_at AS end_time,
                   location, description, is_confirmed AS status
            FROM calendar_events
            WHERE user_id = $1
              AND start_at >= $2
              AND start_at <= $3
            ORDER BY start_at ASC
        """
        return await self.postgres.fetch(sql, user_id, start, end)

    # ------------------------------------------------------------------
    # Thread summary (Qdrant consultation_index)
    # ------------------------------------------------------------------

    async def build_thread_summary(
        self,
        user_id: str,
        thread_id: str,
    ) -> str | None:
        """Return a cached thread summary from Qdrant or None.

        Only threads with > {threshold} emails are summarised and stored in
        the ``consultation_index`` Qdrant collection.  If no cached summary
        exists (or Qdrant is unavailable) the method returns *None* and the
        caller falls back to on-the-fly context building.

        Parameters
        ----------
        user_id:
            Mailbox owner UUID.
        thread_id:
            Thread UUID.

        Returns
        -------
        str | None
            Human-readable summary paragraph, or *None* if not cached yet.
        """
        if self.qdrant is None:
            logger.debug("Qdrant not configured; skipping thread summary lookup")
            return None

        try:
            # Check email count first — only summarise heavy threads
            email_count = await self._get_thread_email_count(user_id, thread_id)
            if email_count < _THREAD_SUMMARY_EMAIL_THRESHOLD:
                logger.debug(
                    "Thread %s has %d emails (<%d); no summary cache",
                    thread_id, email_count, _THREAD_SUMMARY_EMAIL_THRESHOLD,
                )
                return None

            # Query Qdrant consultation_index by thread_id payload filter
            from qdrant_client.models import FieldCondition, Filter, MatchValue

            search_result = await self.qdrant.client.scroll(
                collection_name="consultation_index",
                scroll_filter=Filter(
                    must=[
                        FieldCondition(
                            key="thread_id",
                            match=MatchValue(value=str(thread_id)),
                        ),
                        FieldCondition(
                            key="user_id",
                            match=MatchValue(value=str(user_id)),
                        ),
                        FieldCondition(
                            key="type",
                            match=MatchValue(value="thread_summary"),
                        ),
                    ]
                ),
                limit=1,
                with_payload=True,
                with_vectors=False,
            )

            points = search_result[0] if isinstance(search_result, tuple) else search_result
            if not points:
                logger.debug("No cached summary for thread %s", thread_id)
                return None

            point = points[0]
            payload = point.payload or {}
            summary_text = payload.get("summary") or payload.get("text")
            if summary_text:
                logger.debug(
                    "Found cached summary for thread %s (%d chars)",
                    thread_id, len(summary_text),
                )
                return str(summary_text)

            return None

        except Exception as exc:
            logger.warning("Thread summary lookup failed (non-critical): %s", exc)
            return None

    async def _get_thread_email_count(self, user_id: str, thread_id: str) -> int:
        """Return the number of emails in a thread from PostgreSQL."""
        sql = """
            SELECT COUNT(*) AS cnt
            FROM raw_emails
            WHERE user_id = $1 AND thread_id = $2
        """
        row = await self.postgres.fetchrow(sql, user_id, thread_id)
        return row.get("cnt", 0) if row else 0
