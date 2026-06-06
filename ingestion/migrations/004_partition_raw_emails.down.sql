-- Migration 004: Rollback — remove partitioned table
-- Service: ingestion
--
-- WARNING: This destroys the partitioned table and all 16 partitions.
-- Ensure data has been migrated back or is preserved in raw_emails_old
-- before running this rollback.

-- Drop all 16 partitions (cascade handles this automatically via
-- PARTITION OF, but we drop explicitly for clarity)
DO $$
DECLARE
    i INT;
BEGIN
    FOR i IN 0..15 LOOP
        EXECUTE format('DROP TABLE IF EXISTS raw_emails_p%s', i);
    END LOOP;
END $$;

-- Drop the partitioned parent table
DROP TABLE IF EXISTS raw_emails_partitioned;
