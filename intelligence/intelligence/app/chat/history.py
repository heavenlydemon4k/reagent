"""
Conversation History — PostgreSQL-backed CRUD for chat.

Manages the lifecycle of conversations and their messages using asyncpg.
Conversations are scoped per-user and support auto-generated titles from
the first user message.
"""

from __future__ import annotations

import logging
from typing import List, Optional
from uuid import UUID, uuid4

from intelligence.app.chat.models import (
    ChatMessage,
    Conversation,
    ConversationListItem,
)
from intelligence.core.config import get_settings

logger = logging.getLogger(__name__)


class ConversationHistory:
    """PostgreSQL-backed conversation storage."""

    def __init__(self, pool) -> None:
        self.pool = pool

    # ------------------------------------------------------------------
    # Conversation lifecycle
    # ------------------------------------------------------------------

    async def get_or_create(
        self,
        user_id: str,
        conversation_id: Optional[str] = None,
        title: Optional[str] = None,
    ) -> Conversation:
        """
        Get an existing conversation or create a new one.

        Args:
            user_id: The owner of the conversation.
            conversation_id: Optional existing conversation UUID. If None,
                a new conversation is created.
            title: Optional title for new conversations. Auto-generated
                from first message if not provided.

        Returns:
            A Conversation model (with messages loaded if existing).
        """
        if conversation_id:
            conv = await self.get_conversation(conversation_id)
            if conv and str(conv.user_id) == user_id:
                return conv
            # User mismatch or not found — fall through to create new
            logger.warning(
                "Conversation %s not found or user mismatch; creating new.",
                conversation_id,
            )

        # Create new conversation
        conv_id = uuid4()
        now = __import__("datetime").datetime.utcnow()

        async with self.pool.acquire() as conn:
            await conn.execute(
                """
                INSERT INTO conversations (id, user_id, title, created_at, updated_at)
                VALUES ($1, $2, $3, $4, $5)
                """,
                str(conv_id),
                user_id,
                title or "New Conversation",
                now,
                now,
            )

        return Conversation(
            id=conv_id,
            user_id=UUID(user_id) if isinstance(user_id, str) else user_id,
            title=title or "New Conversation",
            created_at=now,
            updated_at=now,
        )

    async def get_conversation(self, conversation_id: str) -> Optional[Conversation]:
        """
        Load a conversation by ID, including all messages.

        Args:
            conversation_id: UUID of the conversation.

        Returns:
            Conversation model with messages, or None if not found.
        """
        async with self.pool.acquire() as conn:
            # Load conversation row
            row = await conn.fetchrow(
                """
                SELECT id, user_id, title, created_at, updated_at
                FROM conversations WHERE id = $1
                """,
                conversation_id,
            )
            if not row:
                return None

            # Load messages
            msg_rows = await conn.fetch(
                """
                SELECT id, conversation_id, role, content, audio_url,
                       transcription, citations, model_used, tokens_used, created_at
                FROM chat_messages
                WHERE conversation_id = $1
                ORDER BY created_at ASC
                """,
                conversation_id,
            )

            messages = []
            for mr in msg_rows:
                messages.append(
                    ChatMessage(
                        id=UUID(mr["id"]),
                        conversation_id=UUID(mr["conversation_id"]),
                        role=mr["role"],
                        content=mr["content"],
                        audio_url=mr["audio_url"],
                        transcription=mr["transcription"],
                        citations=mr["citations"] or [],
                        model_used=mr["model_used"],
                        tokens_used=mr["tokens_used"],
                        created_at=mr["created_at"],
                    )
                )

            return Conversation(
                id=UUID(row["id"]),
                user_id=UUID(row["user_id"]),
                title=row["title"],
                messages=messages,
                created_at=row["created_at"],
                updated_at=row["updated_at"],
            )

    # ------------------------------------------------------------------
    # Messages
    # ------------------------------------------------------------------

    async def add_message(self, message: ChatMessage) -> ChatMessage:
        """
        Persist a chat message to PostgreSQL.

        Also updates the conversation's updated_at timestamp and
        auto-generates the title from the first user message if needed.
        """
        async with self.pool.acquire() as conn:
            async with conn.transaction():
                # Insert message
                await conn.execute(
                    """
                    INSERT INTO chat_messages (
                        id, conversation_id, role, content, audio_url,
                        transcription, citations, model_used, tokens_used, created_at
                    ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
                    """,
                    str(message.id),
                    str(message.conversation_id),
                    message.role,
                    message.content,
                    message.audio_url,
                    message.transcription,
                    message.citations,
                    message.model_used,
                    message.tokens_used,
                    message.created_at,
                )

                # Update conversation timestamp
                now = __import__("datetime").datetime.utcnow()
                await conn.execute(
                    """
                    UPDATE conversations
                    SET updated_at = $1
                    WHERE id = $2
                    """,
                    now,
                    str(message.conversation_id),
                )

                # Auto-generate title from first user message
                if message.role == "user":
                    await self._maybe_set_title(conn, message)

        return message

    async def _maybe_set_title(self, conn, message: ChatMessage) -> None:
        """
        Set the conversation title from the first user message if
        the title is still the default.
        """
        row = await conn.fetchrow(
            """
            SELECT title FROM conversations WHERE id = $1
            """,
            str(message.conversation_id),
        )
        if row and row["title"] in ("New Conversation", None, ""):
            # Use first ~40 chars of message as title
            title = message.content[:40].strip()
            if len(message.content) > 40:
                title += "..."
            if title:
                await conn.execute(
                    """
                    UPDATE conversations SET title = $1 WHERE id = $2
                    """,
                    title,
                    str(message.conversation_id),
                )

    # ------------------------------------------------------------------
    # Listing
    # ------------------------------------------------------------------

    async def list_conversations(self, user_id: str) -> List[ConversationListItem]:
        """
        List all conversations for a user with metadata.

        Returns lightweight ConversationListItem objects with message count
        and last message preview.
        """
        async with self.pool.acquire() as conn:
            rows = await conn.fetch(
                """
                SELECT
                    c.id,
                    c.title,
                    c.updated_at,
                    COUNT(m.id) AS message_count,
                    (
                        SELECT content FROM chat_messages
                        WHERE conversation_id = c.id
                        ORDER BY created_at DESC LIMIT 1
                    ) AS last_message
                FROM conversations c
                LEFT JOIN chat_messages m ON m.conversation_id = c.id
                WHERE c.user_id = $1
                GROUP BY c.id, c.title, c.updated_at
                ORDER BY c.updated_at DESC
                """,
                user_id,
            )

            return [
                ConversationListItem(
                    id=UUID(row["id"]),
                    title=row["title"] or "Untitled",
                    message_count=row["message_count"],
                    last_message_preview=(row["last_message"] or "")[:100],
                    updated_at=row["updated_at"],
                )
                for row in rows
            ]

    # ------------------------------------------------------------------
    # Schema helpers
    # ------------------------------------------------------------------

    @staticmethod
    async def init_schema(pool) -> None:
        """
        Create the conversations and chat_messages tables if they do not exist.

        Called during application startup.
        """
        async with pool.acquire() as conn:
            await conn.execute(
                """
                CREATE TABLE IF NOT EXISTS conversations (
                    id UUID PRIMARY KEY,
                    user_id UUID NOT NULL,
                    title TEXT,
                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
                    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
                )
                """
            )
            await conn.execute(
                """
                CREATE INDEX IF NOT EXISTS idx_conversations_user_id
                ON conversations (user_id)
                """
            )
            await conn.execute(
                """
                CREATE TABLE IF NOT EXISTS chat_messages (
                    id UUID PRIMARY KEY,
                    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
                    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
                    content TEXT NOT NULL,
                    audio_url TEXT,
                    transcription TEXT,
                    citations JSONB DEFAULT '[]',
                    model_used TEXT,
                    tokens_used INTEGER,
                    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
                )
                """
            )
            await conn.execute(
                """
                CREATE INDEX IF NOT EXISTS idx_chat_messages_conversation_id
                ON chat_messages (conversation_id)
                """
            )
            await conn.execute(
                """
                CREATE INDEX IF NOT EXISTS idx_chat_messages_created_at
                ON chat_messages (created_at)
                """
            )
        logger.info("Conversation history schema initialized.")
