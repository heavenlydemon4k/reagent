"""NATS JetStream consumer for Intelligence service.

Subscribes to intelligence.compress events from Classification service and
runs the chunk → embed → index → generate_card pipeline. Each event carries
{user_id, thread_id, raw_email_ids}; the consumer fetches email bodies from
the shared Postgres database and indexes them into Qdrant before calling
CompressionService.generate_card().
"""

import asyncio
import json
import logging
import os
from datetime import datetime
from typing import Optional
from uuid import UUID

import nats
from nats.js.api import ConsumerConfig, DeliverPolicy
from qdrant_client import QdrantClient
from sqlalchemy import text

from intelligence.app.compression.chunker import SemanticChunker
from intelligence.app.compression.embedder import Embedder
from intelligence.app.compression.service import CompressionService
from intelligence.app.compression.store import ChunkStore
from intelligence.app.db import db_session
from intelligence.core.fallback_chain import FallbackChain
from intelligence.infra.db.neo4j_client import Neo4jClient
from intelligence.infra.db.postgres_client import PostgresClient
from intelligence.infra.queue.nats_client import NATSClient

logger = logging.getLogger(__name__)


class IntelligenceNatsConsumer:
    """Consumes intelligence.compress events and runs the card-generation pipeline."""

    SUBJECT = "intelligence.compress"
    DURABLE_NAME = "intelligence-compress-consumer"

    def __init__(self) -> None:
        self.nc: Optional[nats.NATS] = None
        self.js = None
        self.sub = None
        self._compression: Optional[CompressionService] = None
        self._chunker: Optional[SemanticChunker] = None
        self._embedder: Optional[Embedder] = None
        self._chunk_store: Optional[ChunkStore] = None

    async def connect(self) -> None:
        nats_url = os.getenv("NATS_URL", "nats://localhost:4222")
        self.nc = await nats.connect(nats_url, name="intelligence-consumer")
        self.js = self.nc.jetstream()

        qdrant_url = os.getenv("QDRANT_URL", "http://localhost:6333")
        qdrant_client = QdrantClient(url=qdrant_url)
        self._chunk_store = ChunkStore(qdrant_client=qdrant_client)
        self._chunker = SemanticChunker()
        self._embedder = Embedder()

        template_path = os.path.join(
            os.path.dirname(__file__), "..", "core", "prompt_templates", "compression.jinja2"
        )
        with open(template_path) as f:
            prompt_template = f.read()

        self._compression = CompressionService(
            llm=FallbackChain(),
            chunk_store=self._chunk_store,
            neo4j=Neo4jClient(),
            postgres=PostgresClient(),
            nats=NATSClient(),
            prompt_template=prompt_template,
        )

        logger.info("IntelligenceNatsConsumer connected: nats=%s qdrant=%s", nats_url, qdrant_url)

    async def subscribe(self) -> None:
        config = ConsumerConfig(
            durable_name=self.DURABLE_NAME,
            deliver_policy=DeliverPolicy.ALL,
            ack_wait=120,
            max_deliver=5,
        )

        self.sub = await self.js.subscribe(
            self.SUBJECT,
            durable=self.DURABLE_NAME,
            config=config,
            cb=self._on_message,
        )
        logger.info("Subscribed to %s (durable=%s)", self.SUBJECT, self.DURABLE_NAME)

    async def _on_message(self, msg: nats.aio.msg.Msg) -> None:
        try:
            data = json.loads(msg.data.decode())
            # Accept both camelCase (Go JSON) and snake_case keys
            user_id: str = data.get("user_id") or data.get("UserID", "")
            thread_id: str = data.get("thread_id") or data.get("ThreadID", "")
            raw_email_ids: list = data.get("raw_email_ids") or data.get("RawEmailIDs", [])

            if not user_id or not thread_id or not raw_email_ids:
                logger.warning(
                    "intelligence.compress message missing required fields; skipping. keys=%s",
                    list(data.keys()),
                )
                await msg.ack()
                return

            raw_email_ids_str = [str(eid) for eid in raw_email_ids]

            # Step 1: fetch emails, chunk, embed, index into Qdrant
            await self._index_emails(user_id, thread_id, raw_email_ids_str)

            # Step 2: generate decision card via compression pipeline
            result = await self._compression.generate_card(
                user_id=user_id,
                thread_id=thread_id,
                raw_email_ids=raw_email_ids_str,
            )

            if result.routed_to_manual_review:
                logger.warning(
                    "Card routed to manual review: thread=%s reason=%s",
                    thread_id,
                    result.routing_reason,
                )
            else:
                card_id = result.card.id if result.card else "none"
                logger.info("Card generated: thread=%s card=%s", thread_id, card_id)

            await msg.ack()

        except Exception as exc:
            logger.exception("Error processing intelligence.compress message: %s", exc)
            await msg.nak()

    async def _index_emails(
        self, user_id: str, thread_id: str, raw_email_ids: list[str]
    ) -> None:
        """Fetch emails from Postgres, chunk, embed, and upsert to Qdrant."""
        if not raw_email_ids:
            return

        # Named bind params for safe IN query (asyncpg / SQLAlchemy text)
        id_params = {f"id_{i}": eid for i, eid in enumerate(raw_email_ids)}
        placeholders = ", ".join(f":id_{i}" for i in range(len(raw_email_ids)))
        sql = text(
            f"SELECT id, subject, from_address, body_text, received_at "
            f"FROM raw_emails WHERE id::text IN ({placeholders})"
        )

        async with db_session() as db:
            result = await db.execute(sql, id_params)
            rows = result.fetchall()

        if not rows:
            logger.warning(
                "No raw_emails found for ids=%s; Qdrant index will be empty for thread=%s",
                raw_email_ids,
                thread_id,
            )
            return

        # Chunk each email body
        all_chunks = []
        for row in rows:
            email_id_str, subject, from_address, body_text, received_at = row
            if not body_text:
                continue
            received_dt = (
                received_at if isinstance(received_at, datetime) else datetime.utcnow()
            )
            chunks = self._chunker.chunk_email(
                email_id=UUID(str(email_id_str)),
                thread_id=UUID(thread_id),
                user_id=UUID(user_id),
                sender_email=from_address or "",
                body_text=body_text,
                received_at=received_dt,
            )
            all_chunks.extend(chunks)

        if not all_chunks:
            logger.warning("No chunks produced for thread=%s (empty bodies?)", thread_id)
            return

        # Embed all chunks and upsert to Qdrant
        texts = [c.content for c in all_chunks]
        embeddings = await self._embedder.embed(texts)
        upserted = await self._chunk_store.upsert_chunks(all_chunks, embeddings)
        logger.info("Indexed %d chunks for thread=%s user=%s", upserted, thread_id, user_id)

    async def close(self) -> None:
        if self.sub:
            await self.sub.unsubscribe()
        if self.nc:
            await self.nc.close()
