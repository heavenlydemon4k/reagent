"""NATS JetStream consumer for Intelligence service.

Subscribes to email.classified events from Classification service.
Creates decision cards for critical emails.
"""

import asyncio
import json
import os
from typing import Optional

import nats
from nats.js.api import ConsumerConfig, DeliverPolicy

from intelligence.app.decision_stack.service import DecisionStackService
from intelligence.app.email_kb.service import EmailKnowledgeBase, EmailContext


class IntelligenceNatsConsumer:
    """Consumes NATS events and routes to appropriate handlers."""

    def __init__(self):
        self.nc: Optional[nats.NATS] = None
        self.js = None
        self.sub = None
        self.stack = DecisionStackService()
        self.kb = EmailKnowledgeBase()

    async def connect(self):
        nats_url = os.getenv("NATS_URL", "nats://localhost:4222")
        self.nc = await nats.connect(nats_url, name="intelligence-consumer")
        self.js = self.nc.jetstream()

    async def subscribe(self):
        """Subscribe to email.classified topic."""
        try:
            await self.js.add_stream(
                name="EMAIL_CLASSIFIED",
                subjects=["email.classified"],
                max_msgs=-1,
                max_bytes=-1,
            )
        except nats.js.errors.BadRequestError:
            pass

        config = ConsumerConfig(
            durable_name="intelligence-classified-consumer",
            deliver_policy=DeliverPolicy.ALL,
            ack_wait=30,
            max_deliver=5,
        )

        self.sub = await self.js.subscribe(
            "email.classified",
            durable="intelligence-classified-consumer",
            config=config,
            cb=self._on_message,
        )

    async def _on_message(self, msg):
        try:
            data = json.loads(msg.data.decode())
            user_id = data.get("user_id")
            email_id = data.get("email_id")
            classification = data.get("classification")
            reason = data.get("reason", "")

            if classification != "stack":
                await msg.ack()
                return

            email = EmailContext(
                email_id=email_id,
                subject=data.get("subject", ""),
                from_address=data.get("from_address", ""),
                to_addresses=data.get("to_addresses", []),
                body_text=data.get("body_text", ""),
                received_at=data.get("received_at", ""),
                thread_id=data.get("thread_id"),
                labels=data.get("labels", []),
                score=data.get("score", 0.0),
            )

            card = await self.stack.receive_critical_email(user_id, email)
            print(f"[NATS] Created card {card.id} for email {email_id} (user {user_id})")
            await msg.ack()
        except Exception as e:
            print(f"[NATS] Error processing message: {e}")
            await msg.nak()

    async def close(self):
        if self.sub:
            await self.sub.unsubscribe()
        if self.nc:
            await self.nc.close()
