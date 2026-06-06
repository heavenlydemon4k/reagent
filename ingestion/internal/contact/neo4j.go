// Package contact provides contact deduplication for the Ingestion Mesh.
// neo4j.go implements Neo4j CRUD operations for Contact nodes and relationships.
package contact

import (
	"context"
	"fmt"
	"time"

	"github.com/decisionstack/ingestion/internal/models"
	"github.com/google/uuid"
	neo4jdriver "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// Neo4jStore wraps a Neo4j driver and provides all contact persistence operations.
type Neo4jStore struct {
	driver neo4jdriver.DriverWithContext
}

// InteractionMetadata captures contextual data about an email interaction.
type InteractionMetadata struct {
	ThreadID       uuid.UUID `json:"thread_id"`
	EmailDirection string    `json:"email_direction"` // "incoming" | "outgoing"
	Subject        string    `json:"subject"`
	SentAt         time.Time `json:"sent_at"`
}

// NewNeo4jStore creates a new Neo4j-backed contact store.
func NewNeo4jStore(driver neo4jdriver.DriverWithContext) *Neo4jStore {
	return &Neo4jStore{driver: driver}
}

// FindContactByEmail performs an exact match on canonical_email for a given user.
// Uses a composite index on (user_id, canonical_email) for fast lookup.
func (s *Neo4jStore) FindContactByEmail(ctx context.Context, userID uuid.UUID, email string) (*models.Contact, error) {
	canonical := NormalizeEmail(email)

	session := s.driver.NewSession(ctx, neo4jdriver.SessionConfig{AccessMode: neo4jdriver.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4jdriver.ManagedTransaction) (interface{}, error) {
		query := `
			MATCH (c:Contact)
			WHERE c.user_id = $user_id AND c.canonical_email = $email
			RETURN c.id AS id, c.user_id AS user_id, c.canonical_email AS canonical_email,
			       c.name_variants AS name_variants, c.organization AS organization,
			       c.first_contact_date AS first_contact_date, c.last_contact_date AS last_contact_date,
			       c.interaction_count AS interaction_count, c.avg_response_hours AS avg_response_hours,
			       c.tone_history AS tone_history, c.total_monetary_value AS total_monetary_value,
			       c.projects AS projects
			LIMIT 1
		`
		rec, err := tx.Run(ctx, query, map[string]interface{}{
			"user_id": userID.String(),
			"email":   canonical,
		})
		if err != nil {
			return nil, err
		}

		if rec.Next(ctx) {
			record := rec.Record()
			return recordToContact(record)
		}
		return nil, nil
	})
	if err != nil {
		return nil, fmt.Errorf("neo4j find by email: %w", err)
	}

	if result == nil {
		return nil, nil
	}
	return result.(*models.Contact), nil
}

// FindContactsByName searches for contacts whose name_variants contain the
// given name (case-insensitive). Returns all matches for review.
func (s *Neo4jStore) FindContactsByName(ctx context.Context, userID uuid.UUID, name string) ([]*models.Contact, error) {
	normalized := NormalizeName(name)
	if normalized == "" {
		return nil, nil
	}

	session := s.driver.NewSession(ctx, neo4jdriver.SessionConfig{AccessMode: neo4jdriver.AccessModeRead})
	defer session.Close(ctx)

	result, err := session.ExecuteRead(ctx, func(tx neo4jdriver.ManagedTransaction) (interface{}, error) {
		query := `
			MATCH (c:Contact)
			WHERE c.user_id = $user_id
			  AND any(v IN c.name_variants WHERE toLower(v) CONTAINS toLower($name))
			RETURN c.id AS id, c.user_id AS user_id, c.canonical_email AS canonical_email,
			       c.name_variants AS name_variants, c.organization AS organization,
			       c.first_contact_date AS first_contact_date, c.last_contact_date AS last_contact_date,
			       c.interaction_count AS interaction_count, c.avg_response_hours AS avg_response_hours,
			       c.tone_history AS tone_history, c.total_monetary_value AS total_monetary_value,
			       c.projects AS projects
			LIMIT 20
		`
		rec, err := tx.Run(ctx, query, map[string]interface{}{
			"user_id": userID.String(),
			"name":    normalized,
		})
		if err != nil {
			return nil, err
		}

		var contacts []*models.Contact
		for rec.Next(ctx) {
			record := rec.Record()
			c, err := recordToContact(record)
			if err != nil {
				continue
			}
			contacts = append(contacts, c)
		}
		return contacts, nil
	})
	if err != nil {
		return nil, fmt.Errorf("neo4j find by name: %w", err)
	}

	return result.([]*models.Contact), nil
}

// CreateContact inserts a new Contact node into Neo4j.
// All properties are parameterized to prevent Cypher injection.
func (s *Neo4jStore) CreateContact(ctx context.Context, userID uuid.UUID, email, name string) (*models.Contact, error) {
	canonical := NormalizeEmail(email)
	nameVariants := GenerateNameVariants(name)
	now := time.Now().UTC()
	contactID := uuid.Must(uuid.NewRandom())

	session := s.driver.NewSession(ctx, neo4jdriver.SessionConfig{AccessMode: neo4jdriver.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4jdriver.ManagedTransaction) (interface{}, error) {
		query := `
			CREATE (c:Contact {
				id: $id,
				user_id: $user_id,
				canonical_email: $canonical_email,
				name_variants: $name_variants,
				first_contact_date: $first_contact_date,
				last_contact_date: $last_contact_date,
				interaction_count: 0,
				avg_response_hours: null,
				tone_history: [],
				total_monetary_value: 0.0,
				projects: []
			})
			RETURN c
		`
		_, err := tx.Run(ctx, query, map[string]interface{}{
			"id":                contactID.String(),
			"user_id":           userID.String(),
			"canonical_email":   canonical,
			"name_variants":     nameVariants,
			"first_contact_date": now.Format(time.RFC3339),
			"last_contact_date":  now.Format(time.RFC3339),
		})
		return nil, err
	})
	if err != nil {
		return nil, fmt.Errorf("neo4j create contact: %w", err)
	}

	return &models.Contact{
		ID:               contactID,
		UserID:           userID,
		CanonicalEmail:   canonical,
		NameVariants:     nameVariants,
		FirstContactDate: &now,
		LastContactDate:  &now,
		InteractionCount: 0,
		ToneHistory:      []string{},
		TotalMonetaryValue: 0,
		Projects:         []string{},
	}, nil
}

// CreateSimilarToEdge creates a directed SIMILAR_TO relationship between two
// Contact nodes with an associated confidence score. This flags the pair for
// human review — contacts are NEVER auto-merged.
func (s *Neo4jStore) CreateSimilarToEdge(ctx context.Context, contactID, similarToID uuid.UUID, confidence float64) error {
	session := s.driver.NewSession(ctx, neo4jdriver.SessionConfig{AccessMode: neo4jdriver.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4jdriver.ManagedTransaction) (interface{}, error) {
		query := `
			MATCH (a:Contact {id: $a_id})
			MATCH (b:Contact {id: $b_id})
			WHERE a <> b
			MERGE (a)-[r:SIMILAR_TO]->(b)
			ON CREATE SET r.confidence = $confidence,
			              r.created_at = datetime(),
			              r.reviewed = false,
			              r.flagged_for_review = true
			ON MATCH SET  r.confidence = $confidence,
			              r.last_seen_at = datetime()
			RETURN r
		`
		_, err := tx.Run(ctx, query, map[string]interface{}{
			"a_id":       contactID.String(),
			"b_id":       similarToID.String(),
			"confidence": confidence,
		})
		return nil, err
	})
	if err != nil {
		return fmt.Errorf("neo4j create similar_to edge: %w", err)
	}
	return nil
}

// UpdateContactInteraction records an email interaction on a Contact node
// by creating an INTERACTION edge. It also updates denormalized counters.
func (s *Neo4jStore) UpdateContactInteraction(ctx context.Context, contactID uuid.UUID, threadID uuid.UUID, metadata InteractionMetadata) error {
	session := s.driver.NewSession(ctx, neo4jdriver.SessionConfig{AccessMode: neo4jdriver.AccessModeWrite})
	defer session.Close(ctx)

	_, err := session.ExecuteWrite(ctx, func(tx neo4jdriver.ManagedTransaction) (interface{}, error) {
		query := `
			MATCH (c:Contact {id: $contact_id})
			CREATE (c)-[i:INTERACTION {
				thread_id: $thread_id,
				direction: $direction,
				subject: $subject,
				sent_at: datetime($sent_at),
				created_at: datetime()
			}]->(c)
			WITH c
			SET c.interaction_count = coalesce(c.interaction_count, 0) + 1,
			    c.last_contact_date = datetime()
			RETURN c
		`
		_, err := tx.Run(ctx, query, map[string]interface{}{
			"contact_id": contactID.String(),
			"thread_id":  threadID.String(),
			"direction":  metadata.EmailDirection,
			"subject":    metadata.Subject,
			"sent_at":    metadata.SentAt.Format(time.RFC3339),
		})
		return nil, err
	})
	if err != nil {
		return fmt.Errorf("neo4j update interaction: %w", err)
	}
	return nil
}

// recordToContact converts a Neo4j record into a *models.Contact.
func recordToContact(record *neo4jdriver.Record) (*models.Contact, error) {
	getStr := func(key string) string {
		v, _ := record.Get(key)
		if v == nil {
			return ""
		}
		s, _ := v.(string)
		return s
	}
	getStrSlice := func(key string) []string {
		v, _ := record.Get(key)
		if v == nil {
			return nil
		}
		switch sv := v.(type) {
		case []string:
			return sv
		case []interface{}:
			var out []string
			for _, item := range sv {
				if s, ok := item.(string); ok {
					out = append(out, s)
				}
			}
			return out
		default:
			return nil
		}
	}
	getTime := func(key string) *time.Time {
		v, _ := record.Get(key)
		if v == nil {
			return nil
		}
		switch tv := v.(type) {
		case time.Time:
			return &tv
		case neo4jdriver.Date:
			t := tv.Time()
			return &t
		case string:
			t, err := time.Parse(time.RFC3339, tv)
			if err == nil {
				return &t
			}
		}
		return nil
	}
	getInt := func(key string) int {
		v, _ := record.Get(key)
		if v == nil {
			return 0
		}
		switch iv := v.(type) {
		case int64:
			return int(iv)
		case int:
			return iv
		case float64:
			return int(iv)
		default:
			return 0
		}
	}
	getFloat := func(key string) *float64 {
		v, _ := record.Get(key)
		if v == nil {
			return nil
		}
		switch fv := v.(type) {
		case float64:
			return &fv
		case int64:
			f := float64(fv)
			return &f
		default:
			return nil
		}
	}
	getUUID := func(key string) uuid.UUID {
		s := getStr(key)
		if s == "" {
			return uuid.Nil
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return uuid.Nil
		}
		return id
	}

	return &models.Contact{
		ID:               getUUID("id"),
		UserID:           getUUID("user_id"),
		CanonicalEmail:   getStr("canonical_email"),
		NameVariants:     getStrSlice("name_variants"),
		Organization:     func() *string { s := getStr("organization"); if s == "" { return nil }; return &s }(),
		FirstContactDate: getTime("first_contact_date"),
		LastContactDate:  getTime("last_contact_date"),
		InteractionCount: getInt("interaction_count"),
		AvgResponseHours: getFloat("avg_response_hours"),
		ToneHistory:      getStrSlice("tone_history"),
		TotalMonetaryValue: func() float64 {
			v, _ := record.Get("total_monetary_value")
			if v == nil {
				return 0
			}
			if f, ok := v.(float64); ok {
				return f
			}
			return 0
		}(),
		Projects: getStrSlice("projects"),
	}, nil
}
