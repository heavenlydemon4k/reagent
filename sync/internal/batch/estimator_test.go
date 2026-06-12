// Package batch_test provides unit tests for clear-time estimation.
package batch

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// ---------------------------------------------------------------------------
// Mock Redis for estimator tests
// ---------------------------------------------------------------------------

// mockRedis implements the subset of redis.UniversalClient that
// ClearTimeEstimator uses: Get, Set, Pipeline, and Del.
type mockRedis struct {
	data map[string]string
}

func newMockRedis() *mockRedis {
	return &mockRedis{data: make(map[string]string)}
}

func (m *mockRedis) Get(ctx context.Context, key string) *redis.StringCmd {
	if val, ok := m.data[key]; ok {
		return redis.NewStringResult(val, nil)
	}
	return redis.NewStringResult("", redis.Nil)
}

func (m *mockRedis) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd {
	m.data[key] = fmt.Sprintf("%v", value)
	return redis.NewStatusResult("OK", nil)
}

func (m *mockRedis) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	count := int64(0)
	for _, k := range keys {
		if _, ok := m.data[k]; ok {
			delete(m.data, k)
			count++
		}
	}
	return redis.NewIntResult(count, nil)
}

// Required to satisfy redis.UniversalClient interface (but unused by estimator)
func (m *mockRedis) Ping(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("PONG", nil)
}

func (m *mockRedis) Close() error { return nil }

// ---------------------------------------------------------------------------
// Helper assertions
// ---------------------------------------------------------------------------

func assertInDelta(t *testing.T, want, got, delta float64, msg string) {
	t.Helper()
	if math.Abs(want-got) > delta {
		t.Errorf("%s: want %f ±%f, got %f", msg, want, delta, got)
	}
}

// ---------------------------------------------------------------------------
// Tests: NewClearTimeEstimator
// ---------------------------------------------------------------------------

func TestNewClearTimeEstimator(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	if est == nil {
		t.Fatal("NewClearTimeEstimator returned nil")
	}
}

// ---------------------------------------------------------------------------
// Tests: timingKey and lastClearKey generation
// ---------------------------------------------------------------------------

func TestTimingKey(t *testing.T) {
	uid := uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	key := timingKey(uid)
	wantPrefix := TimingKeyPrefix + ":" + uid.String() + ":avg_seconds_per_card"
	if key != wantPrefix {
		t.Errorf("timingKey: want %q, got %q", wantPrefix, key)
	}
}

func TestLastClearKey(t *testing.T) {
	uid := uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")
	key := lastClearKey(uid)
	wantPrefix := TimingKeyPrefix + ":" + uid.String() + ":last_cleared_at"
	if key != wantPrefix {
		t.Errorf("lastClearKey: want %q, got %q", wantPrefix, key)
	}
}

// ---------------------------------------------------------------------------
// Tests: Estimate with no history (defaults)
// ---------------------------------------------------------------------------

func TestEstimate_NoHistory(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	// No history → uses DefaultSecondsPerCard (45s)
	// 10 cards * 45s / 60 = 7.5 → ceil = 8
	mins := est.Estimate(ctx, uid, 10)
	want := int(math.Ceil(float64(10) * DefaultSecondsPerCard / 60.0))
	assertEqualInt(t, want, mins, "estimate with no history")
}

func TestEstimate_ZeroCards(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	mins := est.Estimate(ctx, uid, 0)
	assertEqualInt(t, 0, mins, "estimate with 0 cards")
}

func TestEstimate_NegativeCards(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	mins := est.Estimate(ctx, uid, -5)
	assertEqualInt(t, 0, mins, "estimate with negative cards")
}

// ---------------------------------------------------------------------------
// Tests: Estimate with recorded history
// ---------------------------------------------------------------------------

func TestEstimate_WithHistory(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	// Record some timing data: 30s and 60s
	est.RecordCardCleared(ctx, uid, 30.0)
	est.RecordCardCleared(ctx, uid, 60.0)

	// EMA calculation:
	// After 30s:  avg = 0.2*30 + 0.8*45 = 6 + 36 = 42.0
	// After 60s:  avg = 0.2*60 + 0.8*42 = 12 + 33.6 = 45.6
	// 10 cards * 45.6s / 60 = 7.6 → ceil = 8
	mins := est.Estimate(ctx, uid, 10)
	if mins < 1 {
		t.Errorf("estimate should be at least 1 minute, got %d", mins)
	}
}

