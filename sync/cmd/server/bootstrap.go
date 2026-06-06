// ==============================================================================
// Bootstrap — Application Initialization with Secrets Manager Integration
// ==============================================================================
//
// This file demonstrates how to bootstrap the Decision Stack server with
// JWT authentication backed by AWS Secrets Manager.
//
// Flow:
//   1. Load JWT signing key from Secrets Manager on startup
//   2. Initialize MultiKeyValidator with kid support
//   3. Set up HTTP/gRPC middleware with rotation-aware validation
//   4. Expose rotation status endpoint for monitoring
//
// ==============================================================================

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"decision-stack/sync/internal/auth"
)

// ------------------------------------------------------------------------------
// BootstrapConfig holds the auth-related configuration.
// ------------------------------------------------------------------------------

type BootstrapConfig struct {
	JWTSecretARN          string        `env:"JWT_SIGNING_KEY,required"`
	JWTGracePeriod        time.Duration `env:"JWT_KEY_ROTATION_GRACE_PERIOD" envDefault:"24h"`
	Environment           string        `env:"APP_ENV,required"`
	Port                  string        `env:"APP_PORT" envDefault:"8080"`
	GRPCPort              string        `env:"GRPC_PORT" envDefault:"9090"`
	LogLevel              string        `env:"LOG_LEVEL" envDefault:"info"`
	MetricsEnabled        bool          `env:"METRICS_ENABLED" envDefault:"true"`
}

// ------------------------------------------------------------------------------
// AuthSystem holds all auth-related components.
// ------------------------------------------------------------------------------

type AuthSystem struct {
	Rotator    *auth.KeyRotator
	Validator  *auth.MultiKeyValidator
	Middleware struct {
		HTTPRequired  gin.HandlerFunc
		HTTPOptional  gin.HandlerFunc
		GRPCUnary     grpc.UnaryServerInterceptor
		GRPCStream    grpc.StreamServerInterceptor
	}
}

// ------------------------------------------------------------------------------
// InitializeAuth bootstraps the authentication system.
// ------------------------------------------------------------------------------

// InitializeAuth loads the JWT signing key from Secrets Manager and sets up
// the full authentication system with rotation support.
func InitializeAuth(cfg BootstrapConfig) (*AuthSystem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// --- 1. Initialize AWS SDK ---
	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	secretsClient := secretsmanager.NewFromConfig(awsCfg)

	// --- 2. Load JWT signing key from Secrets Manager ---
	log.Printf("[auth] Loading JWT signing key from Secrets Manager: %s", cfg.JWTSecretARN)

	var rotator *auth.KeyRotator

	// Try to load existing state from Secrets Manager
	rotator, err = auth.LoadFromSecretManager(secretsClient, cfg.JWTSecretARN,
		auth.WithGracePeriod(cfg.JWTGracePeriod),
		auth.WithRotationCallback(onKeyRotated),
	)
	if err != nil {
		// Secret may not exist yet — generate initial key
		log.Printf("[auth] Could not load existing key: %v", err)
		log.Printf("[auth] Generating initial JWT signing key...")

		initialKey, kid, err := auth.GenerateSigningKeyHex()
		if err != nil {
			return nil, fmt.Errorf("generate initial signing key: %w", err)
		}

		log.Printf("[auth] Generated initial key with kid: %s", kid)

		rotator = auth.NewKeyRotator(
			[]byte(initialKey),
			secretsClient,
			cfg.JWTSecretARN,
			auth.WithGracePeriod(cfg.JWTGracePeriod),
			auth.WithRotationCallback(onKeyRotated),
		)

		// Persist initial state to Secrets Manager
		// (In production, the initial secret should be created by Terraform)
	}

	validator := rotator.Validator()
	currentKID := validator.CurrentKID()
	keyCount := validator.KeyCount()

	log.Printf("[auth] JWT validator initialized:")
	log.Printf("  - Current kid: %s", currentKID)
	log.Printf("  - Active keys: %d", keyCount)
	log.Printf("  - Grace period: %v", cfg.JWTGracePeriod)
	log.Printf("  - Grace active: %v", validator.IsGracePeriodActive())
	if validator.IsGracePeriodActive() {
		log.Printf("  - Grace remaining: %v", validator.GracePeriodRemaining())
	}

	// --- 3. Set up middleware ---
	sys := &AuthSystem{
		Rotator:   rotator,
		Validator: validator,
	}

	sys.Middleware.HTTPRequired = auth.GinMiddlewareWithExemptions(validator)
	sys.Middleware.HTTPOptional = auth.GinOptionalMiddleware(validator)
	sys.Middleware.GRPCUnary = auth.GRPCUnaryInterceptor(validator)
	sys.Middleware.GRPCStream = auth.GRPCStreamInterceptor(validator)

	return sys, nil
}

// ------------------------------------------------------------------------------
// Route Registration
// ------------------------------------------------------------------------------

