"""Add send-related fields to decisions table."""

from alembic import op
import sqlalchemy as sa

revision = "002"
down_revision = "001"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column("decisions", sa.Column("to_address", sa.String(255), nullable=True))
    op.add_column("decisions", sa.Column("subject", sa.String(500), nullable=True))
    op.add_column("decisions", sa.Column("thread_id", sa.String(36), nullable=True))
    op.add_column("decisions", sa.Column("account_id", sa.String(36), nullable=True))


def downgrade() -> None:
    op.drop_column("decisions", "account_id")
    op.drop_column("decisions", "thread_id")
    op.drop_column("decisions", "subject")
    op.drop_column("decisions", "to_address")
