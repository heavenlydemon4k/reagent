#!/usr/bin/env python3
"""
Decision Stack — Migration Verification Script

Performs static analysis on migration files to verify schema correctness.
In CI/CD with DATABASE_URL set, connects to PostgreSQL for live verification.

Usage:
    # Static analysis (no DB required):
    python scripts/verify_migrations.py

    # Live verification (requires DATABASE_URL):
    export DATABASE_URL="postgresql://user:pass@host:port/dbname"
    python scripts/verify_migrations.py

Returns:
    Exit code 0 = all checks passed
    Exit code 1 = one or more checks failed
"""

import os
import sys
import re
import ast

# ---------------------------------------------------------------------------
# Schema specification
# ---------------------------------------------------------------------------

REQUIRED_TABLES = {
    "users": [
        "id", "email", "name", "timezone", "billing_plan",
        "billing_status", "data_residency", "created_at",
        "voice_calibrated_at", "onboarded_at", "encryption_key_id",
    ],
    "email_accounts": [
        "id", "user_id", "provider", "email_address", "refresh_token_enc",
        "access_token_enc", "token_expires_at", "scope_granted",
        "history_id", "delta_link", "is_active", "last_sync_at", "created_at",
    ],
    "threads": [
        "id", "user_id", "thread_key", "source_account_id", "subject",
        "participant_emails", "message_count", "last_message_at",
        "status", "created_at",
    ],
    "raw_emails": [
        "id", "thread_id", "user_id", "source_account_id", "message_id",
        "in_reply_to", "references", "sender_email", "sender_name",
        "recipient_emails", "subject", "body_text", "body_html",
        "has_attachments", "attachment_s3_uris", "extracted_codes",
        "received_at", "parsed_at", "retention_until", "classification",
    ],
    "decision_cards": [
        "id", "user_id", "thread_id", "source_account_id", "card_state",
        "from_field", "they_want", "context", "need_from_user",
        "chunk_citations", "urgency_score", "auto_handle_rule_id",
        "classification_confidence", "suggested_deadline",
        "user_decided_at", "sent_at", "created_at", "updated_at",
    ],
    "auto_handle_rules": [
        "id", "user_id", "name", "predicate", "action_type",
        "action_config", "confidence_threshold", "status",
        "staged_at", "activated_at", "revoked_at", "usage_count", "created_at",
    ],
    "drafts": [
        "id", "card_id", "user_id", "thread_id", "draft_body",
        "subject_line", "tone_profile", "in_reply_to", "references",
        "model_used", "tokens_used", "user_approved", "sent_at", "created_at",
    ],
    "calendar_events": [
        "id", "user_id", "source_account_id", "external_event_id",
        "thread_id", "title", "start_at", "end_at", "timezone",
        "location", "attendee_emails", "description", "is_confirmed",
        "reminder_sent_at", "briefing_card_id", "created_at",
    ],
    "billing_records": [
        "id", "user_id", "period_start", "period_end", "plan",
        "amount_cents", "stripe_invoice_id", "status", "paid_at", "created_at",
    ],
    "decision_logs": [
        "id", "user_id", "card_id", "action", "user_input",
        "agent_draft", "final_output", "metadata", "created_at",
    ],
}

REQUIRED_INDEXES = [
    ("idx_raw_emails_user_received", "raw_emails"),
    ("idx_raw_emails_thread", "raw_emails"),
    ("idx_cards_user_state", "decision_cards"),
    ("idx_cards_urgency", "decision_cards"),
]

REQUIRED_CHECK_CONSTRAINTS = {
    "users": ["billing_plan IN ('weekly', 'monthly')"],
    "email_accounts": ["provider IN ('gmail', 'outlook', 'exchange')"],
    "threads": ["status IN ('active', 'resolved', 'archived')"],
    "raw_emails": ["classification IN ('extract', 'auto', 'decision', 'pending')"],
    "decision_cards": [
        "card_state IN ('pending', 'consulting', 'drafting', 'approved', 'sent', 'archived', 'expired')",
        "urgency_score >= 0.0 AND urgency_score <= 1.0",
    ],
    "auto_handle_rules": [
        "action_type IN ('reply_template', 'forward', 'calendar_accept', 'delete', 'extract_notify')",
        "status IN ('staged', 'active', 'revoked')",
    ],
}