// RegisterAuthRoutes adds auth-related routes to the Gin router.
func RegisterAuthRoutes(r *gin.Engine, sys *AuthSystem) {
	// JWKS endpoint (public — no auth required)
	r.GET("/.well-known/jwks.json", auth.HandleJWKS(sys.Validator))

	// Rotation status (admin only — requires authentication + admin role)
	admin := r.Group("/admin/rotation")
	admin.Use(sys.Middleware.HTTPRequired)
	admin.Use(auth.RequireRole("admin"))
	{
		// GET /admin/rotation/status — current rotation state
		admin.GET("/status", handleRotationStatus(sys))

		// POST /admin/rotation/rotate — initiate key rotation
		admin.POST("/rotate", handleRotate(sys))

		// POST /admin/rotation/complete — end grace period early
		admin.POST("/complete", handleCompleteGracePeriod(sys))

		// POST /admin/rotation/rollback — rollback failed rotation
		admin.POST("/rollback", handleRollback(sys))
	}

	// Health check (no auth required — used by ALB)
	r.GET("/health", handleHealth(sys))
}

// ------------------------------------------------------------------------------
// HTTP Handlers
// ------------------------------------------------------------------------------

func handleRotationStatus(sys *AuthSystem) gin.HandlerFunc {
	return func(c *gin.Context) {
		status := sys.Rotator.Status()
		c.JSON(http.StatusOK, status)
	}
}

func handleRotate(sys *AuthSystem) gin.HandlerFunc {
	return func(c *gin.Context) {
		newKID, err := sys.Rotator.Rotate()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "rotation_failed",
				"message": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":      "rotation_initiated",
			"new_kid":     newKID,
			"grace_period": "24h",
			"message":     "New key is now active. Old key valid for 24h grace period.",
		})
	}
}

func handleCompleteGracePeriod(sys *AuthSystem) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := sys.Rotator.CompleteGracePeriod(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "completion_failed",
				"message": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "grace_period_completed",
			"message": "Old key has been removed. All clients must use new key.",
		})
	}
}

func handleRollback(sys *AuthSystem) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := sys.Rotator.Rollback(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "rollback_failed",
				"message": err.Error(),
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":  "rotation_rolled_back",
			"message": "Previous key restored as current. New key discarded.",
		})
	}
}

func handleHealth(sys *AuthSystem) gin.HandlerFunc {
	return func(c *gin.Context) {
		keyInfo := sys.Validator.GetKeyInfo()

		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"auth": gin.H{
				"status":        "ready",
				"current_kid":   keyInfo["current_kid"],
				"key_count":     keyInfo["key_count"],
				"grace_period":  keyInfo["grace_period_active"],
			},
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// ------------------------------------------------------------------------------
// Rotation Callback (emits CloudWatch metrics)
// ------------------------------------------------------------------------------

func onKeyRotated(oldKID, newKID string) {
	log.Printf("[auth.rotation] Key rotated: old=%s new=%s", oldKID, newKID)

	// Emit structured log for CloudWatch metric filter
	event := map[string]interface{}{
		"event":       "jwt_key_rotation",
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"old_kid":     oldKID,
		"new_kid":     newKID,
	}

	if newKID == "" {
		event["action"] = "key_removed"        // Grace period ended
		event["kid"] = oldKID
	} else if oldKID == "" {
		event["action"] = "key_activated"      // New key is now current
		event["kid"] = newKID
	} else {
		event["action"] = "grace_period_start" // Rotation initiated
		event["kid"] = newKID
	}

	jsonBytes, _ := json.Marshal(event)
	log.Println(string(jsonBytes))

	// In production, also emit to CloudWatch Metrics directly
	// using the AWS SDK: cloudwatch.PutMetricData
}

// ------------------------------------------------------------------------------
// Example: Main entry point
// ------------------------------------------------------------------------------

// This is an example main() showing how all the pieces fit together.
// In production, this would be in cmd/server/main.go

func mainExample() {
	cfg := BootstrapConfig{
		JWTSecretARN: os.Getenv("JWT_SIGNING_KEY"),         // ECS injects ARN here
		Environment:  os.Getenv("APP_ENV"),
		Port:         getEnv("APP_PORT", "8080"),
		GRPCPort:     getEnv("GRPC_PORT", "9090"),
		LogLevel:     getEnv("LOG_LEVEL", "info"),
	}

	// Parse grace period
	gp, err := time.ParseDuration(getEnv("JWT_KEY_ROTATION_GRACE_PERIOD", "24h"))
	if err != nil {
		log.Fatalf("Invalid grace period: %v", err)
	}
	cfg.JWTGracePeriod = gp

	// Validate required config
	if cfg.JWTSecretARN == "" {
		log.Fatal("JWT_SIGNING_KEY environment variable is required (should contain Secrets Manager ARN)")
	}

	// Initialize auth system
	authSys, err := InitializeAuth(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize auth: %v", err)
	}

	// Set up Gin router
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(authSys.Middleware.HTTPRequired)  // Apply auth middleware globally

	// Register auth routes (JWKS, rotation admin, health)
	RegisterAuthRoutes(r, authSys)

	// Your application routes here...
	// r.GET("/api/v1/...", handler)

	// Start server
	addr := ":" + cfg.Port
	log.Printf("Server starting on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
