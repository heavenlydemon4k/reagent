"""Add scheduled_at and status to drafts table.

Revision ID: 004
Revises: 002
Create Date: 2024-01-04 00:00:00.000000+00:00

"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

# revision identifiers, used by Alembic.
revision = '004'
down_revision = '002'
branch_labels = None
depends_on = None


def upgrade():
    # Add scheduled_at — when the draft is scheduled to be sent
    op.add_column(
        'drafts',
        sa.Column('scheduled_at', sa.TIMESTAMP(timezone=True), nullable=True),
    )
    # Add sent_at — when the draft was actually sent
    op.add_column(
        'drafts',
        sa.Column('sent_at', sa.TIMESTAMP(timezone=True), nullable=True),
    )
    # Add status — draft lifecycle state
    op.add_column(
        'drafts',
        sa.Column(
            'status',
            sa.String(20),
            sa.CheckConstraint(
                "status IN ('pending', 'scheduled', 'sent', 'cancelled')",
                name='ck_drafts_status',
            ),
            server_default='pending',
            nullable=True,
        ),
    )
    # Composite index for the cron job: efficiently find due scheduled drafts
    op.create_index(
        'idx_drafts_scheduled',
        'drafts',
        ['status', sa.text('scheduled_at ASC')],
        postgresql_where=sa.text("status = 'scheduled'"),
    )


def downgrade():
    op.drop_index('idx_drafts_scheduled', table_name='drafts')
    op.drop_column('drafts', 'status')
    op.drop_column('drafts', 'sent_at')
    op.drop_column('drafts', 'scheduled_at')
