"""Initial migration — sessions, messages, cards, decisions, profiles."""

from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

revision = "001"
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "users",
        sa.Column("id", sa.String(36), primary_key=True),
        sa.Column("email", sa.String(255), unique=True, nullable=False),
        sa.Column("name", sa.String(255), nullable=True),
        sa.Column("created_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
    )

    op.create_table(
        "profiles",
        sa.Column("user_id", sa.String(36), sa.ForeignKey("users.id"), primary_key=True),
        sa.Column("system_prompt_suffix", sa.Text(), nullable=True),
        sa.Column("preferences_json", postgresql.JSON(), nullable=True),
        sa.Column("updated_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
    )

    op.create_table(
        "chat_sessions",
        sa.Column("id", sa.String(36), primary_key=True),
        sa.Column("user_id", sa.String(36), sa.ForeignKey("users.id"), nullable=False),
        sa.Column("title", sa.String(255), nullable=False, server_default="New Session"),
        sa.Column("status", sa.String(20), nullable=False, server_default="active"),
        sa.Column("stack_position", sa.Integer(), nullable=True),
        sa.Column("created_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
        sa.Column("updated_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
        sa.Column("last_message_at", sa.DateTime(), nullable=True),
        sa.Column("metadata_json", postgresql.JSON(), nullable=True),
    )
    op.create_index("idx_sessions_user_id", "chat_sessions", ["user_id"])

    op.create_table(
        "messages",
        sa.Column("id", sa.String(36), primary_key=True),
        sa.Column("session_id", sa.String(36), sa.ForeignKey("chat_sessions.id"), nullable=False),
        sa.Column("sender_type", sa.String(20), nullable=False),
        sa.Column("message_type", sa.String(20), nullable=False, server_default="text"),
        sa.Column("content_text", sa.Text(), nullable=True),
        sa.Column("card_payload_json", postgresql.JSON(), nullable=True),
        sa.Column("source_email_id", sa.String(36), nullable=True),
        sa.Column("cost_usd", sa.Float(), nullable=True),
        sa.Column("created_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
    )
    op.create_index("idx_messages_session_id", "messages", ["session_id"])

    op.create_table(
        "cards",
        sa.Column("id", sa.String(36), primary_key=True),
        sa.Column("message_id", sa.String(36), sa.ForeignKey("messages.id"), nullable=True),
        sa.Column("session_id", sa.String(36), sa.ForeignKey("chat_sessions.id"), nullable=True),
        sa.Column("email_id", sa.String(36), nullable=False),
        sa.Column("user_id", sa.String(36), nullable=False),
        sa.Column("card_type", sa.String(20), nullable=False),
        sa.Column("payload_json", postgresql.JSON(), nullable=False, server_default="{}"),
        sa.Column("status", sa.String(20), nullable=False, server_default="pending"),
        sa.Column("resolution_json", postgresql.JSON(), nullable=True),
        sa.Column("created_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
        sa.Column("resolved_at", sa.DateTime(), nullable=True),
        sa.Column("activated_at", sa.DateTime(), nullable=True),
    )
    op.create_index("idx_cards_user_id_status", "cards", ["user_id", "status"])
    op.create_index("idx_cards_session_id", "cards", ["session_id"])

    op.create_table(
        "decisions",
        sa.Column("id", sa.String(36), primary_key=True),
        sa.Column("card_id", sa.String(36), sa.ForeignKey("cards.id"), nullable=False),
        sa.Column("user_id", sa.String(36), nullable=False),
        sa.Column("action_type", sa.String(20), nullable=False),
        sa.Column("draft_text", sa.Text(), nullable=True),
        sa.Column("approved_at", sa.DateTime(), nullable=True),
        sa.Column("sent_at", sa.DateTime(), nullable=True),
        sa.Column("sent_message_id", sa.String(255), nullable=True),
        sa.Column("created_at", sa.DateTime(), nullable=False, server_default=sa.func.now()),
    )
    op.create_index("idx_decisions_card_id", "decisions", ["card_id"])


def downgrade() -> None:
    op.drop_index("idx_decisions_card_id", table_name="decisions")
    op.drop_table("decisions")
    op.drop_index("idx_cards_session_id", table_name="cards")
    op.drop_index("idx_cards_user_id_status", table_name="cards")
    op.drop_table("cards")
    op.drop_index("idx_messages_session_id", table_name="messages")
    op.drop_table("messages")
    op.drop_index("idx_sessions_user_id", table_name="chat_sessions")
    op.drop_table("chat_sessions")
    op.drop_table("profiles")
    op.drop_table("users")