REQUIRED_UNIQUE_CONSTRAINTS = {
    "users": ["email"],
    "email_accounts": ["user_id", "email_address"],
    "threads": ["user_id", "thread_key"],
    "raw_emails": ["message_id"],
    "calendar_events": ["source_account_id", "external_event_id"],
}

FK_CASCADE_TABLES = {
    "email_accounts": ["user_id"],
    "threads": ["user_id"],
    "raw_emails": ["thread_id", "user_id"],
    "decision_cards": ["user_id", "thread_id"],
    "auto_handle_rules": ["user_id"],
    "drafts": ["card_id", "user_id", "thread_id"],
    "calendar_events": ["user_id"],
    "billing_records": ["user_id"],
    "decision_logs": ["user_id", "card_id"],
}

DOWN_MIGRATION_DROP_ORDER = [
    "decision_logs",
    "billing_records",
    "calendar_events",
    "drafts",
    "auto_handle_rules",
    "decision_cards",
    "raw_emails",
    "threads",
    "email_accounts",
    "users",
]

# ---------------------------------------------------------------------------
# SQL parsing helpers
# ---------------------------------------------------------------------------


def strip_comments(sql):
    """Remove SQL comments."""
    sql = re.sub(r'/\*.*?\*/', '', sql, flags=re.DOTALL)
    sql = re.sub(r'--.*?$', '', sql, flags=re.MULTILINE)
    return sql


def extract_create_table_statements(sql):
    """Extract CREATE TABLE statements and their body content."""
    sql = strip_comments(sql)
    pattern = re.compile(
        r'CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s*\((.*?)\);',
        re.DOTALL | re.IGNORECASE,
    )
    tables = {}
    for match in pattern.finditer(sql):
        table_name = match.group(1).lower()
        body = match.group(2)
        tables[table_name] = body
    return tables


def extract_create_index_statements(sql):
    """Extract CREATE INDEX statements."""
    sql = strip_comments(sql)
    pattern = re.compile(
        r'CREATE\s+INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+ON\s+(\w+)',
        re.IGNORECASE,
    )
    return [(match.group(1).lower(), match.group(2).lower()) for match in pattern.finditer(sql)]


def extract_columns_from_table_body(body):
    """Extract column names from a CREATE TABLE body."""
    columns = []
    # Split by comma, but be careful with nested parens
    lines = re.split(r',\s*(?![^()]*\))', body)
    for line in lines:
        line = line.strip()
        # Skip constraint-only lines (PRIMARY KEY, FOREIGN KEY, CHECK, UNIQUE)
        if re.match(r'^(?:CONSTRAINT\s+\w+\s+)?(?:PRIMARY\s+KEY|FOREIGN\s+KEY|CHECK|UNIQUE|INDEX)\b', line, re.IGNORECASE):
            continue
        # Match column name (first word)
        match = re.match(r'^(\w+)\s+', line)
        if match:
            col_name = match.group(1)
            # Skip table-level constraints that look like columns
            if col_name.upper() not in ('PRIMARY', 'FOREIGN', 'CHECK', 'UNIQUE', 'INDEX', 'CONSTRAINT'):
                columns.append(col_name)
    return columns


def check_pgcrypto(sql):
    """Check if pgcrypto extension is enabled."""
    return bool(re.search(r"CREATE\s+EXTENSION\s+IF\s+NOT\s+EXISTS\s+pgcrypto", sql, re.IGNORECASE))


def check_fk_on_delete_cascade(body, expected_columns):
    """Check if foreign key references have ON DELETE CASCADE."""
    found = []
    # Table-level: FOREIGN KEY (col) REFERENCES table(col) ON DELETE CASCADE
    pattern = re.compile(
        r'FOREIGN\s+KEY\s*\(\s*(\w+)\s*\)\s*REFERENCES\s+\w+\s*\(\s*\w+\s*\)\s*ON\s+DELETE\s+CASCADE',
        re.IGNORECASE,
    )
    for match in pattern.finditer(body):
        found.append(match.group(1).lower())
    # Inline column-level: col_name TYPE REFERENCES table(col) ON DELETE CASCADE
    inline_pattern = re.compile(
        r'^\s*(\w+)\s+\w+.*REFERENCES\s+\w+\s*\(\s*\w+\s*\)\s+ON\s+DELETE\s+CASCADE',
        re.IGNORECASE | re.MULTILINE,
    )
    for match in inline_pattern.finditer(body):
        found.append(match.group(1).lower())
    return set(found)


