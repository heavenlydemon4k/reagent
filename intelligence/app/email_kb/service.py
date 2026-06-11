"""Email knowledgebase queries — Qdrant semantic search + Neo4j graph."""

import os
from typing import List, Optional, Dict, Any
from dataclasses import dataclass

from qdrant_client import QdrantClient
from qdrant_client.models import Filter, FieldCondition, MatchValue
from neo4j import GraphDatabase
from openai import OpenAI

from intelligence.core import LLMClient


@dataclass
class EmailContext:
    email_id: str
    subject: str
    from_address: str
    to_addresses: List[str]
    body_text: str
    received_at: str
    thread_id: Optional[str]
    labels: List[str]
    score: float


class EmailKnowledgeBase:
    """Queries the email knowledgebase for agent context."""

    def __init__(
        self,
        qdrant_url: str = None,
        neo4j_uri: str = None,
        neo4j_user: str = None,
        neo4j_password: str = None,
        collection: str = "emails",
    ):
        self.qdrant = QdrantClient(url=qdrant_url or os.getenv("QDRANT_URL", "http://localhost:6333"))
        self.neo4j = GraphDatabase.driver(
            neo4j_uri or os.getenv("NEO4J_URI", "bolt://localhost:7687"),
            auth=(
                neo4j_user or os.getenv("NEO4J_USER", "neo4j"),
                neo4j_password or os.getenv("NEO4J_PASSWORD", "reagent"),
            ),
        )
        self.collection = collection
        self.llm = LLMClient()
        self._openai = OpenAI(api_key=os.getenv("OPENAI_API_KEY"))

    def semantic_search(
        self,
        user_id: str,
        query: str,
        limit: int = 10,
        filters: Optional[Dict[str, Any]] = None,
    ) -> List[EmailContext]:
        embedding = self._embed(query)
        qdrant_filter = Filter(
            must=[FieldCondition(key="user_id", match=MatchValue(value=user_id))]
        )
        if filters:
            for key, value in filters.items():
                qdrant_filter.must.append(FieldCondition(key=key, match=MatchValue(value=value)))

        results = self.qdrant.search(
            collection_name=self.collection,
            query_vector=embedding,
            query_filter=qdrant_filter,
            limit=limit,
            with_payload=True,
        )
        return [
            EmailContext(
                email_id=r.payload["email_id"],
                subject=r.payload.get("subject", ""),
                from_address=r.payload.get("from_address", ""),
                to_addresses=r.payload.get("to_addresses", []),
                body_text=r.payload.get("body_text", ""),
                received_at=r.payload.get("received_at", ""),
                thread_id=r.payload.get("thread_id"),
                labels=r.payload.get("labels", []),
                score=r.score,
            )
            for r in results
        ]

    def thread_context(self, email_id: str) -> List[EmailContext]:
        query = """
        MATCH (e:Email {id: $email_id})-[:BELONGS_TO]->(t:Thread)<-[:BELONGS_TO]-(other:Email)
        RETURN other.id as email_id, other.subject as subject, other.from as from_address,
               other.to as to_addresses, other.body_text as body_text, other.received_at as received_at,
               other.thread_id as thread_id, other.labels as labels
        ORDER BY other.received_at
        """
        with self.neo4j.session() as session:
            result = session.run(query, email_id=email_id)
            return [
                EmailContext(
                    email_id=r["email_id"], subject=r["subject"], from_address=r["from_address"],
                    to_addresses=r["to_addresses"], body_text=r["body_text"], received_at=r["received_at"],
                    thread_id=r["thread_id"], labels=r["labels"], score=1.0,
                )
                for r in result
            ]

    def contact_emails(self, user_id: str, contact_email: str, limit: int = 20) -> List[EmailContext]:
        query = """
        MATCH (u:User {id: $user_id})-[:HAS_EMAIL]->(e:Email)
        WHERE e.from = $contact OR $contact IN e.to
        RETURN e.id as email_id, e.subject as subject, e.from as from_address,
               e.to as to_addresses, e.body_text as body_text, e.received_at as received_at,
               e.thread_id as thread_id, e.labels as labels
        ORDER BY e.received_at DESC LIMIT $limit
        """
        with self.neo4j.session() as session:
            result = session.run(query, user_id=user_id, contact=contact_email, limit=limit)
            return [
                EmailContext(
                    email_id=r["email_id"], subject=r["subject"], from_address=r["from_address"],
                    to_addresses=r["to_addresses"], body_text=r["body_text"], received_at=r["received_at"],
                    thread_id=r["thread_id"], labels=r["labels"], score=1.0,
                )
                for r in result
            ]

    def summarize_for_agent(self, contexts: List[EmailContext]) -> str:
        lines = []
        for ctx in contexts:
            snippet = ctx.body_text[:500].replace("\n", " ")
            lines.append(
                f"[Email {ctx.email_id}] From: {ctx.from_address} | Subject: {ctx.subject} | "
                f"Date: {ctx.received_at}\n{snippet}"
            )
        return "\n".join(lines)

    def _embed(self, text: str) -> List[float]:
        """Real OpenAI embedding via text-embedding-3-small."""
        try:
            resp = self._openai.embeddings.create(
                model="text-embedding-3-small",
                input=text[:8000],
            )
            return resp.data[0].embedding
        except Exception:
            return [0.0] * 1536

    def close(self):
        self.neo4j.close()
        self.llm.close()
