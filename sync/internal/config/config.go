// Package config loads and validates environment configuration for the sync service.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	ServerPort  string `env:"SERVER_PORT" default:"8080"`
	ServerHost  string `env:"SERVER_HOST" default:"0.0.0.0"`
	Environment string `env:"ENVIRONMENT" default:"development"`

	// Database
	DatabaseURL string `env:"DATABASE_URL" default:"postgres://sync:sync@localhost:5432/decisionstack?sslmode=disable"`
	DBMaxOpen   int    `env:"DB_MAX_OPEN" default:"25"`
	DBMaxIdle   int    `env:"DB_MAX_IDLE" default:"5"`

	// Redis
	RedisAddr     string `env:"REDIS_ADDR" default:"localhost:6379"`
	RedisPassword string `env:"REDIS_PASSWORD" default:""`
	RedisDB       int    `env:"REDIS_DB" default:"0"`

	// NATS
	NATSURL          string `env:"NATS_URL" default:"nats://localhost:4222"`
	NATSSubjectDLQ   string `env:"NATS_SUBJECT_DLQ" default:"sync.notify.dlq"`
	NATSMaxDeliver   int    `env:"NATS_MAX_DELIVER" default:"5"`

	// JWT
	JWTSecret        string        `env:"JWT_SECRET" default:"dev-secret-change-in-production"`
	JWTAccessExpiry  time.Duration `env:"JWT_ACCESS_EXPIRY" default:"15m"`
	JWTRefreshExpiry time.Duration `env:"JWT_REFRESH_EXPIRY" default:"168h"`

	// WebSocket
	WSReadBufferSize  int           `env:"WS_READ_BUFFER_SIZE" default:"1024"`
	WSWriteBufferSize int           `env:"WS_WRITE_BUFFER_SIZE" default:"1024"`
	WSPongWait        time.Duration `env:"WS_PONG_WAIT" default:"60s"`
	WSPingPeriod      time.Duration `env:"WS_PING_PERIOD" default:"54s"`
	WSWriteWait       time.Duration `env:"WS_WRITE_WAIT" default:"10s"`

	// Push Notifications
	FCMEnabled    bool   `env:"FCM_ENABLED" default:"false"`
	FCMCredentials string `env:"FCM_CREDENTIALS" default:""`
	APNSKeyID     string `env:"APNS_KEY_ID" default:""`
	APNSTeamID    string `env:"APNS_TEAM_ID" default:""`
	APNSBundleID  string `env:"APNS_BUNDLE_ID" default:""`
	APNSKeyPath   string `env:"APNS_KEY_PATH" default:""`
	QuietHoursStart int  `env:"QUIET_HOURS_START" default:"22"`
	QuietHoursEnd   int  `env:"QUIET_HOURS_END" default:"8"`

	// Sync
	SyncBatchSize     int `env:"SYNC_BATCH_SIZE" default:"100"`
	SyncMaxPageSize   int `env:"SYNC_MAX_PAGE_SIZE" default:"500"`

	// Logging
	LogLevel  string `env:"LOG_LEVEL" default:"info"`
	LogFormat string `env:"LOG_FORMAT" default:"json"`
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{
		ServerPort:       getEnv("SERVER_PORT", "8080"),
		ServerHost:       getEnv("SERVER_HOST", "0.0.0.0"),
		Environment:      getEnv("ENVIRONMENT", "development"),
		DatabaseURL:      getEnv("DATABASE_URL", "postgres://sync:sync@localhost:5432/decisionstack?sslmode=prefer"),
		DBMaxOpen:        getEnvInt("DB_MAX_OPEN", 25),
		DBMaxIdle:        getEnvInt("DB_MAX_IDLE", 5),
		RedisAddr:        getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:    getEnv("REDIS_PASSWORD", ""),
		RedisDB:          getEnvInt("REDIS_DB", 0),
		NATSURL:          getEnv("NATS_URL", "nats://localhost:4222"),
		NATSSubjectDLQ:   getEnv("NATS_SUBJECT_DLQ", "sync.notify.dlq"),
		NATSMaxDeliver:   getEnvInt("NATS_MAX_DELIVER", 5),
		JWTSecret:        getEnv("JWT_SECRET", "dev-secret-change-in-production"),
		JWTAccessExpiry:  getEnvDuration("JWT_ACCESS_EXPIRY", 15*time.Minute),
		JWTRefreshExpiry: getEnvDuration("JWT_REFRESH_EXPIRY", 168*time.Hour),
		WSReadBufferSize:  getEnvInt("WS_READ_BUFFER_SIZE", 1024),
		WSWriteBufferSize: getEnvInt("WS_WRITE_BUFFER_SIZE", 1024),
		WSPongWait:       getEnvDuration("WS_PONG_WAIT", 60*time.Second),
		WSPingPeriod:     getEnvDuration("WS_PING_PERIOD", 54*time.Second),
		WSWriteWait:      getEnvDuration("WS_WRITE_WAIT", 10*time.Second),
		FCMEnabled:       getEnvBool("FCM_ENABLED", false),
		FCMCredentials:   getEnv("FCM_CREDENTIALS", ""),
		APNSKeyID:        getEnv("APNS_KEY_ID", ""),
		APNSTeamID:       getEnv("APNS_TEAM_ID", ""),
		APNSBundleID:     getEnv("APNS_BUNDLE_ID", ""),
		APNSKeyPath:      getEnv("APNS_KEY_PATH", ""),
		QuietHoursStart:  getEnvInt("QUIET_HOURS_START", 22),
		QuietHoursEnd:    getEnvInt("QUIET_HOURS_END", 8),
		SyncBatchSize:    getEnvInt("SYNC_BATCH_SIZE", 100),
		SyncMaxPageSize:  getEnvInt("SYNC_MAX_PAGE_SIZE", 500),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
		LogFormat:        getEnv("LOG_FORMAT", "json"),
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

// validate ensures required configuration is present in production.
func (c *Config) validate() error {
	if c.Environment == "production" {
		if c.JWTSecret == "dev-secret-change-in-production" {
			return fmt.Errorf("JWT_SECRET must be set in production")
		}
		if c.DatabaseURL == "" {
			return fmt.Errorf("DATABASE_URL must be set in production")
		}
	}
	return nil
}

// Addr returns the full server address (host:port).
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%s", c.ServerHost, c.ServerPort)
}

