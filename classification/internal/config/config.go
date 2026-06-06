// Package config loads environment-based configuration for the Classification Core.
// All values have sensible defaults for local development; production overrides via env.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Build-time variables (injected via ldflags).
var (
	BuildTime    = "unknown"
	GitRevision  = "unknown"
)

// Config aggregates all configuration for the service.
type Config struct {
	// HTTP server
	ServerPort         string        `env:"SERVER_PORT" default:"8080"`
	ServerReadTimeout  time.Duration `env:"SERVER_READ_TIMEOUT" default:"5s"`
	ServerWriteTimeout time.Duration `env:"SERVER_WRITE_TIMEOUT" default:"10s"`

	// PostgreSQL
	DBHost     string `env:"DB_HOST" default:"localhost"`
	DBPort     int    `env:"DB_PORT" default:"5432"`
	DBUser     string `env:"DB_USER" default:"classification"`
	DBPassword string `env:"DB_PASSWORD" default:"classification"`
	DBName     string `env:"DB_NAME" default:"classification"`
	DBSSLMode  string `env:"DB_SSLMODE" default:"disable"`
	DBPoolMax  int    `env:"DB_POOL_MAX" default:"10"`

	// Redis
	RedisAddr     string `env:"REDIS_ADDR" default:"localhost:6379"`
	RedisPassword string `env:"REDIS_PASSWORD" default:""`
	RedisDB       int    `env:"REDIS_DB" default:"0"`

	// NATS JetStream
	NATSURL               string        `env:"NATS_URL" default:"nats://localhost:4222"`
	NATSStream            string        `env:"NATS_STREAM" default:"EMAIL_STREAM"`
	NATSConsumerName      string        `env:"NATS_CONSUMER_NAME" default:"classification-worker"`
	NATSSubjectEmail      string        `env:"NATS_SUBJECT_EMAIL" default:"email.ingested"`
	NATSSubjectIntelligence string      `env:"NATS_SUBJECT_INTELLIGENCE" default:"intelligence.compress"`
	NATSSubjectExtracted  string        `env:"NATS_SUBJECT_EXTRACTED" default:"ExtractCompleted"`
	NATSSubjectAuto       string        `env:"NATS_SUBJECT_AUTO" default:"AutoHandled"`
	NATSSubjectDLQ        string        `env:"NATS_SUBJECT_DLQ" default:"email.ingested.dlq"`
	NATSMaxDeliver        int           `env:"NATS_MAX_DELIVER" default:"5"`
	NATSBatchSize         int           `env:"NATS_BATCH_SIZE" default:"100"`
	NATSFetchTimeout      time.Duration `env:"NATS_FETCH_TIMEOUT" default:"5s"`
	NATSConsumerMaxRetries int          `env:"NATS_CONSUMER_MAX_RETRIES" default:"3"`
	NATSConsumerRetryBackoff time.Duration `env:"NATS_CONSUMER_RETRY_BACKOFF" default:"1s"`

	// LLM (Bedrock / Claude Haiku)
	LLMEnabled        bool    `env:"LLM_ENABLED" default:"true"`
	LLMAPIKey         string  `env:"LLM_API_KEY" default:""`
	LLMAPIRegion      string  `env:"LLM_API_REGION" default:"us-east-1"`
	LLMModelID        string  `env:"LLM_MODEL_ID" default:"anthropic.claude-3-haiku-20240307-v1:0"`
	LLMEndpoint       string  `env:"LLM_ENDPOINT" default:""`
	LLMConfidenceFloor float64 `env:"LLM_CONFIDENCE_FLOOR" default:"0.92"`

	// Classification
	ConfidenceFloor    float64       `env:"CONFIDENCE_FLOOR" default:"0.92"`
	StagingWindow      time.Duration `env:"STAGING_WINDOW" default:"48h"`
	MaxBodyPreviewLen  int           `env:"MAX_BODY_PREVIEW_LEN" default:"500"`

	// Observability
	LogLevel  string `env:"LOG_LEVEL" default:"info"` // debug | info | warn | error
	LogFormat string `env:"LOG_FORMAT" default:"json"` // json | text
	MetricsPort string `env:"METRICS_PORT" default:"9090"`
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{}

	// Server
	cfg.ServerPort = getEnv("SERVER_PORT", "8080")
	cfg.ServerReadTimeout = getDuration("SERVER_READ_TIMEOUT", 5*time.Second)
	cfg.ServerWriteTimeout = getDuration("SERVER_WRITE_TIMEOUT", 10*time.Second)

	// DB
	cfg.DBHost = getEnv("DB_HOST", "localhost")
	cfg.DBPort = getInt("DB_PORT", 5432)
	cfg.DBUser = getEnv("DB_USER", "classification")
	cfg.DBPassword = getEnv("DB_PASSWORD", "classification")
	cfg.DBName = getEnv("DB_NAME", "classification")
	cfg.DBSSLMode = getEnv("DB_SSLMODE", "prefer")
	cfg.DBPoolMax = getInt("DB_POOL_MAX", 10)

	// Redis
	cfg.RedisAddr = getEnv("REDIS_ADDR", "localhost:6379")
	cfg.RedisPassword = getEnv("REDIS_PASSWORD", "")
	cfg.RedisDB = getInt("REDIS_DB", 0)

	// NATS
	cfg.NATSURL = getEnv("NATS_URL", "nats://localhost:4222")
	cfg.NATSStream = getEnv("NATS_STREAM", "EMAIL_STREAM")
	cfg.NATSConsumerName = getEnv("NATS_CONSUMER_NAME", "classification-worker")
	cfg.NATSSubjectEmail = getEnv("NATS_SUBJECT_EMAIL", "email.ingested")
	cfg.NATSSubjectIntelligence = getEnv("NATS_SUBJECT_INTELLIGENCE", "intelligence.compress")
	cfg.NATSSubjectExtracted = getEnv("NATS_SUBJECT_EXTRACTED", "ExtractCompleted")
	cfg.NATSSubjectAuto = getEnv("NATS_SUBJECT_AUTO", "AutoHandled")
	cfg.NATSSubjectDLQ = getEnv("NATS_SUBJECT_DLQ", "email.ingested.dlq")
	cfg.NATSMaxDeliver = getInt("NATS_MAX_DELIVER", 5)
	cfg.NATSBatchSize = getInt("NATS_BATCH_SIZE", 100)
	cfg.NATSFetchTimeout = getDuration("NATS_FETCH_TIMEOUT", 5*time.Second)
	cfg.NATSConsumerMaxRetries = getInt("NATS_CONSUMER_MAX_RETRIES", 3)
	cfg.NATSConsumerRetryBackoff = getDuration("NATS_CONSUMER_RETRY_BACKOFF", 1*time.Second)

	// LLM
	cfg.LLMEnabled = getBool("LLM_ENABLED", true)
	cfg.LLMAPIKey = getEnv("LLM_API_KEY", "")
	cfg.LLMAPIRegion = getEnv("LLM_API_REGION", "us-east-1")
	cfg.LLMModelID = getEnv("LLM_MODEL_ID", "anthropic.claude-3-haiku-20240307-v1:0")
	cfg.LLMEndpoint = getEnv("LLM_ENDPOINT", "")
	cfg.LLMConfidenceFloor = getFloat64("LLM_CONFIDENCE_FLOOR", 0.92)

	// Classification
	cfg.ConfidenceFloor = getFloat64("CONFIDENCE_FLOOR", 0.92)
	cfg.StagingWindow = getDuration("STAGING_WINDOW", 48*time.Hour)
	cfg.MaxBodyPreviewLen = getInt("MAX_BODY_PREVIEW_LEN", 500)

	// Observability
	cfg.LogLevel = getEnv("LOG_LEVEL", "info")
	cfg.LogFormat = getEnv("LOG_FORMAT", "json")
	cfg.MetricsPort = getEnv("METRICS_PORT", "9090")

	return cfg, cfg.validate()
}

// DSN returns the PostgreSQL connection string.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

// DSNWithSSL returns the PostgreSQL connection string with production-safe SSL settings.
// In production, forces sslmode=require. In development, uses sslmode=prefer.
func (c *Config) DSNWithSSL() string {
	env := os.Getenv("ENVIRONMENT")
	if env == "production" {
		return fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=require",
			c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
		)
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=prefer",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName,
	)
}

func (c *Config) validate() error {
	if c.ConfidenceFloor < 0 || c.ConfidenceFloor > 1 {
		return fmt.Errorf("CONFIDENCE_FLOOR must be between 0 and 1, got %f", c.ConfidenceFloor)
	}
	if c.NATSMaxDeliver < 1 {
		return fmt.Errorf("NATS_MAX_DELIVER must be >= 1")
	}
	if c.NATSConsumerMaxRetries < 1 {
		return fmt.Errorf("NATS_CONSUMER_MAX_RETRIES must be >= 1")
	}
	if c.NATSConsumerRetryBackoff < 1*time.Millisecond {
		return fmt.Errorf("NATS_CONSUMER_RETRY_BACKOFF must be >= 1ms")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func getFloat64(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