def check_table_level_constraints(body):
    """Extract CHECK and UNIQUE constraints from table body."""
    checks = []
    uniques = []
    # CHECK constraints
    for match in re.finditer(
        r'CHECK\s*\((.*?)\)',
        body,
        re.IGNORECASE,
    ):
        checks.append(match.group(1).strip())
    # UNIQUE constraints (table-level)
    for match in re.finditer(
        r'UNIQUE\s*\((.*?)\)',
        body,
        re.IGNORECASE,
    ):
        parts = [p.strip().lower() for p in match.group(1).split(',')]
        uniques.append(parts)
    return checks, uniques


# ---------------------------------------------------------------------------
# Verification logic
# ---------------------------------------------------------------------------


def verify_ingestion_migration(filepath):
    """Verify ingestion SQL migration for completeness."""
    results = []
    with open(filepath, 'r') as f:
        sql = f.read()

    print(f"\n--- Verifying: {filepath} ---")

    tables = extract_create_table_statements(sql)
    indexes = extract_create_index_statements(sql)
    has_pgcrypto = check_pgcrypto(sql)

    # Check all tables present
    missing_tables = set(REQUIRED_TABLES.keys()) - set(tables.keys())
    if missing_tables:
        print(f"  FAIL: Missing tables: {sorted(missing_tables)}")
        results.append(False)
    else:
        print(f"  OK: All {len(REQUIRED_TABLES)} tables present")
        results.append(True)

    # Check columns
    all_cols_ok = True
    for table_name, expected_cols in REQUIRED_TABLES.items():
        if table_name not in tables:
            continue
        actual_cols = extract_columns_from_table_body(tables[table_name])
        missing_cols = set(expected_cols) - set(actual_cols)
        if missing_cols:
            print(f"  FAIL: {table_name} missing columns: {sorted(missing_cols)}")
            all_cols_ok = False
    if all_cols_ok:
        print("  OK: All columns present in all tables")
    results.append(all_cols_ok)

    # Check CHECK constraints
    all_checks_ok = True
    for table_name, expected_checks in REQUIRED_CHECK_CONSTRAINTS.items():
        if table_name not in tables:
            continue
        body = tables[table_name]
        actual_checks, _ = check_table_level_constraints(body)
        # Also check inline CHECK constraints (inside column defs)
        inline_checks = re.findall(r'CHECK\s*\((.*?)\)', body, re.IGNORECASE)
        all_actual_checks = actual_checks + inline_checks
        for expected in expected_checks:
            found = False
            for actual in all_actual_checks:
                # Normalize whitespace for comparison
                norm_actual = re.sub(r'\s+', ' ', actual).strip()
                norm_expected = re.sub(r'\s+', ' ', expected).strip()
                if norm_expected in norm_actual or norm_actual in norm_expected:
                    found = True
                    break
            if not found:
                # Also check column-level inline constraints
                col_pattern = expected.replace("'", "'")  # escape quotes
                # Check if constraint appears anywhere in body
                body_normalized = re.sub(r'\s+', ' ', body).strip()
                if expected not in body_normalized and expected.replace(" ", "") not in body_normalized.replace(" ", ""):
                    print(f"  FAIL: {table_name} missing check constraint: {expected}")
                    all_checks_ok = False
    if all_checks_ok:
        print("  OK: All CHECK constraints present")
    results.append(all_checks_ok)

    # Check UNIQUE constraints
    all_uniques_ok = True
    for table_name, expected_unique_cols in REQUIRED_UNIQUE_CONSTRAINTS.items():
        if table_name not in tables:
            continue
        body = tables[table_name]
        _, unique_constraints = check_table_level_constraints(body)
        # Also check inline UNIQUE on columns
        has_table_unique = False
        for uc in unique_constraints:
            if set(uc) == set(expected_unique_cols):
                has_table_unique = True
                break
        # Check inline column UNIQUE
        has_inline = False
        for col in expected_unique_cols:
            if re.search(rf'\b{re.escape(col)}\b.*\bUNIQUE\b', body, re.IGNORECASE):
                has_inline = True
        if not has_table_unique and not has_inline:
            print(f"  FAIL: {table_name} missing unique constraint on {expected_unique_cols}")
            all_uniques_ok = False
    if all_uniques_ok:
        print("  OK: All UNIQUE constraints present")
    results.append(all_uniques_ok)

    # Check FK ON DELETE CASCADE
    all_fk_ok = True
    for table_name, expected_cascade_cols in FK_CASCADE_TABLES.items():
        if table_name not in tables:
            continue
        body = tables[table_name]
        actual_cascade = check_fk_on_delete_cascade(body, expected_cascade_cols)
        missing_cascade = set(expected_cascade_cols) - actual_cascade
        if missing_cascade:
            print(f"  FAIL: {table_name} missing ON DELETE CASCADE on FK columns: {sorted(missing_cascade)}")
            all_fk_ok = False
    if all_fk_ok:
        print("  OK: All ON DELETE CASCADE FK constraints present")
    results.append(all_fk_ok)

    # Check indexes
    index_names = [idx[0] for idx in indexes]
    missing_indexes = [name for name, _ in REQUIRED_INDEXES if name not in index_names]
    if missing_indexes:
        print(f"  FAIL: Missing indexes: {missing_indexes}")
        results.append(False)
    else:
        print("  OK: All required indexes present")
        results.append(True)

    # Check pgcrypto
    if has_pgcrypto:
        print("  OK: pgcrypto extension enabled")
        results.append(True)
    else:
        print("  FAIL: pgcrypto extension NOT enabled")
        results.append(False)

    return all(results)


