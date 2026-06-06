// Package auto implements the Auto-Handle engine for structured rule
// predicate evaluation with LLM fallback pattern matching.
package auto

import (
	"container/list"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/decisionstack/classification/internal/models"

	"github.com/google/uuid"
)

// lruCacheEntry is a single entry in the regex LRU cache.
type lruCacheEntry struct {
	pattern *regexp.Regexp
	elem    *list.Element
}

// PredicateEvaluator evaluates rule predicates against email attributes
// with a thread-safe LRU cache for compiled regular expressions.
type PredicateEvaluator struct {
	mu         sync.RWMutex
	regexCache map[string]*lruCacheEntry
	lruList    *list.List
	maxEntries int

	hits   uint64
	misses uint64
}

// NewPredicateEvaluator creates a new PredicateEvaluator with an LRU regex cache.
func NewPredicateEvaluator() *PredicateEvaluator {
	return &PredicateEvaluator{
		regexCache: make(map[string]*lruCacheEntry),
		lruList:    list.New(),
		maxEntries: 100,
	}
}

// Evaluate applies a rule predicate against email attributes.
// Returns true if all AllOf conditions match AND at least one AnyOf condition
// matches (when AnyOf is present).
func (pe *PredicateEvaluator) Evaluate(p models.RulePredicate, attrs models.EmailAttributes) (bool, error) {
	// Evaluate AllOf (AND) conditions.
	for _, c := range p.AllOf {
		match, err := pe.evalCondition(c, attrs)
		if err != nil {
			return false, fmt.Errorf("allOf condition failed: field=%q op=%q: %w", c.Field, c.Operator, err)
		}
		if !match {
			return false, nil
		}
	}

	// Evaluate AnyOf (OR) conditions.
	if len(p.AnyOf) > 0 {
		anyMatched := false
		for _, c := range p.AnyOf {
			match, err := pe.evalCondition(c, attrs)
			if err != nil {
				return false, fmt.Errorf("anyOf condition failed: field=%q op=%q: %w", c.Field, c.Operator, err)
			}
			if match {
				anyMatched = true
				break
			}
		}
		if !anyMatched {
			return false, nil
		}
	}

	return true, nil
}

// evalCondition evaluates a single condition against email attributes.
func (pe *PredicateEvaluator) evalCondition(c models.Condition, attrs models.EmailAttributes) (bool, error) {
	fieldVal := attrs.Get(c.Field)
	if fieldVal == nil {
		// Unknown field always fails the condition.
		return false, nil
	}

	switch c.Operator {
	case "eq":
		return pe.evalEq(fieldVal, c.Value), nil
	case "ne":
		return !pe.evalEq(fieldVal, c.Value), nil
	case "contains":
		return pe.evalContains(fieldVal, c.Value)
	case "regex":
		return pe.evalRegex(fieldVal, c.Value)
	case "gt":
		return pe.evalGt(fieldVal, c.Value)
	case "lt":
		return pe.evalLt(fieldVal, c.Value)
	case "in":
		return pe.evalIn(fieldVal, c.Value)
	case "not_in":
		ok, err := pe.evalIn(fieldVal, c.Value)
		return !ok, err
	default:
		return false, fmt.Errorf("unknown operator %q", c.Operator)
	}
}

// evalEq performs case-insensitive equality for strings, exact for other types.
func (pe *PredicateEvaluator) evalEq(a, b interface{}) bool {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && strings.EqualFold(av, bv)
	default:
		return a == b
	}
}

// evalContains checks if a string field contains a substring (case-insensitive).
func (pe *PredicateEvaluator) evalContains(fieldVal, condVal interface{}) (bool, error) {
	s, ok := fieldVal.(string)
	if !ok {
		return false, fmt.Errorf("contains requires string field, got %T", fieldVal)
	}
	v, vok := condVal.(string)
	if !vok {
		return false, fmt.Errorf("contains requires string value, got %T", condVal)
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(v)), nil
}

// evalRegex matches a string field against a regex pattern using the LRU cache.
func (pe *PredicateEvaluator) evalRegex(fieldVal, condVal interface{}) (bool, error) {
	s, ok := fieldVal.(string)
	if !ok {
		return false, fmt.Errorf("regex requires string field, got %T", fieldVal)
	}
	pattern, pok := condVal.(string)
	if !pok {
		return false, fmt.Errorf("regex requires string pattern, got %T", condVal)
	}

	re, err := pe.getCompiledRegex(pattern)
	if err != nil {
		return false, fmt.Errorf("invalid regex %q: %w", pattern, err)
	}

	return re.MatchString(s), nil
}

