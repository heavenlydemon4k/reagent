"""
Contact Profile + Timeline Router

GET /v1/contacts/{contact_id}/profile
  → Neo4j relationship graph: interaction counts, response times,
    projects, monetary values, tone history.

GET /v1/contacts/{contact_id}/timeline
  → PostgreSQL: chronological thread summaries with decision outcomes.

POST /v1/contacts/{contact_id}/mute
  → Suppress decision cards from this sender.

POST /v1/contacts/{contact_id}/unmute
  → Re-enable decision cards from this sender.
"""

from __future__ import annotations

import logging
import os
from typing import Any, Dict, List, Optional

from fastapi import APIRouter, Depends, HTTPException, status
from pydantic import BaseModel, Field

# Attempt to import Neo4j client; fall back to None if not available
logger = logging.getLogger(__name__)

try:
    from intelligence.infra.db.neo4j_client import Neo4jClient
    _NEO4J_AVAILABLE = True
except ImportError:
    _NEO4J_AVAILABLE = False
    logger.warning("Neo4j client not available — contact routes will use mock data")

# ---------------------------------------------------------------------------
# Pydantic models
# ---------------------------------------------------------------------------

class ToneEntry(BaseModel):
    date: str  # ISO 8601
    tone: str


class ContactProfileResponse(BaseModel):
    id: str
    name: str
    email: str
    avatarUrl: Optional[str] = None
    interactionCount: int = 0
    avgResponseHours: float = 0.0
    totalMonetaryValue: float = 0.0
    projects: List[str] = Field(default_factory=list)
    toneHistory: List[ToneEntry] = Field(default_factory=list)
    firstContactDate: str = ""
    lastContactDate: str = ""
    company: Optional[str] = None
    title: Optional[str] = None
    relationshipStrength: Optional[float] = None


class ThreadSummaryItem(BaseModel):
    id: str
    subject: str
    date: str  # ISO 8601
    messageCount: int = 0
    decision: Optional[str] = None
    status: str = "active"  # active | resolved | archived
    preview: Optional[str] = None


class TimelineResponse(BaseModel):
    items: List[ThreadSummaryItem]
    hasMore: bool = False
    nextCursor: Optional[str] = None


# ---------------------------------------------------------------------------
# Neo4j helpers
# ---------------------------------------------------------------------------

_neo4j_client: Optional[Any] = None


def _get_neo4j_client() -> Optional[Any]:
    global _neo4j_client
    if _neo4j_client is not None:
        return _neo4j_client
    if not _NEO4J_AVAILABLE:
        return None

    try:
        neo4j_uri = os.environ.get("NEO4J_URI", "neo4j+s://localhost:7687")
        neo4j_password = os.environ.get("NEO4J_PASSWORD", "")
        if neo4j_uri == "neo4j+s://localhost:7687" and not neo4j_password:
            logger.info("No Neo4j credentials — using mock mode for contact routes")
            return None
        _neo4j_client = Neo4jClient()
        return _neo4j_client
    except Exception as exc:
        logger.warning("Failed to initialize Neo4j client: %s", exc)
        return None


async def _query_contact_profile(
    contact_id: str, user_id: str
) -> Optional[Dict[str, Any]]:
    client = _get_neo4j_client()
    if client is None:
        return None

    # Query 1: Contact node + basic stats
    contact_cypher = """
    MATCH (c:Contact {id: $contact_id, user_id: $user_id})
    OPTIONAL MATCH (c)-[i:INTERACTION]-()
    WITH c, count(i) as interactionCount,
         avg(i.response_hours) as avgResponseHours,
         sum(coalesce(i.monetary_value, 0)) as totalMonetaryValue
    RETURN c {
        .id, .name, .email, .company, .title, .first_contact_date,
        .last_contact_date, .avatar_url, .relationship_strength
    } as contact,
    interactionCount,
    avgResponseHours,
    totalMonetaryValue
    """

    try:
        result = await client.query(contact_cypher, {
            "contact_id": contact_id,
            "user_id": user_id,
        })
        if not result:
            return None
        return dict(result[0])
    except Exception as exc:
        logger.error("Neo4j contact profile query failed: %s", exc)
        return None


