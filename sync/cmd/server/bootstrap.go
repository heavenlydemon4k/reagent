// bootstrap.go — AWS Secrets Manager JWT key rotation documentation.
//
// This file is intentionally NOT compiled (build tag: ignore) because it
// demonstrates a future integration pattern (gin + gRPC) that requires
// additional dependencies not yet in go.mod. The pattern is documented here
// for reference when wiring Phase 9 CI/CD hardening.
//
// To promote this to real code:
//  1. Add github.com/gin-gonic/gin and google.golang.org/grpc to go.mod.
//  2. Remove the //go:build ignore tag below.
//  3. Replace mainExample() with the real startup sequence in main.go.

//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	"decision-stack/sync/internal/auth"
)

// BootstrapConfig holds auth-related configuration for Secrets Manager
// key rotation bootstrap.
type BootstrapConfig struct {
	JWTSecretARN  string        `env:"JWT_SIGNING_KEY,required"`
	JWTGracePeriod time.Duration `env:"JWT_KEY_ROTATION_GRACE_PERIOD" envDefault:"24h"`
	Environment   string        `env:"APP_ENV,required"`
	Port          string        `env:"APP_PORT" envDefault:"8082"`
	GRPCPort      string        `env:"GRPC_PORT" envDefault:"9090"`
	LogLevel      string        `env:"LOG_LEVEL" envDefault:"info"`
}

// AuthSystem holds all auth-related components.
type AuthSystem struct {
	Rotator   *auth.KeyRotator
	Validator *auth.MultiKeyValidator
	Middleware struct {
		HTTPRequired grpc.UnaryServerInterceptor // placeholder type
		GRPCUnary    grpc.UnaryServerInterceptor
		GRPCStream   grpc.StreamServerInterceptor
	}
}

// InitializeAuth loads the JWT signing key from AWS Secrets Manager and sets
// up the authentication system with key-rotation support.
func InitializeAuth(cfg BootstrapConfig) (*AuthSystem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	secretsClient := secretsmanager.NewFromConfig(awsCfg)
	log.Printf("[auth] Loading JWT signing key from Secrets Manager: %s", cfg.JWTSecretARN)

	rotator, err := auth.LoadFromSecretManager(secretsClient, cfg.JWTSecretARN,
		auth.WithGracePeriod(cfg.JWTGracePeriod),
		auth.WithRotationCallback(onKeyRotated),
	)
	if err != nil {
		log.Printf("[auth] Could not load existing key: %v — generating initial key", err)

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
	}

	validator := rotator.Validator()
	log.Printf("[auth] JWT validator initialised: kid=%s keys=%d grace=%v",
		validator.CurrentKID(), validator.KeyCount(), cfg.JWTGracePeriod)

	sys := &AuthSystem{Rotator: rotator, Validator: validator}
	sys.Middleware.GRPCUnary = auth.GRPCUnaryInterceptor(validator)
	sys.Middleware.GRPCStream = auth.GRPCStreamInterceptor(validator)

	return sys, nil
}

// RegisterAuthRoutes mounts JWKS + rotation admin endpoints on a Gin router.
func RegisterAuthRoutes(r *gin.Engine, sys *AuthSystem) {
	r.GET("/.well-known/jwks.json", auth.HandleJWKS(sys.Validator))

	admin := r.Group("/admin/rotation")
	admin.Use(auth.GinMiddlewareWithExemptions(sys.Validator))
	admin.Use(auth.RequireRole("admin"))
	{
		admin.GET("/status", func(c *gin.Context) { c.JSON(http.StatusOK, sys.Rotator.Status()) })
		admin.POST("/rotate", func(c *gin.Context) {
			kid, err := sys.Rotator.Rotate()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"new_kid": kid})
		})
		admin.POST("/complete", func(c *gin.Context) {
			if err := sys.Rotator.CompleteGracePeriod(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "grace_period_completed"})
		})
		admin.POST("/rollback", func(c *gin.Context) {
			if err := sys.Rotator.Rollback(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "rotation_rolled_back"})
		})
	}
}

func onKeyRotated(oldKID, newKID string) {
	event := map[string]interface{}{
		"event":     "jwt_key_rotation",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"old_kid":   oldKID,
		"new_kid":   newKID,
	}
	b, _ := json.Marshal(event)
	log.Println(string(b))
}

func mainExample() {
	cfg := BootstrapConfig{
		JWTSecretARN: os.Getenv("JWT_SIGNING_KEY"),
		Environment:  os.Getenv("APP_ENV"),
		Port:         getEnvBS("APP_PORT", "8082"),
	}

	gp, err := time.ParseDuration(getEnvBS("JWT_KEY_ROTATION_GRACE_PERIOD", "24h"))
	if err != nil {
		log.Fatalf("Invalid grace period: %v", err)
	}
	cfg.JWTGracePeriod = gp

	if cfg.JWTSecretARN == "" {
		log.Fatal("JWT_SIGNING_KEY is required")
	}

	authSys, err := InitializeAuth(cfg)
	if err != nil {
		log.Fatalf("Failed to initialise auth: %v", err)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	RegisterAuthRoutes(r, authSys)

	log.Printf("Bootstrap server on :%s", cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// getEnvBS is a local helper (avoids conflict with the real getEnv in main.go).
func getEnvBS(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
