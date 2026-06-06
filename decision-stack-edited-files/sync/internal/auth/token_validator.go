package auth

import "github.com/golang-jwt/jwt/v5"

// TokenValidator provides standalone JWT validation for WebSocket upgrades.
type TokenValidator struct {
	secret []byte
}

// NewTokenValidator creates a TokenValidator with the given HS256 secret.
func NewTokenValidator(secret string) *TokenValidator {
	return &TokenValidator{secret: []byte(secret)}
}

// Validate parses and validates a JWT string, returning the embedded claims.
func (v *TokenValidator) Validate(tokenStr string) (*SyncClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &SyncClaims{}, func(token *jwt.Token) (interface{}, error) {
		return v.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*SyncClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, jwt.ErrSignatureInvalid
}