// ---------------------------------------------------------------------------
// Tests: Estimate with exactly 1 card
// ---------------------------------------------------------------------------

func TestEstimate_OneCard(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	mins := est.Estimate(ctx, uid, 1)
	// 1 * 45 / 60 = 0.75 → ceil = 1 (minimum)
	assertEqualInt(t, 1, mins, "estimate with 1 card should be at least 1")
}

// ---------------------------------------------------------------------------
// Tests: RecordCardCleared EMA calculation
// ---------------------------------------------------------------------------

func TestRecordCardCleared_EMA(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	// First sample at default: avg = 45.0
	// After 30s: avg = 0.2*30 + 0.8*45 = 42.0
	est.RecordCardCleared(ctx, uid, 30.0)
	avg := est.GetAverageSeconds(ctx, uid)
	assertInDelta(t, 42.0, avg, 0.1, "EMA after first sample")

	// After 60s: avg = 0.2*60 + 0.8*42 = 45.6
	est.RecordCardCleared(ctx, uid, 60.0)
	avg = est.GetAverageSeconds(ctx, uid)
	assertInDelta(t, 45.6, avg, 0.1, "EMA after second sample")

	// After 30s: avg = 0.2*30 + 0.8*45.6 = 6 + 36.48 = 42.48
	est.RecordCardCleared(ctx, uid, 30.0)
	avg = est.GetAverageSeconds(ctx, uid)
	assertInDelta(t, 42.48, avg, 0.1, "EMA after third sample")
}

// ---------------------------------------------------------------------------
// Tests: RecordCardCleared clamping
// ---------------------------------------------------------------------------

func TestRecordCardCleared_LowClamp(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	// Below MinSecondsPerCard → clamped to 5
	est.RecordCardCleared(ctx, uid, 1.0)
	avg := est.GetAverageSeconds(ctx, uid)
	// avg = 0.2*5 + 0.8*45 = 1 + 36 = 37
	assertInDelta(t, 37.0, avg, 0.1, "low clamp EMA")
}

func TestRecordCardCleared_HighClamp(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	// Above MaxSecondsPerCard → clamped to 600
	est.RecordCardCleared(ctx, uid, 999.0)
	avg := est.GetAverageSeconds(ctx, uid)
	// avg = 0.2*600 + 0.8*45 = 120 + 36 = 156
	assertInDelta(t, 156.0, avg, 0.1, "high clamp EMA")
}

// ---------------------------------------------------------------------------
// Tests: GetAverageSeconds
// ---------------------------------------------------------------------------

func TestGetAverageSeconds_DefaultWhenNoData(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	avg := est.GetAverageSeconds(ctx, uid)
	assertInDelta(t, DefaultSecondsPerCard, avg, 0.001, "default average")
}

func TestGetAverageSeconds_ReturnsStoredValue(t *testing.T) {
	mr := newMockRedis()
	uid := uuid.New()
	key := timingKey(uid)
	mr.data[key] = "60.5"

	est := NewClearTimeEstimator(mr)
	ctx := context.Background()
	avg := est.GetAverageSeconds(ctx, uid)
	assertInDelta(t, 60.5, avg, 0.001, "stored average")
}

func TestGetAverageSeconds_InvalidValueReturnsDefault(t *testing.T) {
	mr := newMockRedis()
	uid := uuid.New()
	key := timingKey(uid)
	mr.data[key] = "not-a-number"

	est := NewClearTimeEstimator(mr)
	ctx := context.Background()
	avg := est.GetAverageSeconds(ctx, uid)
	assertInDelta(t, DefaultSecondsPerCard, avg, 0.001, "default for invalid value")
}

func TestGetAverageSeconds_SanityClampLow(t *testing.T) {
	mr := newMockRedis()
	uid := uuid.New()
	key := timingKey(uid)
	mr.data[key] = "1.0" // Below MinSecondsPerCard

	est := NewClearTimeEstimator(mr)
	ctx := context.Background()
	avg := est.GetAverageSeconds(ctx, uid)
	assertInDelta(t, MinSecondsPerCard, avg, 0.001, "low sanity clamp")
}

func TestGetAverageSeconds_SanityClampHigh(t *testing.T) {
	mr := newMockRedis()
	uid := uuid.New()
	key := timingKey(uid)
	mr.data[key] = "9999.0" // Above MaxSecondsPerCard

	est := NewClearTimeEstimator(mr)
	ctx := context.Background()
	avg := est.GetAverageSeconds(ctx, uid)
	assertInDelta(t, MaxSecondsPerCard, avg, 0.001, "high sanity clamp")
}