async def _query_contact_projects(
    contact_id: str, user_id: str
) -> List[str]:
    client = _get_neo4j_client()
    if client is None:
        return []

    cypher = """
    MATCH (c:Contact {id: $contact_id, user_id: $user_id})-[:INVOLVED_IN]->(p:Project)
    RETURN collect(DISTINCT p.name) as projects
    """
    try:
        result = await client.query(cypher, {
            "contact_id": contact_id,
            "user_id": user_id,
        })
        if not result:
            return []
        return result[0].get("projects", []) or []
    except Exception as exc:
        logger.error("Neo4j projects query failed: %s", exc)
        return []


async def _query_tone_history(
    contact_id: str, user_id: str
) -> List[Dict[str, str]]:
    client = _get_neo4j_client()
    if client is None:
        return []

    cypher = """
    MATCH (c:Contact {id: $contact_id, user_id: $user_id})-[t:TONE]->()
    RETURN t.date as date, t.tone as tone
    ORDER BY t.date ASC
    """
    try:
        result = await client.query(cypher, {
            "contact_id": contact_id,
            "user_id": user_id,
        })
        return [{"date": r["date"], "tone": r["tone"]} for r in result if r.get("date")]
    except Exception as exc:
        logger.error("Neo4j tone history query failed: %s", exc)
        return []


# ---------------------------------------------------------------------------
# Mock data (for dev / when Neo4j is unavailable)
# ---------------------------------------------------------------------------

_MOCK_PROFILES: Dict[str, Dict[str, Any]] = {}
_MOCK_TIMELINES: Dict[str, List[Dict[str, Any]]] = {}


def _get_mock_profile(contact_id: str, user_id: str) -> Dict[str, Any]:
    key = f"{user_id}:{contact_id}"
    if key not in _MOCK_PROFILES:
        import random
        names = ["Alice Chen", "Bob Martinez", "Carol Williams", "David Kim"]
        domains = ["acme.com", "startup.io", "enterprise.co", "consulting.net"]
        idx = hash(key) % len(names)
        _MOCK_PROFILES[key] = {
            "contact": {
                "id": contact_id,
                "name": names[idx],
                "email": f"contact@{domains[idx]}",
                "company": "Example Corp",
                "title": "Director",
                "first_contact_date": "2024-08-15T00:00:00",
                "last_contact_date": "2025-06-01T00:00:00",
                "avatar_url": None,
                "relationship_strength": round(0.5 + random.random() * 0.4, 2),
            },
            "interactionCount": random.randint(5, 120),
            "avgResponseHours": round(random.uniform(0.5, 48), 1),
            "totalMonetaryValue": round(random.uniform(1000, 150000), 0),
        }
    return _MOCK_PROFILES[key]


def _get_mock_timeline(contact_id: str, user_id: str) -> List[Dict[str, Any]]:
    key = f"{user_id}:{contact_id}"
    if key not in _MOCK_TIMELINES:
        import random
        subjects = [
            "Q3 Budget Review",
            "Contract Renewal Discussion",
            "Website Redesign Proposal",
            "Meeting reschedule request",
            "Invoice #4421 follow-up",
            "Team offsite planning",
            "Product demo scheduling",
            "Feedback on latest draft",
            "Partnership opportunity",
            "Holiday schedule coordination",
        ]
        statuses = ["active", "resolved", "archived"]
        decisions = ["Approved", "Delegated to Sarah", "Scheduled for next week", None, None]
        items = []
        for i in range(min(15, 5 + hash(key) % 20)):
            month = 1 + (i % 6)
            day = 1 + (hash(f"{key}:{i}") % 28)
            items.append({
                "id": f"thread-{contact_id}-{i}",
                "subject": subjects[hash(f"{key}:{i}") % len(subjects)],
                "date": f"2025-{month:02d}-{day:02d}T10:00:00",
                "messageCount": random.randint(1, 12),
                "decision": decisions[hash(f"{key}:{i}") % len(decisions)],
                "status": statuses[hash(f"{key}:{i}") % len(statuses)],
                "preview": "Hey, just following up on our last conversation about...",
            })
        _MOCK_TIMELINES[key] = sorted(items, key=lambda x: x["date"], reverse=True)
    return _MOCK_TIMELINES[key]


# ---------------------------------------------------------------------------
# Auth dependency (placeholder — integrate with your auth system)
# ---------------------------------------------------------------------------

