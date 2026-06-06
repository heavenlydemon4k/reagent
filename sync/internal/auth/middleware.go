// ==============================================================================
// Package auth — HTTP/gRPC Middleware for JWT Authentication
// ==============================================================================
//
// Provides Gin middleware for HTTP and interceptors for gRPC that validate
// JWT tokens using the MultiKeyValidator with kid support.
//
// On JWT key rotation:
//   - During grace period (24h), both old and new tokens are accepted
//   - After grace period, only tokens with the current kid are accepted
//   - The middleware automatically handles the MultiKeyValidator state
//
// ==============================================================================

package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ------------------------------------------------------------------------------
// Context Keys
// ------------------------------------------------------------------------------

type contextKey int

const (
	// ContextKeyClaims stores validated Claims in context.
	ContextKeyClaims contextKey = iota
	// ContextKeyUserID stores the user ID in context.
	ContextKeyUserID
)

// ------------------------------------------------------------------------------
// Gin HTTP Middleware
// ------------------------------------------------------------------------------

// GinMiddleware creates a Gin middleware that validates JWT tokens.
// It uses the MultiKeyValidator to support graceful key rotation.
func GinMiddleware(validator *MultiKeyValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "missing_authorization_header",
				"message": "Authorization header is required",
			})
			return
		}

		// Expect "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "invalid_authorization_format",
				"message": "Authorization header must be 'Bearer <token>'",
			})
			return
		}

		tokenStr := parts[1]

		// Validate token using multi-key validator (supports rotation grace period)
		claims, err := validator.Validate(tokenStr)
		if err != nil {
			// Check if this is an expired key (past grace period)
			if kid, parseErr := ParseKID(tokenStr); parseErr == nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error":    "token_invalid",
					"message":  fmt.Sprintf("Token validation failed: %v", err),
					"kid":      kid,
					"current_kid": validator.CurrentKID(),
				})
			} else {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error":   "token_invalid",
					"message": fmt.Sprintf("Token validation failed: %v", err),
				})
			}
			return
		}

		// Check token expiry (defense in depth — validator also checks)
		if claims.ExpiresAt != nil && claims.ExpiresAt.Before(time.Now()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "token_expired",
				"message": "Token has expired, please re-authenticate",
			})
			return
		}

		// Store claims in context for downstream handlers
		c.Set("claims", claims)
		c.Set("userID", claims.UserID)
		c.Set("deviceID", claims.DeviceID)

		c.Next()
	}
}

// GinOptionalMiddleware creates middleware that validates JWT if present
// but allows anonymous access if no token is provided.
func GinOptionalMiddleware(validator *MultiKeyValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			c.Next()
			return
		}

		claims, err := validator.Validate(parts[1])
		if err == nil {
			c.Set("claims", claims)
			c.Set("userID", claims.UserID)
		}

		c.Next()
	}
}

// RequireRole creates middleware that requires a specific role.
func RequireRole(role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		claimsVal, exists := c.Get("claims")
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "authentication_required",
				"message": "This endpoint requires authentication",
			})
			return
		}

		claims := claimsVal.(*Claims)
		if claims.Role != role && claims.Role != "admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":   "insufficient_permissions",
				"message": fmt.Sprintf("Required role: %s", role),
				"current_role": claims.Role,
			})
			return
		}

		c.Next()
	}
}

// ------------------------------------------------------------------------------
// gRPC Interceptors
// ------------------------------------------------------------------------------

// GRPCUnaryInterceptor creates a gRPC unary interceptor for JWT validation.
func GRPCUnaryInterceptor(validator *MultiKeyValidator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract token from gRPC metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		parts := strings.SplitN(authHeaders[0], " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return nil, status.Error(codes.Unauthenticated, "invalid authorization format")
		}

		claims, err := validator.Validate(parts[1])
		if err != nil {
			return nil, status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
		}

		// Add claims to context
		ctx = context.WithValue(ctx, ContextKeyClaims, claims)
		ctx = context.WithValue(ctx, ContextKeyUserID, claims.UserID)

		return handler(ctx, req)
	}
}

// GRPCStreamInterceptor creates a gRPC stream interceptor for JWT validation.
func GRPCStreamInterceptor(validator *MultiKeyValidator) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 {
			return status.Error(codes.Unauthenticated, "missing authorization header")
		}

		parts := strings.SplitN(authHeaders[0], " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			return status.Error(codes.Unauthenticated, "invalid authorization format")
		}

		claims, err := validator.Validate(parts[1])
		if err != nil {
			return status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
		}

		// Wrap stream with claims in context
		wrapped := &wrappedStream{
			ServerStream: ss,
			ctx: context.WithValue(
				context.WithValue(ctx, ContextKeyClaims, claims),
				ContextKeyUserID, claims.UserID,
			),
		}

		return handler(srv, wrapped)
	}
}

// wrappedStream wraps grpc.ServerStream to inject context.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}

// ------------------------------------------------------------------------------
// Context Helpers
// ------------------------------------------------------------------------------

// UserIDFromContext extracts the user ID from context.
func UserIDFromContext(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(ContextKeyUserID).(string)
	return userID, ok
}

// ClaimsFromContext extracts the full claims from context.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value(ContextKeyClaims).(*Claims)
	return claims, ok
}

// MustUserID extracts user ID or returns empty string.
func MustUserID(ctx context.Context) string {
	userID, _ := UserIDFromContext(ctx)
	return userID
}

// ------------------------------------------------------------------------------
// Health Check Middleware (no auth required)
// ------------------------------------------------------------------------------

// HealthCheckPaths contains paths that bypass authentication.
var HealthCheckPaths = map[string]bool{
	"/health":         true,
	"/health/live":    true,
	"/health/ready":   true,
	"/metrics":        true,
	"/.well-known/jwks.json": true,
}

// GinMiddlewareWithExemptions creates middleware that skips auth for health checks.
func GinMiddlewareWithExemptions(validator *MultiKeyValidator) gin.HandlerFunc {
	baseMiddleware := GinMiddleware(validator)

	return func(c *gin.Context) {
		if HealthCheckPaths[c.Request.URL.Path] {
			c.Next()
			return
		}
		baseMiddleware(c)
	}
}

// ------------------------------------------------------------------------------
// JWKS Endpoint (for external clients to discover current key)
// ------------------------------------------------------------------------------

// JWK represents a JSON Web Key for the JWKS endpoint.
type JWK struct {
	KID       string `json:"kid"`
	Kty       string `json:"kty"`
	Alg       string `json:"alg"`
	Use       string `json:"use"`
}

// JWKSResponse is the response from the JWKS endpoint.
type JWKSResponse struct {
	Keys []JWK `json:"keys"`
}

// HandleJWKS serves the JWKS endpoint for external token verification.
// Only exposes the current key's kid — previous keys are intentionally hidden
// to prevent external clients from accepting tokens past the grace period.
func HandleJWKS(validator *MultiKeyValidator) gin.HandlerFunc {
	return func(c *gin.Context) {
		currentKID := validator.CurrentKID()
		c.JSON(http.StatusOK, JWKSResponse{
			Keys: []JWK{
				{
					KID: currentKID,
					Kty: "oct",
					Alg: "HS256",
					Use: "sig",
				},
			},
		})
	}
}