// ---------------------------------------------------------------------------
// Tests: Reset
// ---------------------------------------------------------------------------

func TestReset(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	// Record data
	est.RecordCardCleared(ctx, uid, 30.0)
	key := timingKey(uid)
	if _, ok := mr.data[key]; !ok {
		t.Fatal("data should be stored before reset")
	}

	// Reset
	err := est.Reset(ctx, uid)
	assertNoError(t, err, "reset")
	if _, ok := mr.data[key]; ok {
		t.Error("timing key should be deleted after reset")
	}
}

// ---------------------------------------------------------------------------
// Tests: GetLastClearedAt
// ---------------------------------------------------------------------------

func TestGetLastClearedAt_NoData(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	ts := est.GetLastClearedAt(ctx, uid)
	if !ts.IsZero() {
		t.Error("should return zero time when no data")
	}
}

func TestGetLastClearedAt_WithData(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	before := time.Now().UTC().Add(-time.Second).Unix()
	est.RecordCardCleared(ctx, uid, 30.0)
	after := time.Now().UTC().Add(time.Second).Unix()

	ts := est.GetLastClearedAt(ctx, uid)
	unixTs := ts.Unix()
	if unixTs < before || unixTs > after {
		t.Errorf("timestamp %d not in range [%d, %d]", unixTs, before, after)
	}
}

// ---------------------------------------------------------------------------
// Tests: Constants
// ---------------------------------------------------------------------------

func TestEstimatorConstants(t *testing.T) {
	assertInDelta(t, 45.0, DefaultSecondsPerCard, 0.001, "DefaultSecondsPerCard")
	assertInDelta(t, 0.2, EMAAlpha, 0.001, "EMAAlpha")
	assertInDelta(t, 600.0, MaxSecondsPerCard, 0.001, "MaxSecondsPerCard")
	assertInDelta(t, 5.0, MinSecondsPerCard, 0.001, "MinSecondsPerCard")
}

// ---------------------------------------------------------------------------
// Table-driven: estimate scenarios
// ---------------------------------------------------------------------------

func TestEstimate_Scenarios(t *testing.T) {
	tests := []struct {
		name          string
		recordedTimes []float64
		cardCount     int
		wantMin       int // minimum acceptable estimate
		wantMax       int // maximum acceptable estimate
	}{
		{
			name:          "no history, 10 cards",
			recordedTimes: nil,
			cardCount:     10,
			wantMin:       7,
			wantMax:       8,
		},
		{
			name:          "fast user (10s avg), 10 cards",
			recordedTimes: []float64{10, 10, 10, 10, 10},
			cardCount:     10,
			wantMin:       3,
			wantMax:       5,
		},
		{
			name:          "slow user (120s avg), 10 cards",
			recordedTimes: []float64{120, 120, 120, 120, 120},
			cardCount:     10,
			wantMin:       15,
			wantMax:       17,
		},
		{
			name:          "mixed times converge, 20 cards",
			recordedTimes: []float64{30, 60, 30, 60, 30, 60},
			cardCount:     20,
			wantMin:       10,
			wantMax:       20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mr := newMockRedis()
			est := NewClearTimeEstimator(mr)
			uid := uuid.New()
			ctx := context.Background()

			for _, elapsed := range tt.recordedTimes {
				est.RecordCardCleared(ctx, uid, elapsed)
			}

			got := est.Estimate(ctx, uid, tt.cardCount)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("Estimate(%d cards) = %d, want in range [%d, %d]",
					tt.cardCount, got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Tests: estimate edge cases
// ---------------------------------------------------------------------------

func TestEstimate_LargeCardCount(t *testing.T) {
	mr := newMockRedis()
	est := NewClearTimeEstimator(mr)
	uid := uuid.New()
	ctx := context.Background()

	// 100 cards at default 45s each = 100 * 45 / 60 = 75 minutes
	mins := est.Estimate(ctx, uid, 100)
	want := int(math.Ceil(float64(100) * DefaultSecondsPerCard / 60.0))
	assertEqualInt(t, want, mins, "large card count estimate")
}

// Additional interface stubs for the mockRedis
func (m *mockRedis) GetSet(ctx context.Context, key string, value interface{}) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}
func (m *mockRedis) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) *redis.BoolCmd {
	return redis.NewBoolResult(true, nil)
}
func (m *mockRedis) SetXX(ctx context.Context, key string, value interface{}, ttl time.Duration) *redis.BoolCmd {
	return redis.NewBoolResult(true, nil)
}
func (m *mockRedis) SetEX(ctx context.Context, key string, value interface{}, ttl time.Duration) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) Incr(ctx context.Context, key string) *redis.IntCmd {
	return redis.NewIntResult(1, nil)
}
func (m *mockRedis) IncrBy(ctx context.Context, key string, value int64) *redis.IntCmd {
	return redis.NewIntResult(1, nil)
}
func (m *mockRedis) Decr(ctx context.Context, key string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) DecrBy(ctx context.Context, key string, value int64) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) Expire(ctx context.Context, key string, ttl time.Duration) *redis.BoolCmd {
	return redis.NewBoolResult(true, nil)
}
func (m *mockRedis) ExpireAt(ctx context.Context, key string, tm time.Time) *redis.BoolCmd {
	return redis.NewBoolResult(true, nil)
}
func (m *mockRedis) TTL(ctx context.Context, key string) *redis.DurationCmd {
	return redis.NewDurationResult(time.Hour, nil)
}
func (m *mockRedis) Exists(ctx context.Context, keys ...string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) Do(ctx context.Context, args ...interface{}) *redis.Cmd {
	return redis.NewCmdResult(nil, nil)
}