// IsProduction returns true if running in production mode.
func (c *Config) IsProduction() bool {
	return strings.ToLower(c.Environment) == "production"
}

// IsDevelopment returns true if running in development mode.
func (c *Config) IsDevelopment() bool {
	return strings.ToLower(c.Environment) == "development"
}

// AllowedWSOrigins returns the list of allowed WebSocket origins.
// In development, all origins are allowed (returns empty slice to allow any).
// In production, reads from ALLOWED_WS_ORIGINS env var (comma-separated).
func (c *Config) AllowedWSOrigins() []string {
	if c.IsDevelopment() {
		return nil // nil means allow all in development
	}
	origins := os.Getenv("ALLOWED_WS_ORIGINS")
	if origins == "" {
		return nil
	}
	return strings.Split(origins, ",")
}

// DatabaseURLWithSSL returns the database URL with SSL mode enforced in production.
// Uses sslmode=require in production, sslmode=prefer in development.
func (c *Config) DatabaseURLWithSSL() string {
	if strings.Contains(c.DatabaseURL, "sslmode=") {
		return c.DatabaseURL
	}
	if c.IsProduction() {
		return c.DatabaseURL + "?sslmode=require"
	}
	return c.DatabaseURL + "?sslmode=prefer"
}

// getEnv reads a string environment variable or returns the default.
func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// getEnvInt reads an integer environment variable or returns the default.
func getEnvInt(key string, defaultVal int) int {
	s := os.Getenv(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

// getEnvBool reads a boolean environment variable or returns the default.
func getEnvBool(key string, defaultVal bool) bool {
	s := os.Getenv(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return defaultVal
	}
	return v
}

// getEnvDuration reads a duration environment variable or returns the default.
func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	s := os.Getenv(key)
	if s == "" {
		return defaultVal
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return defaultVal
	}
	return v
}
