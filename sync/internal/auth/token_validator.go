package auth

import "github.com/golang-jwt/jwt/v5"

// TokenValidator provides standalone JWT validation for WebSocket upgrades.
type TokenValidator struct {
	secret []byte
}

// Claims are the JWT claims expected on WebSocket connection tokens.
type Claims struct {
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
	jwt.RegisteredClaims
}

// NewTokenValidator creates a TokenValidator with the given HS256 secret.
func NewTokenValidator(secret string) *TokenValidator {
	return &TokenValidator{secret: []byte(secret)}
}

// Validate parses and validates a JWT string, returning the embedded claims.
func (v *TokenValidator) Validate(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		return v.secret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}
	return nil, jwt.ErrSignatureInvalid
}
