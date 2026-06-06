"""
Add indexes for Decision Stack — Intelligence service.

All specified indexes are already created in 001_initial_schema.
This migration is reserved for future index additions.

Revision ID: 002
Revises: 001
Create Date: 2024-01-02 00:00:00.000000+00:00

"""
from alembic import op

# revision identifiers, used by Alembic.
revision = '002'
down_revision = '001'
branch_labels = None
depends_on = None


def upgrade():
    # All indexes from spec already applied in 001_initial_schema.
    # No additional indexes to create.
    pass


def downgrade():
    # No indexes were added in this migration.
    pass
