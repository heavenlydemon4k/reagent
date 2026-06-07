// ============================================================================
// Neo4j Schema Initialization for Decision Stack Intelligence Layer
// ============================================================================
// This script is idempotent — safe to run multiple times.
// All constraints use IF NOT EXISTS.
//
// Run with: cypher-shell -f neo4j_schema.cypher
// Or call from schema_init.py via the Neo4j driver.
// ============================================================================

// ----------------------------------------------------------------------------
// CONSTRAINTS
// ----------------------------------------------------------------------------

// Primary unique identifier for Contact nodes (UUID v4)
CREATE CONSTRAINT contact_id IF NOT EXISTS
  FOR (c:Contact) REQUIRE c.id IS UNIQUE;

// Canonical email ensures deduplication across name variants
CREATE CONSTRAINT contact_email IF NOT EXISTS
  FOR (c:Contact) REQUIRE c.canonical_email IS UNIQUE;

// ----------------------------------------------------------------------------
// INDEXES
// ----------------------------------------------------------------------------

// Multi-tenant queries: isolate contacts per user
CREATE INDEX contact_user_id IF NOT EXISTS
  FOR (c:Contact) ON (c.user_id);

// Fast lookup by canonical email (complement to unique constraint)
CREATE INDEX contact_canonical_email IF NOT EXISTS
  FOR (c:Contact) ON (c.canonical_email);

// Time-range queries for interaction history
CREATE INDEX interaction_date IF NOT EXISTS
  FOR ()-[r:INTERACTION]-() ON (r.date);

// Thread reconstruction: group all interactions in a conversation
CREATE INDEX interaction_thread_id IF NOT EXISTS
  FOR ()-[r:INTERACTION]-() ON (r.thread_id);

// ----------------------------------------------------------------------------
// FULL SCHEMA DOCUMENTATION
// ----------------------------------------------------------------------------

// (:Contact {
//   id:                     "uuid",           // Primary key (UUID v4)
//   user_id:                "uuid",           // Multi-tenant isolation key
//   canonical_email:        "sarah@vendor.com",
//   name_variants:          ["Sarah Chen", "S. Chen"],
//   organization:           "Vendor Inc",
//   first_contact_date:     "2024-03-15",     // ISO-8601 date
//   last_contact_date:      "2024-06-01",
//   interaction_count:      42,
//   avg_response_hours:     4.5,
//   tone_history:           ["effusive", "neutral", "neutral"],
//   total_monetary_value:   42000.00,
//   projects:               ["Website Redesign"]
// })

// [:INTERACTION {
//   id:               "uuid",           // Edge unique identifier
//   user_id:          "uuid",           // Multi-tenant isolation (redundant but query-efficient)
//   thread_id:        "uuid",           // Groups emails in the same conversation
//   email_id:         "uuid",           // References source email in Qdrant
//   date:             "2024-06-01",     // ISO-8601 date of interaction
//   direction:        "inbound" | "outbound",
//   type:             "email" | "meeting" | "call" | "note",
//   subject:          "Re: Proposal Update",
//   summary:          "Sarah approved the revised timeline",
//   agreed_to:        true,             // True if contact agreed to something
//   committed_to:     "Send mockups by Friday",
//   quote_amount:     15000.00,         // Numeric value quoted/committed
//   response_hours:   2.5,              // Hours to respond (for velocity calc)
//   tone:             "effusive" | "warm" | "neutral" | "cold" | "frustrated",
//   mentions_project: ["Website Redesign"],
//   created_at:       "2024-06-01T12:34:56Z"
// }]

// ----------------------------------------------------------------------------
// TRAVERSAL QUERY TEMPLATES
// ----------------------------------------------------------------------------

// --- Last agreement per contact ---
// MATCH (c:Contact {user_id: $user_id})-[r:INTERACTION]->()
// WHERE r.agreed_to = true
// WITH c, r ORDER BY r.date DESC
// RETURN c.canonical_email AS contact,
//        collect(r.committed_to)[0] AS last_commitment,
//        collect(r.date)[0] AS committed_on;

// --- Quote history ---
// MATCH (c:Contact {user_id: $user_id})-[r:INTERACTION]->()
// WHERE r.quote_amount IS NOT NULL
// RETURN c.canonical_email AS contact,
//        r.date AS date,
//        r.quote_amount AS amount,
//        r.subject AS context
// ORDER BY r.date DESC;

// --- Response velocity (avg hours to respond) ---
// MATCH (c:Contact {user_id: $user_id})-[r:INTERACTION]->()
// WHERE r.direction = 'inbound' AND r.response_hours IS NOT NULL
// RETURN c.canonical_email AS contact,
//        avg(r.response_hours) AS avg_response_hours,
//        count(r) AS sample_size
// ORDER BY avg_response_hours ASC;

// --- Tone trajectory ---
// MATCH (c:Contact {user_id: $user_id})-[r:INTERACTION]->()
// WITH c, r ORDER BY r.date ASC
// RETURN c.canonical_email AS contact,
//        collect(r.tone) AS tone_sequence,
//        collect(r.date) AS dates;