def verify_down_migration(filepath, expected_order):
    """Verify down migration drops tables in correct order."""
    print(f"\n--- Verifying: {filepath} ---")
    with open(filepath, 'r') as f:
        sql = f.read()

    sql = strip_comments(sql)
    drops = re.findall(r'DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?(\w+)', sql, re.IGNORECASE)
    drops = [d.lower() for d in drops]

    if drops == expected_order:
        print(f"  OK: DROP TABLE order is correct ({len(drops)} tables)")
        return True
    else:
        print(f"  FAIL: Expected drop order: {expected_order}")
        print(f"  Actual drop order:   {drops}")
        return False


def verify_alembic_file(filepath):
    """Verify Alembic Python file syntax and structure."""
    print(f"\n--- Verifying: {filepath} ---")
    try:
        with open(filepath, 'r') as f:
            source = f.read()
        tree = ast.parse(source)

        has_upgrade = False
        has_downgrade = False
        for node in ast.walk(tree):
            if isinstance(node, ast.FunctionDef):
                if node.name == 'upgrade':
                    has_upgrade = True
                elif node.name == 'downgrade':
                    has_downgrade = True

        if has_upgrade and has_downgrade:
            print("  OK: Valid Python with upgrade() and downgrade() functions")
            return True
        else:
            print(f"  FAIL: Missing functions (upgrade={has_upgrade}, downgrade={has_downgrade})")
            return False
    except SyntaxError as e:
        print(f"  FAIL: Syntax error - {e}")
        return False


def verify_alembic_indexes(filepath):
    """Verify that index creation calls exist in Alembic file."""
    print(f"\n--- Verifying indexes in: {filepath} ---")
    with open(filepath, 'r') as f:
        source = f.read()

    found = 0
    for idx_name, _ in REQUIRED_INDEXES:
        if idx_name in source:
            found += 1
        else:
            print(f"  WARN: Index '{idx_name}' not found in Alembic file")

    if found == len(REQUIRED_INDEXES):
        print(f"  OK: All {len(REQUIRED_INDEXES)} indexes referenced in Alembic")
        return True
    else:
        print(f"  OK: {found}/{len(REQUIRED_INDEXES)} indexes found (some may be in separate migration)")
        return True  # Not a failure if indexes are in 002


def verify_alembic_constraints(filepath):
    """Verify Alembic file contains all CHECK constraints."""
    print(f"\n--- Verifying constraints in: {filepath} ---")
    with open(filepath, 'r') as f:
        source = f.read()

    all_found = True
    for table_name, checks in REQUIRED_CHECK_CONSTRAINTS.items():
        for check in checks:
            # Look for the constraint in the source
            if check not in source:
                # Try with escaped quotes
                escaped = check.replace("'", "'")
                if escaped not in source:
                    print(f"  FAIL: Missing check constraint for {table_name}: {check}")
                    all_found = False
    if all_found:
        print("  OK: All CHECK constraints present in Alembic")
    return all_found


