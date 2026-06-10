"""SQLAlchemy models for Intelligence service."""

import uuid
from datetime import datetime
from typing import Optional, List

from sqlalchemy import (
    String, Text, Float, DateTime, Boolean, Integer, JSON, ForeignKey, Index, create_engine
)
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column, relationship
from sqlalchemy.dialects.postgresql import UUID, ARRAY


class Base(DeclarativeBase):
    pass


class User(Base):
    __tablename__ = "users"

    id: Mapped[str] = mapped_column(String(36), primary_key=True)
    email: Mapped[str] = mapped_column(String(255), unique=True, nullable=False)
    name: Mapped[Optional[str]] = mapped_column(String(255), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow)

    sessions: Mapped[List["ChatSessionModel"]] = relationship(back_populates="user", lazy="selectin")
    profiles: Mapped[List["ProfileModel"]] = relationship(back_populates="user", lazy="selectin")


class ProfileModel(Base):
    __tablename__ = "profiles"

    user_id: Mapped[str] = mapped_column(String(36), ForeignKey("users.id"), primary_key=True)
    system_prompt_suffix: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    preferences_json: Mapped[Optional[dict]] = mapped_column(JSON, nullable=True)
    updated_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow, onupdate=datetime.utcnow)

    user: Mapped["User"] = relationship(back_populates="profiles")


class ChatSessionModel(Base):
    __tablename__ = "chat_sessions"

    id: Mapped[str] = mapped_column(String(36), primary_key=True)
    user_id: Mapped[str] = mapped_column(String(36), ForeignKey("users.id"), nullable=False)
    title: Mapped[str] = mapped_column(String(255), default="New Session")
    status: Mapped[str] = mapped_column(String(20), default="active")
    stack_position: Mapped[Optional[int]] = mapped_column(Integer, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow)
    updated_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow, onupdate=datetime.utcnow)
    last_message_at: Mapped[Optional[datetime]] = mapped_column(DateTime, nullable=True)
    metadata_json: Mapped[Optional[dict]] = mapped_column(JSON, nullable=True)

    user: Mapped["User"] = relationship(back_populates="sessions")
    messages: Mapped[List["MessageModel"]] = relationship(back_populates="session", lazy="selectin", order_by="MessageModel.created_at")
    cards: Mapped[List["CardModel"]] = relationship(back_populates="session", lazy="selectin")

    __table_args__ = (Index("idx_sessions_user_id", "user_id"),)


class MessageModel(Base):
    __tablename__ = "messages"

    id: Mapped[str] = mapped_column(String(36), primary_key=True)
    session_id: Mapped[str] = mapped_column(String(36), ForeignKey("chat_sessions.id"), nullable=False)
    sender_type: Mapped[str] = mapped_column(String(20), nullable=False)
    message_type: Mapped[str] = mapped_column(String(20), default="text")
    content_text: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    card_payload_json: Mapped[Optional[dict]] = mapped_column(JSON, nullable=True)
    source_email_id: Mapped[Optional[str]] = mapped_column(String(36), nullable=True)
    cost_usd: Mapped[Optional[float]] = mapped_column(Float, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow)

    session: Mapped["ChatSessionModel"] = relationship(back_populates="messages")

    __table_args__ = (Index("idx_messages_session_id", "session_id"),)


class CardModel(Base):
    __tablename__ = "cards"

    id: Mapped[str] = mapped_column(String(36), primary_key=True)
    message_id: Mapped[Optional[str]] = mapped_column(String(36), ForeignKey("messages.id"), nullable=True)
    session_id: Mapped[Optional[str]] = mapped_column(String(36), ForeignKey("chat_sessions.id"), nullable=True)
    email_id: Mapped[str] = mapped_column(String(36), nullable=False)
    user_id: Mapped[str] = mapped_column(String(36), nullable=False)
    card_type: Mapped[str] = mapped_column(String(20), nullable=False)
    payload_json: Mapped[dict] = mapped_column(JSON, default=dict)
    status: Mapped[str] = mapped_column(String(20), default="pending")
    resolution_json: Mapped[Optional[dict]] = mapped_column(JSON, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow)
    resolved_at: Mapped[Optional[datetime]] = mapped_column(DateTime, nullable=True)
    activated_at: Mapped[Optional[datetime]] = mapped_column(DateTime, nullable=True)

    session: Mapped[Optional["ChatSessionModel"]] = relationship(back_populates="cards")
    decisions: Mapped[List["DecisionModel"]] = relationship(back_populates="card", lazy="selectin")

    __table_args__ = (
        Index("idx_cards_user_id_status", "user_id", "status"),
        Index("idx_cards_session_id", "session_id"),
    )


class DecisionModel(Base):
    __tablename__ = "decisions"

    id: Mapped[str] = mapped_column(String(36), primary_key=True)
    card_id: Mapped[str] = mapped_column(String(36), ForeignKey("cards.id"), nullable=False)
    user_id: Mapped[str] = mapped_column(String(36), nullable=False)
    action_type: Mapped[str] = mapped_column(String(20), nullable=False)
    draft_text: Mapped[Optional[str]] = mapped_column(Text, nullable=True)
    approved_at: Mapped[Optional[datetime]] = mapped_column(DateTime, nullable=True)
    sent_at: Mapped[Optional[datetime]] = mapped_column(DateTime, nullable=True)
    sent_message_id: Mapped[Optional[str]] = mapped_column(String(255), nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=datetime.utcnow)

    card: Mapped["CardModel"] = relationship(back_populates="decisions")

    __table_args__ = (Index("idx_decisions_card_id", "card_id"),)
