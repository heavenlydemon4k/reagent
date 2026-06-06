"""
Alembic environment configuration for Decision Stack — Intelligence service.
"""

import os
import sys
from logging.config import fileConfig

from sqlalchemy import create_engine, pool
from alembic import context

# Add project root to sys.path so models can be imported if needed
# Adjust this path to your project's model location
sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..'))

# Alembic Config object
config = context.config

# Interpret the config file for Python logging
if config.config_file_name is not None:
    fileConfig(config.config_file_name)

# Metadata for autogenerate support (optional; set to your models' Base.metadata)
# target_metadata = None
try:
    from intelligence.models.base import Base  # noqa: F401
    target_metadata = Base.metadata
except ImportError:
    target_metadata = None


def get_database_url():
    """Retrieve DATABASE_URL from environment."""
    url = os.environ.get('DATABASE_URL')
    if not url:
        raise RuntimeError(
            'DATABASE_URL environment variable is not set. '
            'Expected format: postgresql://user:pass@host:port/dbname'
        )
    return url


def run_migrations_offline():
    """Run migrations in 'offline' mode."""
    url = get_database_url()
    context.configure(
        url=url,
        target_metadata=target_metadata,
        literal_binds=True,
        dialect_opts={'paramstyle': 'named'},
    )

    with context.begin_transaction():
        context.run_migrations()


def run_migrations_online():
    """Run migrations in 'online' mode."""
    connectable = create_engine(
        get_database_url(),
        poolclass=pool.NullPool,
    )

    with connectable.connect() as connection:
        context.configure(
            connection=connection,
            target_metadata=target_metadata,
        )

        with context.begin_transaction():
            context.run_migrations()


if context.is_offline_mode():
    run_migrations_offline()
else:
    run_migrations_online()
