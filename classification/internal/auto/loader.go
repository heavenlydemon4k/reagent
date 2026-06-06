package auto

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/decisionstack/classification/internal/models"

	"github.com/google/uuid"
)

// cacheEntry holds a cached slice of rules with expiration time.
type cacheEntry struct {
	rules     []models.AutoHandleRule
	expiresAt time.Time
}

// RuleCache provides a per-user in-memory cache with TTL for active rules.
type RuleCache struct {
	mu         sync.RWMutex
	entries    map[string]*cacheEntry
	ttl        time.Duration
	invalidCh  chan struct{}
}

// NewRuleCache creates a new per-user rule cache with the specified TTL.
func NewRuleCache(ttl time.Duration) *RuleCache {
	return &RuleCache{
		entries:   make(map[string]*cacheEntry),
		ttl:       ttl,
		invalidCh: make(chan struct{}, 1),
	}
}

// Get retrieves cached active rules for a user if present and not expired.
func (c *RuleCache) Get(userID uuid.UUID) ([]models.AutoHandleRule, bool) {
	key := userID.String()

	c.mu.RLock()
	entry, exists := c.entries[key]
	c.mu.RUnlock()

	if !exists {
		return nil, false
	}

	if time.Now().UTC().After(entry.expiresAt) {
		// Expired — remove under write lock.
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil, false
	}

	return entry.rules, true
}

// Set stores active rules for a user with the configured TTL.
func (c *RuleCache) Set(userID uuid.UUID, rules []models.AutoHandleRule) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[userID.String()] = &cacheEntry{
		rules:     rules,
		expiresAt: time.Now().UTC().Add(c.ttl),
	}
}

// InvalidateUser removes cached entries for a specific user.
func (c *RuleCache) InvalidateUser(userID uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, userID.String())
	select {
	case c.invalidCh <- struct{}{}:
	default:
	}
}

// InvalidateAll clears the entire cache.
func (c *RuleCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
	select {
	case c.invalidCh <- struct{}{}:
	default:
	}
}

// CachedStore wraps RuleStore with per-user in-memory caching.
type CachedStore struct {
	store *RuleStore
	cache *RuleCache
	mu    sync.RWMutex
}

// NewCachedStore creates a CachedStore with the given database connection.
func NewCachedStore(db *sql.DB) *CachedStore {
	return &CachedStore{
		store: NewRuleStore(db),
		cache: NewRuleCache(5 * time.Minute),
	}
}

// GetActiveRules returns active rules for a user, using cache if available.
// Rules are ordered by usage_count DESC.
func (cs *CachedStore) GetActiveRules(ctx context.Context, userID uuid.UUID) ([]models.AutoHandleRule, error) {
	// Fast path: check cache.
	if rules, ok := cs.cache.Get(userID); ok {
		return rules, nil
	}

	// Slow path: load from database.
	rules, err := cs.store.GetActiveRules(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load active rules: %w", err)
	}

	// Populate cache.
	cs.cache.Set(userID, rules)

	return rules, nil
}

// IncrementUsage bumps the usage counter and invalidates the user's cache entry
// so subsequent reads reflect the new ordering.
func (cs *CachedStore) IncrementUsage(ctx context.Context, ruleID uuid.UUID) error {
	// Get the rule first to know which user's cache to invalidate.
	rule, err := cs.store.GetByID(ctx, ruleID)
	if err != nil {
		return fmt.Errorf("get rule for cache invalidation: %w", err)
	}

	if err := cs.store.IncrementUsage(ctx, ruleID); err != nil {
		return err
	}

	// Invalidate this user's cache so the new usage_count is reflected.
	cs.cache.InvalidateUser(rule.UserID)

	return nil
}

// Create creates a new rule and invalidates the user's cache.
func (cs *CachedStore) Create(ctx context.Context, rule *models.AutoHandleRule) error {
	if err := cs.store.Create(ctx, rule); err != nil {
		return err
	}
	cs.cache.InvalidateUser(rule.UserID)
	return nil
}

// GetByID retrieves a rule by ID directly from the database (no cache).
func (cs *CachedStore) GetByID(ctx context.Context, id uuid.UUID) (*models.AutoHandleRule, error) {
	return cs.store.GetByID(ctx, id)
}

// GetByUser retrieves all rules for a user directly from the database (no cache).
func (cs *CachedStore) GetByUser(ctx context.Context, userID uuid.UUID) ([]models.AutoHandleRule, error) {
	return cs.store.GetByUser(ctx, userID)
}

// Update modifies a rule and invalidates the user's cache.
func (cs *CachedStore) Update(ctx context.Context, rule *models.AutoHandleRule) error {
	if err := cs.store.Update(ctx, rule); err != nil {
		return err
	}
	cs.cache.InvalidateUser(rule.UserID)
	return nil
}

// UpdateStatus changes a rule's status and invalidates caches.
func (cs *CachedStore) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	// Need to find the user ID for cache invalidation.
	rule, err := cs.store.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get rule for cache invalidation: %w", err)
	}

	if err := cs.store.UpdateStatus(ctx, id, status); err != nil {
		return err
	}

	cs.cache.InvalidateUser(rule.UserID)
	return nil
}

// Delete removes a rule and invalidates the user's cache.
func (cs *CachedStore) Delete(ctx context.Context, id uuid.UUID) error {
	// Need to find the user ID for cache invalidation.
	rule, err := cs.store.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get rule for cache invalidation: %w", err)
	}

	if err := cs.store.Delete(ctx, id); err != nil {
		return err
	}

	cs.cache.InvalidateUser(rule.UserID)
	return nil
}