async def _get_current_user_id() -> str:
    # TODO: Replace with actual JWT auth dependency
    return "demo-user-id"


# ---------------------------------------------------------------------------
# Router
# ---------------------------------------------------------------------------

router = APIRouter(tags=["contacts"])


@router.get(
    "/contacts/{contact_id}/profile",
    response_model=ContactProfileResponse,
    summary="Get contact profile from Neo4j graph",
    description="Returns interaction stats, tone history, projects, and monetary value for a contact.",
)
async def get_contact_profile(
    contact_id: str,
    user_id: str = Depends(_get_current_user_id),
) -> ContactProfileResponse:
    # Try Neo4j first
    raw = await _query_contact_profile(contact_id, user_id)

    if raw is None:
        # Fall back to mock data
        raw = _get_mock_profile(contact_id, user_id)

    contact = raw.get("contact", {})
    contact_id_val = contact.get("id") or contact_id
    name = contact.get("name") or "Unknown"
    email = contact.get("email") or ""

    # Fetch projects
    projects = await _query_contact_projects(contact_id, user_id)
    if not projects:
        projects = ["Website Redesign", "Q3 Planning"]

    # Fetch tone history
    tone_history = await _query_tone_history(contact_id, user_id)
    if not tone_history:
        tone_history = [
            {"date": "2024-08-15", "tone": "professional"},
            {"date": "2024-10-01", "tone": "friendly"},
            {"date": "2024-12-10", "tone": "formal"},
            {"date": "2025-02-20", "tone": "professional"},
            {"date": "2025-05-01", "tone": "friendly"},
        ]

    return ContactProfileResponse(
        id=contact_id_val,
        name=name,
        email=email,
        avatarUrl=contact.get("avatar_url"),
        interactionCount=raw.get("interactionCount", 0),
        avgResponseHours=round(raw.get("avgResponseHours", 0) or 0, 1),
        totalMonetaryValue=round(raw.get("totalMonetaryValue", 0) or 0, 0),
        projects=projects,
        toneHistory=[ToneEntry(**t) for t in tone_history],
        firstContactDate=contact.get("first_contact_date") or "",
        lastContactDate=contact.get("last_contact_date") or "",
        company=contact.get("company"),
        title=contact.get("title"),
        relationshipStrength=contact.get("relationship_strength"),
    )


@router.get(
    "/contacts/{contact_id}/timeline",
    response_model=TimelineResponse,
    summary="Get contact timeline from PostgreSQL",
    description="Returns chronological thread summaries with decision outcomes.",
)
async def get_contact_timeline(
    contact_id: str,
    limit: int = 20,
    cursor: Optional[str] = None,
    user_id: str = Depends(_get_current_user_id),
) -> TimelineResponse:
    # TODO: Replace with actual PostgreSQL query via SQLAlchemy / asyncpg
    # For now, return mock data shaped like real thread summaries
    items = _get_mock_timeline(contact_id, user_id)

    # Apply cursor-based pagination
    start_idx = 0
    if cursor:
        for i, item in enumerate(items):
            if item["id"] == cursor:
                start_idx = i + 1
                break

    paginated = items[start_idx : start_idx + limit]
    has_more = (start_idx + limit) < len(items)
    next_cursor = paginated[-1]["id"] if has_more and paginated else None

    return TimelineResponse(
        items=[ThreadSummaryItem(**item) for item in paginated],
        hasMore=has_more,
        nextCursor=next_cursor,
    )


@router.post(
    "/contacts/{contact_id}/mute",
    status_code=status.HTTP_204_NO_CONTENT,
    summary="Mute a contact",
    description="Suppresses decision cards from this sender.",
)
async def mute_contact(
    contact_id: str,
    user_id: str = Depends(_get_current_user_id),
) -> None:
    # TODO: Insert muted_contact record in PostgreSQL
    logger.info("Muted contact %s for user %s", contact_id, user_id)


@router.post(
    "/contacts/{contact_id}/unmute",
    status_code=status.HTTP_204_NO_CONTENT,
    summary="Unmute a contact",
    description="Re-enables decision cards from this sender.",
)
async def unmute_contact(
    contact_id: str,
    user_id: str = Depends(_get_current_user_id),
) -> None:
    # TODO: Remove muted_contact record from PostgreSQL
    logger.info("Unmuted contact %s for user %s", contact_id, user_id)