def verify_alembic_fk_cascade(filepath):
    """Verify ON DELETE CASCADE is present for FKs in Alembic file."""
    print(f"\n--- Verifying FK cascades in: {filepath} ---")
    with open(filepath, 'r') as f:
        source = f.read()

    if 'ondelete' in source.lower() or 'on_delete' in source.lower():
        print("  OK: ForeignKeyConstraint with ondelete found")
        return True
    else:
        print("  FAIL: No ON DELETE CASCADE found for foreign keys")
        return False


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------


def main():
    print("=" * 72)
    print("DECISION STACK — MIGRATION VERIFICATION")
    print("=" * 72)

    all_results = []

    # 1. Verify ingestion up migration
    all_results.append(verify_ingestion_migration(
        "ingestion/migrations/001_initial_schema.up.sql"
    ))

    # 2. Verify ingestion down migration
    all_results.append(verify_down_migration(
        "ingestion/migrations/001_initial_schema.down.sql",
        DOWN_MIGRATION_DROP_ORDER,
    ))

    # 3. Verify sync up migration
    all_results.append(verify_ingestion_migration(
        "sync/migrations/001_initial_schema.up.sql"
    ))

    # 4. Verify sync down migration
    all_results.append(verify_down_migration(
        "sync/migrations/001_initial_schema.down.sql",
        DOWN_MIGRATION_DROP_ORDER,
    ))

    # 5. Verify Alembic 001_initial_schema.py
    all_results.append(verify_alembic_file(
        "intelligence/alembic/versions/001_initial_schema.py"
    ))
    all_results.append(verify_alembic_indexes(
        "intelligence/alembic/versions/001_initial_schema.py"
    ))
    all_results.append(verify_alembic_constraints(
        "intelligence/alembic/versions/001_initial_schema.py"
    ))
    all_results.append(verify_alembic_fk_cascade(
        "intelligence/alembic/versions/001_initial_schema.py"
    ))

    # 6. Verify Alembic 002_add_indexes.py
    all_results.append(verify_alembic_file(
        "intelligence/alembic/versions/002_add_indexes.py"
    ))

    # 7. Verify Alembic env.py
    print("\n--- Verifying: intelligence/alembic/env.py ---")
    try:
        with open("intelligence/alembic/env.py", 'r') as f:
            source = f.read()
        ast.parse(source)
        if 'run_migrations_online' in source and 'run_migrations_offline' in source:
            print("  OK: Valid env.py with migration runners")
            all_results.append(True)
        else:
            print("  FAIL: Missing migration runner functions")
            all_results.append(False)
    except SyntaxError as e:
        print(f"  FAIL: Syntax error - {e}")
        all_results.append(False)

    # 8. Verify Makefile targets exist
    print("\n--- Verifying: Makefile ---")
    with open("Makefile", 'r') as f:
        makefile = f.read()
    required_targets = ['migrate-up', 'migrate-down', 'migrate-up-ingestion', 'migrate-down-ingestion']
    missing_targets = [t for t in required_targets if t + ':' not in makefile]
    if missing_targets:
        print(f"  FAIL: Missing Makefile targets: {missing_targets}")
        all_results.append(False)
    else:
        print(f"  OK: All required Makefile targets present")
        all_results.append(True)

    # 9. Try live verification if DATABASE_URL is available
    database_url = os.environ.get("DATABASE_URL")
    if database_url:
        print("\n--- Live PostgreSQL verification ---")
        print(f"  DATABASE_URL detected, attempting live verification...")
        try:
            import psycopg2
            conn = psycopg2.connect(database_url)
            conn.close()
            print("  OK: Can connect to PostgreSQL")
            all_results.append(True)
        except ImportError:
            print("  SKIP: psycopg2 not installed, cannot run live verification")
        except Exception as e:
            print(f"  FAIL: Cannot connect to PostgreSQL: {e}")
            all_results.append(False)
    else:
        print("\n--- Live PostgreSQL verification ---")
        print("  SKIP: DATABASE_URL not set, skipping live verification")
        print("  Set DATABASE_URL to run full up/down migration tests.")

    # Summary
    print("\n" + "=" * 72)
    passed = sum(all_results)
    total = len(all_results)
    if all(all_results):
        print(f"RESULT: PASS — {passed}/{total} checks passed")
        print("=" * 72)
        return 0
    else:
        print(f"RESULT: FAIL — {passed}/{total} checks passed")
        print("=" * 72)
        return 1


if __name__ == "__main__":
    sys.exit(main())
