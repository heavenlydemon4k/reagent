"""
Initial schema for Decision Stack — Intelligence service.

Creates all 10 tables with constraints, indexes, and foreign keys.
Uses gen_random_uuid() as proxy for UUIDv7.

Revision ID: 001
Revises:
Create Date: 2024-01-01 00:00:00.000000+00:00

"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

# revision identifiers, used by Alembic.
revision = '001'
down_revision = None
branch_labels = None
depends_on = None


def upgrade():
    # Enable pgcrypto extension
    op.execute("CREATE EXTENSION IF NOT EXISTS pgcrypto")

    # 1. users table
    op.create_table(
        'users',
        sa.Column('id', postgresql.UUID(), server_default=sa.text('gen_random_uuid()'), nullable=False),
        sa.Column('email', sa.VARCHAR(255), nullable=False),
        sa.Column('name', sa.VARCHAR(255), nullable=True),
        sa.Column('timezone', sa.VARCHAR(50), server_default='America/New_York', nullable=True),
        sa.Column('billing_plan', sa.VARCHAR(20), sa.CheckConstraint("billing_plan IN ('weekly', 'monthly')", name='ck_users_billing_plan'), nullable=True),
        sa.Column('billing_status', sa.VARCHAR(20), server_default='active', nullable=True),
        sa.Column('data_residency', sa.VARCHAR(20), server_default='us-east-1', nullable=True),
        sa.Column('created_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.Column('voice_calibrated_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('onboarded_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('encryption_key_id', sa.VARCHAR(255), nullable=False),
        sa.PrimaryKeyConstraint('id', name='pk_users'),
        sa.UniqueConstraint('email', name='uq_users_email'),
    )

    # 2. email_accounts table
    op.create_table(
        'email_accounts',
        sa.Column('id', postgresql.UUID(), server_default=sa.text('gen_random_uuid()'), nullable=False),
        sa.Column('user_id', postgresql.UUID(), nullable=False),
        sa.Column('provider', sa.VARCHAR(20), sa.CheckConstraint("provider IN ('gmail', 'outlook', 'exchange')", name='ck_email_accounts_provider'), nullable=True),
        sa.Column('email_address', sa.VARCHAR(255), nullable=False),
        sa.Column('refresh_token_enc', postgresql.BYTEA(), nullable=False),
        sa.Column('access_token_enc', postgresql.BYTEA(), nullable=True),
        sa.Column('token_expires_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('scope_granted', postgresql.ARRAY(sa.Text()), nullable=False),
        sa.Column('history_id', sa.VARCHAR(255), nullable=True),
        sa.Column('delta_link', sa.Text(), nullable=True),
        sa.Column('is_active', sa.Boolean(), server_default='true', nullable=True),
        sa.Column('last_sync_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('created_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.PrimaryKeyConstraint('id', name='pk_email_accounts'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
        sa.UniqueConstraint('user_id', 'email_address', name='uq_email_accounts_user_email'),
    )

    # 3. threads table
    op.create_table(
        'threads',
        sa.Column('id', postgresql.UUID(), server_default=sa.text('gen_random_uuid()'), nullable=False),
        sa.Column('user_id', postgresql.UUID(), nullable=False),
        sa.Column('thread_key', sa.VARCHAR(255), nullable=False),
        sa.Column('source_account_id', postgresql.UUID(), nullable=False),
        sa.Column('subject', sa.Text(), nullable=True),
        sa.Column('participant_emails', postgresql.ARRAY(sa.Text()), nullable=False),
        sa.Column('message_count', sa.Integer(), server_default='0', nullable=True),
        sa.Column('last_message_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('status', sa.VARCHAR(20), sa.CheckConstraint("status IN ('active', 'resolved', 'archived')", name='ck_threads_status'), server_default='active', nullable=True),
        sa.Column('created_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.PrimaryKeyConstraint('id', name='pk_threads'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['source_account_id'], ['email_accounts.id']),
        sa.UniqueConstraint('user_id', 'thread_key', name='uq_threads_user_thread_key'),
    )

    # 4. raw_emails table
    op.create_table(
        'raw_emails',
        sa.Column('id', postgresql.UUID(), server_default=sa.text('gen_random_uuid()'), nullable=False),
        sa.Column('thread_id', postgresql.UUID(), nullable=False),
        sa.Column('user_id', postgresql.UUID(), nullable=False),
        sa.Column('source_account_id', postgresql.UUID(), nullable=False),
        sa.Column('message_id', sa.VARCHAR(255), nullable=False),
        sa.Column('in_reply_to', sa.VARCHAR(255), nullable=True),
        sa.Column('references', postgresql.ARRAY(sa.Text()), nullable=True),
        sa.Column('sender_email', sa.VARCHAR(255), nullable=False),
        sa.Column('sender_name', sa.VARCHAR(255), nullable=True),
        sa.Column('recipient_emails', postgresql.ARRAY(sa.Text()), nullable=False),
        sa.Column('subject', sa.Text(), nullable=True),
        sa.Column('body_text', sa.Text(), nullable=True),
        sa.Column('body_html', sa.Text(), nullable=True),
        sa.Column('has_attachments', sa.Boolean(), server_default='false', nullable=True),
        sa.Column('attachment_s3_uris', postgresql.ARRAY(sa.Text()), nullable=True),
        sa.Column('extracted_codes', postgresql.ARRAY(sa.Text()), nullable=True),
        sa.Column('received_at', sa.TIMESTAMP(timezone=True), nullable=False),
        sa.Column('parsed_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.Column('retention_until', sa.TIMESTAMP(timezone=True), nullable=False),
        sa.Column('classification', sa.VARCHAR(20), sa.CheckConstraint("classification IN ('extract', 'auto', 'decision', 'pending')", name='ck_raw_emails_classification'), nullable=True),
        sa.PrimaryKeyConstraint('id', name='pk_raw_emails'),
        sa.ForeignKeyConstraint(['thread_id'], ['threads.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['source_account_id'], ['email_accounts.id']),
        sa.UniqueConstraint('message_id', name='uq_raw_emails_message_id'),
    )

    # Indexes for raw_emails
    op.create_index('idx_raw_emails_user_received', 'raw_emails', ['user_id', sa.text('received_at DESC')])
    op.create_index('idx_raw_emails_thread', 'raw_emails', ['thread_id', sa.text('received_at DESC')])

    # 5. decision_cards table
    op.create_table(
        'decision_cards',
        sa.Column('id', postgresql.UUID(), server_default=sa.text('gen_random_uuid()'), nullable=False),
        sa.Column('user_id', postgresql.UUID(), nullable=False),
        sa.Column('thread_id', postgresql.UUID(), nullable=False),
        sa.Column('source_account_id', postgresql.UUID(), nullable=False),
        sa.Column('card_state', sa.VARCHAR(20), sa.CheckConstraint("card_state IN ('pending', 'consulting', 'drafting', 'approved', 'sent', 'archived', 'expired')", name='ck_decision_cards_card_state'), server_default='pending', nullable=True),
        sa.Column('from_field', postgresql.JSONB(), nullable=False),
        sa.Column('they_want', sa.Text(), nullable=False),
        sa.Column('context', postgresql.JSONB(), nullable=False),
        sa.Column('need_from_user', sa.Text(), nullable=False),
        sa.Column('chunk_citations', postgresql.JSONB(), server_default='[]', nullable=False),
        sa.Column('urgency_score', sa.Float(), sa.CheckConstraint('urgency_score >= 0.0 AND urgency_score <= 1.0', name='ck_decision_cards_urgency_score'), server_default='0.0', nullable=True),
        sa.Column('auto_handle_rule_id', postgresql.UUID(), nullable=True),
        sa.Column('classification_confidence', sa.Float(), nullable=True),
        sa.Column('suggested_deadline', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('user_decided_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('sent_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('created_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.Column('updated_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.PrimaryKeyConstraint('id', name='pk_decision_cards'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['thread_id'], ['threads.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['source_account_id'], ['email_accounts.id']),
    )

    # Indexes for decision_cards
    op.create_index('idx_cards_user_state', 'decision_cards', ['user_id', 'card_state', sa.text('created_at DESC')])
    op.create_index('idx_cards_urgency', 'decision_cards', ['user_id', 'card_state', sa.text('urgency_score DESC')], postgresql_where=sa.text("card_state = 'pending'"))

    # 6. auto_handle_rules table (owned by classification service)
    # NOTE: The canonical table definition is in classification/migrations/001_initial_schema.up.sql.
    #       Intelligence service does NOT create this table — it is created by the classification
    #       service's migration. The decision_cards table below references auto_handle_rules
    #       via auto_handle_rule_id without an enforced FK to avoid cross-service migration
    #       ordering dependencies.

    # 7. drafts table
    op.create_table(
        'drafts',
        sa.Column('id', postgresql.UUID(), server_default=sa.text('gen_random_uuid()'), nullable=False),
        sa.Column('card_id', postgresql.UUID(), nullable=False),
        sa.Column('user_id', postgresql.UUID(), nullable=False),
        sa.Column('thread_id', postgresql.UUID(), nullable=False),
        sa.Column('draft_body', sa.Text(), nullable=False),
        sa.Column('subject_line', sa.Text(), nullable=True),
        sa.Column('tone_profile', sa.VARCHAR(50), nullable=True),
        sa.Column('in_reply_to', sa.VARCHAR(255), nullable=True),
        sa.Column('references', postgresql.ARRAY(sa.Text()), nullable=True),
        sa.Column('model_used', sa.VARCHAR(50), nullable=True),
        sa.Column('tokens_used', sa.Integer(), nullable=True),
        sa.Column('user_approved', sa.Boolean(), server_default='false', nullable=True),
        sa.Column('sent_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('created_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.PrimaryKeyConstraint('id', name='pk_drafts'),
        sa.ForeignKeyConstraint(['card_id'], ['decision_cards.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['thread_id'], ['threads.id'], ondelete='CASCADE'),
    )

    # 8. calendar_events table
    op.create_table(
        'calendar_events',
        sa.Column('id', postgresql.UUID(), server_default=sa.text('gen_random_uuid()'), nullable=False),
        sa.Column('user_id', postgresql.UUID(), nullable=False),
        sa.Column('source_account_id', postgresql.UUID(), nullable=False),
        sa.Column('external_event_id', sa.VARCHAR(255), nullable=False),
        sa.Column('thread_id', postgresql.UUID(), nullable=True),
        sa.Column('title', sa.Text(), nullable=False),
        sa.Column('start_at', sa.TIMESTAMP(timezone=True), nullable=False),
        sa.Column('end_at', sa.TIMESTAMP(timezone=True), nullable=False),
        sa.Column('timezone', sa.VARCHAR(50), nullable=True),
        sa.Column('location', sa.Text(), nullable=True),
        sa.Column('attendee_emails', postgresql.ARRAY(sa.Text()), nullable=True),
        sa.Column('description', sa.Text(), nullable=True),
        sa.Column('is_confirmed', sa.Boolean(), server_default='false', nullable=True),
        sa.Column('reminder_sent_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('briefing_card_id', postgresql.UUID(), nullable=True),
        sa.Column('created_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.PrimaryKeyConstraint('id', name='pk_calendar_events'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['source_account_id'], ['email_accounts.id']),
        sa.ForeignKeyConstraint(['thread_id'], ['threads.id']),
        sa.ForeignKeyConstraint(['briefing_card_id'], ['decision_cards.id']),
        sa.UniqueConstraint('source_account_id', 'external_event_id', name='uq_calendar_events_source_event'),
    )

    # 9. billing_records table
    op.create_table(
        'billing_records',
        sa.Column('id', postgresql.UUID(), server_default=sa.text('gen_random_uuid()'), nullable=False),
        sa.Column('user_id', postgresql.UUID(), nullable=False),
        sa.Column('period_start', sa.Date(), nullable=False),
        sa.Column('period_end', sa.Date(), nullable=False),
        sa.Column('plan', sa.VARCHAR(20), nullable=False),
        sa.Column('amount_cents', sa.Integer(), nullable=False),
        sa.Column('stripe_invoice_id', sa.VARCHAR(255), nullable=True),
        sa.Column('status', sa.VARCHAR(20), server_default='pending', nullable=True),
        sa.Column('paid_at', sa.TIMESTAMP(timezone=True), nullable=True),
        sa.Column('created_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.PrimaryKeyConstraint('id', name='pk_billing_records'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
    )

    # 10. decision_logs table
    op.create_table(
        'decision_logs',
        sa.Column('id', postgresql.UUID(), server_default=sa.text('gen_random_uuid()'), nullable=False),
        sa.Column('user_id', postgresql.UUID(), nullable=False),
        sa.Column('card_id', postgresql.UUID(), nullable=False),
        sa.Column('action', sa.VARCHAR(50), nullable=False),
        sa.Column('user_input', sa.Text(), nullable=True),
        sa.Column('agent_draft', sa.Text(), nullable=True),
        sa.Column('final_output', sa.Text(), nullable=True),
        sa.Column('metadata', postgresql.JSONB(), nullable=True),
        sa.Column('created_at', sa.TIMESTAMP(timezone=True), server_default=sa.text('NOW()'), nullable=True),
        sa.PrimaryKeyConstraint('id', name='pk_decision_logs'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['card_id'], ['decision_cards.id'], ondelete='CASCADE'),
    )


def downgrade():
    # Drop tables in reverse dependency order
    op.drop_table('decision_logs')
    op.drop_table('billing_records')
    op.drop_table('calendar_events')
    op.drop_table('drafts')
    # auto_handle_rules is owned by classification service — do NOT drop here
    op.drop_table('decision_cards')
    op.drop_table('raw_emails')
    op.drop_table('threads')
    op.drop_table('email_accounts')
    op.drop_table('users')
    op.execute("DROP EXTENSION IF EXISTS pgcrypto")