// getCompiledRegex retrieves a compiled regex from the LRU cache or compiles it.
func (pe *PredicateEvaluator) getCompiledRegex(pattern string) (*regexp.Regexp, error) {
	// Fast path: read lock.
	pe.mu.RLock()
	entry, exists := pe.regexCache[pattern]
	if exists {
		pe.hits++
		pe.mu.RUnlock()
		// Move to front (most recently used).
		pe.mu.Lock()
		pe.lruList.MoveToFront(entry.elem)
		pe.mu.Unlock()
		return entry.pattern, nil
	}
	pe.mu.RUnlock()

	// Slow path: compile and insert with write lock.
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	pe.mu.Lock()
	defer pe.mu.Unlock()

	// Double-check in case another goroutine compiled it.
	if entry, exists := pe.regexCache[pattern]; exists {
		pe.hits++
		pe.lruList.MoveToFront(entry.elem)
		return entry.pattern, nil
	}

	pe.misses++

	// Evict oldest if at capacity.
	if pe.lruList.Len() >= pe.maxEntries {
		oldest := pe.lruList.Back()
		if oldest != nil {
			oldestKey := oldest.Value.(string)
			delete(pe.regexCache, oldestKey)
			pe.lruList.Remove(oldest)
		}
	}

	elem := pe.lruList.PushFront(pattern)
	pe.regexCache[pattern] = &lruCacheEntry{
		pattern: re,
		elem:    elem,
	}

	return re, nil
}

// evalGt checks if field value > condition value (numeric comparison).
func (pe *PredicateEvaluator) evalGt(fieldVal, condVal interface{}) (bool, error) {
	cmp, err := compareValues(fieldVal, condVal)
	if err != nil {
		return false, err
	}
	return cmp > 0, nil
}

// evalLt checks if field value < condition value (numeric comparison).
func (pe *PredicateEvaluator) evalLt(fieldVal, condVal interface{}) (bool, error) {
	cmp, err := compareValues(fieldVal, condVal)
	if err != nil {
		return false, err
	}
	return cmp < 0, nil
}

// evalIn checks if a string field is in a slice of strings (case-insensitive).
func (pe *PredicateEvaluator) evalIn(fieldVal, condVal interface{}) (bool, error) {
	s, ok := fieldVal.(string)
	if !ok {
		return false, fmt.Errorf("in requires string field, got %T", fieldVal)
	}

	switch v := condVal.(type) {
	case []string:
		for _, item := range v {
			if strings.EqualFold(s, item) {
				return true, nil
			}
		}
		return false, nil
	case []interface{}:
		for _, item := range v {
			str, ok := item.(string)
			if ok && strings.EqualFold(s, str) {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("in requires []string or []interface{} value, got %T", condVal)
	}
}

// compareValues compares two numeric or string values.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareValues(a, b interface{}) (int, error) {
	switch av := a.(type) {
	case int:
		bv, ok := b.(int)
		if !ok {
			// Try float64.
			bf, ok := b.(float64)
			if !ok {
				return 0, fmt.Errorf("cannot compare int with %T", b)
			}
			bv = int(bf)
		}
		switch {
		case av < bv:
			return -1, nil
		case av > bv:
			return 1, nil
		default:
			return 0, nil
		}
	case float64:
		bv, ok := toFloat64(b)
		if !ok {
			return 0, fmt.Errorf("cannot compare float64 with %T", b)
		}
		switch {
		case av < bv:
			return -1, nil
		case av > bv:
			return 1, nil
		default:
			return 0, nil
		}
	case string:
		bv, ok := b.(string)
		if !ok {
			return 0, fmt.Errorf("cannot compare string with %T", b)
		}
		return strings.Compare(strings.ToLower(av), strings.ToLower(bv)), nil
	default:
		return 0, fmt.Errorf("unsupported comparison type %T", a)
	}
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// CacheStats returns current cache hit/miss statistics.
func (pe *PredicateEvaluator) CacheStats() (hits, misses uint64) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return pe.hits, pe.misses
}

// ResetCache clears all compiled regex patterns from the cache.
func (pe *PredicateEvaluator) ResetCache() {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.regexCache = make(map[string]*lruCacheEntry)
	pe.lruList.Init()
	pe.hits = 0
	pe.misses = 0
}

// CacheSize returns the current number of cached compiled regex patterns.
func (pe *PredicateEvaluator) CacheSize() int {
	pe.mu.RLock()
	defer pe.mu.RUnlock()
	return pe.lruList.Len()
}

// --- Staging window manager ---

// stagingManager tracks rules in the 48-hour staging window.
type stagingManager struct {
	mu     sync.RWMutex
	rules  map[uuid.UUID]*models.StagingRule
	ticker *time.Ticker
	done   chan struct{}
}

// newStagingManager creates a staging manager with periodic cleanup.
func newStagingManager() *stagingManager {
	sm := &stagingManager{
		rules:  make(map[uuid.UUID]*models.StagingRule),
		ticker: time.NewTicker(5 * time.Minute),
		done:   make(chan struct{}),
	}
	go sm.cleanupLoop()
	return sm
}

// cleanupLoop periodically removes expired staged rules.
func (sm *stagingManager) cleanupLoop() {
	for {
		select {
		case <-sm.ticker.C:
			sm.removeExpired()
		case <-sm.done:
			return
		}
	}
}

// removeExpired deletes rules past their 48-hour activation window.
func (sm *stagingManager) removeExpired() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	now := time.Now().UTC()
	for id, rule := range sm.rules {
		if now.After(rule.ActivatesAt) && rule.Status == "staged" {
			delete(sm.rules, id)
		}
	}
}

// Stop halts the staging manager cleanup goroutine.
func (sm *stagingManager) Stop() {
	close(sm.done)
	sm.ticker.Stop()
}