func (m *mockRedis) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return nil
}
func (m *mockRedis) PSubscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return nil
}
func (m *mockRedis) SAdd(ctx context.Context, key string, members ...interface{}) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) SRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) SMembers(ctx context.Context, key string) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) SIsMember(ctx context.Context, key string, member interface{}) *redis.BoolCmd {
	return redis.NewBoolResult(false, nil)
}
func (m *mockRedis) SCard(ctx context.Context, key string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) HGet(ctx context.Context, key, field string) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}
func (m *mockRedis) HSet(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) HDel(ctx context.Context, key string, fields ...string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) HGetAll(ctx context.Context, key string) *redis.MapStringStringCmd {
	return redis.NewMapStringStringResult(nil, nil)
}
func (m *mockRedis) HLen(ctx context.Context, key string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) HMGet(ctx context.Context, key string, fields ...string) *redis.SliceCmd {
	return redis.NewSliceResult(nil, nil)
}
func (m *mockRedis) HMSet(ctx context.Context, key string, values ...interface{}) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) HKeys(ctx context.Context, key string) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) HVals(ctx context.Context, key string) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) LPop(ctx context.Context, key string) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}
func (m *mockRedis) RPop(ctx context.Context, key string) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}
func (m *mockRedis) LLen(ctx context.Context, key string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) BLPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ZRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ZRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) ZRevRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) ZCard(ctx context.Context, key string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ZScore(ctx context.Context, key, member string) *redis.FloatCmd {
	return redis.NewFloatResult(0, redis.Nil)
}
func (m *mockRedis) ZRank(ctx context.Context, key, member string) *redis.IntCmd {
	return redis.NewIntResult(0, redis.Nil)
}
func (m *mockRedis) ZCount(ctx context.Context, key, min, max string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ZRemRangeByRank(ctx context.Context, key string, start, stop int64) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ZRemRangeByScore(ctx context.Context, key, min, max string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ZIncrBy(ctx context.Context, key string, increment float64, member string) *redis.FloatCmd {
	return redis.NewFloatResult(0, nil)
}
func (m *mockRedis) Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) FlushDB(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) FlushAll(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) Rename(ctx context.Context, key, newkey string) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) Keys(ctx context.Context, pattern string) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, nil)
}
func (m *mockRedis) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	return redis.NewSliceResult(nil, nil)
}
func (m *mockRedis) MSet(ctx context.Context, values ...interface{}) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) MSetNX(ctx context.Context, values ...interface{}) *redis.BoolCmd {
	return redis.NewBoolResult(true, nil)
}
func (m *mockRedis) GetEx(ctx context.Context, key string, expiration time.Duration) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}
func (m *mockRedis) GetDel(ctx context.Context, key string) *redis.StringCmd {
	return redis.NewStringResult("", redis.Nil)
}
func (m *mockRedis) Type(ctx context.Context, key string) *redis.StatusCmd {
	return redis.NewStatusResult("none", nil)
}
func (m *mockRedis) Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd {
	return redis.NewScanCmdResult(nil, 0, nil)
}
func (m *mockRedis) StrLen(ctx context.Context, key string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) Append(ctx context.Context, key, value string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) GetRange(ctx context.Context, key string, start, end int64) *redis.StringCmd {
	return redis.NewStringResult("", nil)
}
func (m *mockRedis) SetRange(ctx context.Context, key string, offset int64, value string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) Watch(ctx context.Context, fn func(*redis.Tx) error, keys ...string) error {
	return nil
}
func (m *mockRedis) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	return redis.NewCmdResult(nil, nil)
}
func (m *mockRedis) EvalSha(ctx context.Context, sha1 string, keys []string, args ...interface{}) *redis.Cmd {
	return redis.NewCmdResult(nil, nil)
}
func (m *mockRedis) ScriptExists(ctx context.Context, hashes ...string) *redis.BoolSliceCmd {
	return redis.NewBoolSliceResult(nil, nil)
}
func (m *mockRedis) ScriptLoad(ctx context.Context, script string) *redis.StringCmd {
	return redis.NewStringResult("", nil)
}
func (m *mockRedis) ScriptFlush(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) ScriptKill(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) DebugObject(ctx context.Context, key string) *redis.StringCmd {
	return redis.NewStringResult("", nil)
}
func (m *mockRedis) MemoryUsage(ctx context.Context, key string, samples ...int) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ClientGetName(ctx context.Context) *redis.StringCmd {
	return redis.NewStringResult("", nil)
}
func (m *mockRedis) ClientKill(ctx context.Context, ipPort string) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) ClientKillByFilter(ctx context.Context, keys ...string) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ClientList(ctx context.Context) *redis.StringCmd {
	return redis.NewStringResult("", nil)
}
func (m *mockRedis) ClientPause(ctx context.Context, dur time.Duration) *redis.BoolCmd {
	return redis.NewBoolResult(true, nil)
}
func (m *mockRedis) ClientID(ctx context.Context) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ClientUnblock(ctx context.Context, id int64) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ClientUnblockWithError(ctx context.Context, id int64) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) ConfigGet(ctx context.Context, parameter string) *redis.SliceCmd {
	return redis.NewSliceResult(nil, nil)
}
func (m *mockRedis) ConfigSet(ctx context.Context, parameter, value string) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) ConfigResetStat(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) ConfigRewrite(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) DBSize(ctx context.Context) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) Info(ctx context.Context, section ...string) *redis.StringCmd {
	return redis.NewStringResult("", nil)
}
func (m *mockRedis) LastSave(ctx context.Context) *redis.IntCmd {
	return redis.NewIntResult(0, nil)
}
func (m *mockRedis) Save(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) BgSave(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) BgRewriteAOF(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) SlowLogGet(ctx context.Context, num int64) *redis.SlowLogCmd {
	return redis.NewSlowLogCmd(context.Background())
}
func (m *mockRedis) Time(ctx context.Context) *redis.TimeCmd {
	return redis.NewTimeCmd(context.Background())
}
func (m *mockRedis) MemoryFlushAll(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) Command(ctx context.Context) *redis.CommandsInfoCmd {
	return redis.NewCommandsInfoCmdResult(nil, nil)
}
func (m *mockRedis) CommandsInfo(ctx context.Context) *redis.CommandsInfoCmd {
	return redis.NewCommandsInfoCmdResult(nil, nil)
}
func (m *mockRedis) CommandInfo(ctx context.Context, names ...string) *redis.CommandsInfoCmd {
	return redis.NewCommandsInfoCmdResult(nil, nil)
}
func (m *mockRedis) ClientSetName(ctx context.Context, name string) *redis.BoolCmd {
	return redis.NewBoolResult(true, nil)
}
func (m *mockRedis) ClientReadOnly(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) ClientReadWrite(ctx context.Context) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) ClientInfo(ctx context.Context) *redis.StringCmd {
	return redis.NewStringResult("", nil)
}
func (m *mockRedis) ClientSetInfo(ctx context.Context, key, value string) *redis.StatusCmd {
	return redis.NewStatusResult("OK", nil)
}
func (m *mockRedis) PoolStats() *redis.PoolStats {
	return &redis.PoolStats{}
}
func (m *mockRedis) Options() *redis.Options {
	return &redis.Options{}
}
func (m *mockRedis) AddHook(hook redis.Hook) {}

// Helper: parse float from string for assertions
func parseFloat(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("parse float %q: %v", s, err)
	}
	return v
}
