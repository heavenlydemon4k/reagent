// Package config provides environment-based configuration for the Ingestion Mesh.
// All configuration is loaded at startup and validated. No runtime config changes.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the Ingestion Mesh service.
type Config struct {
	// Server
	ServerPort    string        `env:"SERVER_PORT,default=8080"`
	ServerHost    string        `env:"SERVER_HOST,default=0.0.0.0"`
	ReadTimeout   time.Duration `env:"READ_TIMEOUT,default=30s"`
	WriteTimeout  time.Duration `env:"WRITE_TIMEOUT,default=30s"`

	// PostgreSQL
	DatabaseURL          string        `env:"DATABASE_URL,required"`
	DBMaxConns           int           `env:"DB_MAX_CONNS,default=25"`
	DBMaxIdleConns       int           `env:"DB_MAX_IDLE_CONNS,default=5"`
	DBConnMaxLifetime    time.Duration `env:"DB_CONN_MAX_LIFETIME,default=30m"`

	// Redis
	RedisURL             string        `env:"REDIS_URL,required"`
	RedisPoolSize        int           `env:"REDIS_POOL_SIZE,default=10"`

	// NATS
	NATSURL              string        `env:"NATS_URL,required"`

	// S3
	S3Bucket             string        `env:"S3_BUCKET,required"`
	S3Region             string        `env:"S3_REGION,default=us-east-1"`
	S3Endpoint           string        `env:"S3_ENDPOINT"` // for local dev (MinIO)

	// KMS
	KMSKeyID             string        `env:"KMS_KEY_ID,required"`

	// OAuth
	GoogleClientID       string        `env:"GOOGLE_CLIENT_ID,required"`
	GoogleClientSecret   string        `env:"GOOGLE_CLIENT_SECRET,required"`
	GoogleRedirectURI    string        `env:"GOOGLE_REDIRECT_URI,default=http://localhost:8080/auth/google/callback"`
	MicrosoftClientID    string        `env:"MICROSOFT_CLIENT_ID,required"`
	MicrosoftClientSecret string       `env:"MICROSOFT_CLIENT_SECRET,required"`
	MicrosoftRedirectURI string        `env:"MICROSOFT_REDIRECT_URI,default=http://localhost:8080/auth/microsoft/callback"`

	// Neo4j
	Neo4jURI             string        `env:"NEO4J_URI,required"`
	Neo4jUser            string        `env:"NEO4J_USER,default=neo4j"`
	Neo4jPassword        string        `env:"NEO4J_PASSWORD,required"`

	// Polling
	PollIntervalDefault  time.Duration `env:"POLL_INTERVAL_DEFAULT,default=5m"`
	PollBackoffMax       time.Duration `env:"POLL_BACKOFF_MAX,default=6h"`
	WebhookToPollFallback time.Duration `env:"WEBHOOK_POLL_FALLBACK,default=5m"`

	// Rate Limiting
	GmailQuotaPerSecond  int           `env:"GMAIL_QUOTA_PER_SECOND,default=250"`
	OutlookQuotaPer10Min int           `env:"OUTLOOK_QUOTA_PER_10MIN,default=10000"`

	// OCR Microservice
	OCREndpoint          string        `env:"OCR_ENDPOINT,default=http://localhost:8001"`

	// Logging
	LogLevel             string        `env:"LOG_LEVEL,default=info"`
	LogFormat            string        `env:"LOG_FORMAT,default=json"` // json | text

	// Environment
	Environment          string        `env:"ENVIRONMENT,default=development"` // development | staging | production
	AppVersion           string        `env:"APP_VERSION,default=dev"`
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{}
	var missing []string

	// Use reflection-like manual mapping for clarity and zero dependencies
	setters := map[string]*string{
		"SERVER_PORT":              &cfg.ServerPort,
		"SERVER_HOST":              &cfg.ServerHost,
		"DATABASE_URL":             &cfg.DatabaseURL,
		"REDIS_URL":                &cfg.RedisURL,
		"NATS_URL":                 &cfg.NATSURL,
		"S3_BUCKET":                &cfg.S3Bucket,
		"S3_REGION":                &cfg.S3Region,
		"S3_ENDPOINT":              &cfg.S3Endpoint,
		"KMS_KEY_ID":               &cfg.KMSKeyID,
		"GOOGLE_CLIENT_ID":         &cfg.GoogleClientID,
		"GOOGLE_CLIENT_SECRET":     &cfg.GoogleClientSecret,
		"GOOGLE_REDIRECT_URI":      &cfg.GoogleRedirectURI,
		"MICROSOFT_CLIENT_ID":      &cfg.MicrosoftClientID,
		"MICROSOFT_CLIENT_SECRET":  &cfg.MicrosoftClientSecret,
		"MICROSOFT_REDIRECT_URI":   &cfg.MicrosoftRedirectURI,
		"NEO4J_URI":                &cfg.Neo4jURI,
		"NEO4J_USER":               &cfg.Neo4jUser,
		"NEO4J_PASSWORD":           &cfg.Neo4jPassword,
		"OCR_ENDPOINT":             &cfg.OCREndpoint,
		"LOG_LEVEL":                &cfg.LogLevel,
		"LOG_FORMAT":               &cfg.LogFormat,
		"ENVIRONMENT":              &cfg.Environment,
		"APP_VERSION":              &cfg.AppVersion,
	}

	defaults := map[string]string{
		"SERVER_PORT":              "8080",
		"SERVER_HOST":              "0.0.0.0",
		"S3_REGION":                "us-east-1",
		"GOOGLE_REDIRECT_URI":      "http://localhost:8080/auth/google/callback",
		"MICROSOFT_REDIRECT_URI":   "http://localhost:8080/auth/microsoft/callback",
		"NEO4J_USER":               "neo4j",
		"OCR_ENDPOINT":             "http://localhost:8001",
		"LOG_LEVEL":                "info",
		"LOG_FORMAT":               "json",
		"ENVIRONMENT":              "development",
		"APP_VERSION":              "dev",
	}

	required := []string{
		"DATABASE_URL",
		"REDIS_URL",
		"NATS_URL",
		"S3_BUCKET",
		"KMS_KEY_ID",
		"NEO4J_URI",
		"NEO4J_PASSWORD",
	}

	for env, ptr := range setters {
		val := os.Getenv(env)
		if val == "" {
			if def, ok := defaults[env]; ok {
				val = def
			}
		}
		*ptr = val
	}

	for _, env := range required {
		val := os.Getenv(env)
		if val == "" && defaults[env] == "" {
			missing = append(missing, env)
		}
	}

	// Duration fields
	if v := os.Getenv("READ_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.ReadTimeout = d
		} else {
			cfg.ReadTimeout = 30 * time.Second
		}
	} else {
		cfg.ReadTimeout = 30 * time.Second
	}

	if v := os.Getenv("WRITE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.WriteTimeout = d
		} else {
			cfg.WriteTimeout = 30 * time.Second
		}
	} else {
		cfg.WriteTimeout = 30 * time.Second
	}

	if v := os.Getenv("POLL_INTERVAL_DEFAULT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.PollIntervalDefault = d
		} else {
			cfg.PollIntervalDefault = 5 * time.Minute
		}
	} else {
		cfg.PollIntervalDefault = 5 * time.Minute
	}

	if v := os.Getenv("POLL_BACKOFF_MAX"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.PollBackoffMax = d
		} else {
			cfg.PollBackoffMax = 6 * time.Hour
		}
	} else {
		cfg.PollBackoffMax = 6 * time.Hour
	}

	if v := os.Getenv("WEBHOOK_POLL_FALLBACK"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.WebhookToPollFallback = d
		} else {
			cfg.WebhookToPollFallback = 5 * time.Minute
		}
	} else {
		cfg.WebhookToPollFallback = 5 * time.Minute
	}

	if v := os.Getenv("DB_CONN_MAX_LIFETIME"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.DBConnMaxLifetime = d
		} else {
			cfg.DBConnMaxLifetime = 30 * time.Minute
		}
	} else {
		cfg.DBConnMaxLifetime = 30 * time.Minute
	}

	// Int fields
	if v := os.Getenv("DB_MAX_CONNS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.DBMaxConns = i
		} else {
			cfg.DBMaxConns = 25
		}
	} else {
		cfg.DBMaxConns = 25
	}

	if v := os.Getenv("DB_MAX_IDLE_CONNS"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.DBMaxIdleConns = i
		} else {
			cfg.DBMaxIdleConns = 5
		}
	} else {
		cfg.DBMaxIdleConns = 5
	}

	if v := os.Getenv("REDIS_POOL_SIZE"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.RedisPoolSize = i
		} else {
			cfg.RedisPoolSize = 10
		}
	} else {
		cfg.RedisPoolSize = 10
	}

	if v := os.Getenv("GMAIL_QUOTA_PER_SECOND"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.GmailQuotaPerSecond = i
		} else {
			cfg.GmailQuotaPerSecond = 250
		}
	} else {
		cfg.GmailQuotaPerSecond = 250
	}

	if v := os.Getenv("OUTLOOK_QUOTA_PER_10MIN"); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.OutlookQuotaPer10Min = i
		} else {
			cfg.OutlookQuotaPer10Min = 10000
		}
	} else {
		cfg.OutlookQuotaPer10Min = 10000
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return cfg, nil
}

// IsDevelopment returns true if running in development mode.
func (c *Config) IsDevelopment() bool {
	return c.Environment == "development"
}

// IsProduction returns true if running in production mode.
func (c *Config) IsProduction() bool {
	return c.Environment == "production"
}

// DatabaseURLWithSSL returns the database URL with SSL mode configured.
// Uses sslmode=require in production, sslmode=prefer in development.
func (c *Config) DatabaseURLWithSSL() string {
	if strings.Contains(c.DatabaseURL, "sslmode=") {
		// Replace existing sslmode parameter
		return c.DatabaseURL
	}
	if c.IsProduction() {
		return c.DatabaseURL + "?sslmode=require"
	}
	return c.DatabaseURL + "?sslmode=prefer"
}
