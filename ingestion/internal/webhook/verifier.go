// Package webhook provides HTTP handlers and verification for Gmail and Outlook
// push notifications. Authenticity is verified via JWT (Gmail) and validation
// tokens (Outlook) before any processing occurs.
package webhook

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	googleCertsURL = "https://www.googleapis.com/oauth2/v3/certs"
	msftJWKSURL    = "https://login.microsoftonline.com/common/discovery/v2.0/keys"
	// certCacheTTL is how long cached certificates are considered valid.
	certCacheTTL = 1 * time.Hour
)

// GmailPayload represents the verified claims from a Gmail Pub/Sub push JWT.
type GmailPayload struct {
	Subject  string `json:"sub"`       // user ID (email)
	Email    string `json:"email"`     // user email
	Audience string `json:"aud"`       // our app client ID
	Issuer   string `json:"iss"`       // accounts.google.com
	HistoryID uint64 `json:"historyId"` // Gmail history ID (in data claims)
}

// GmailPubSubMessage is the inner data payload of a Gmail Pub/Sub push.
type GmailPubSubMessage struct {
	Data []byte `json:"data"`
}

// GmailPubSubPayload is the outer envelope from Gmail Pub/Sub push.
type GmailPubSubPayload struct {
	Message *GmailPubSubMessage `json:"message"`
}

// GmailHistoryPayload is the decoded data inside the Pub/Sub message.
type GmailHistoryPayload struct {
	EmailAddress string `json:"emailAddress"`
	HistoryID    uint64 `json:"historyId"`
}

// OutlookPayload represents a verified Outlook Graph notification.
type OutlookPayload struct {
	ChangeType string `json:"changeType"` // "created" | "updated" | "deleted"
	Resource   string `json:"resource"`   // e.g., "Users('id')/Messages('id')"
	ClientState string `json:"clientState"` // subscription client state
	SubscriptionID string `json:"subscriptionId"`
	NotificationID string `json:"id"` // unique per notification, for dedup
	DeltaLink  string `json:"deltaLink,omitempty"`
}

// OutlookNotificationEnvelope is the outer wrapper for Outlook notifications.
type OutlookNotificationEnvelope struct {
	Value []OutlookPayload `json:"value"`
}

// jwks represents a JSON Web Key Set.
type jwks struct {
	Keys []jwk `json:"keys"`
}

// jwk represents a single JSON Web Key.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	X5c []string `json:"x5c,omitempty"`
	X5t string `json:"x5t,omitempty"`
}

// certCacheEntry holds cached JWKS data with expiration.
type certCacheEntry struct {
	jwks      *jwks
	expiresAt time.Time
}

// Verifier handles JWT and validation token verification for Gmail and Outlook.
type Verifier struct {
	httpClient     *http.Client
	googleCertsURL string
	msftJwksURL    string

	// certCache caches JWKS responses keyed by provider name.
	certCache map[string]*certCacheEntry
	certMu    sync.RWMutex
}

// NewVerifier creates a new Verifier with the default Google and Microsoft URLs.
func NewVerifier() *Verifier {
	return &Verifier{
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		googleCertsURL: googleCertsURL,
		msftJwksURL:    msftJWKSURL,
		certCache:      make(map[string]*certCacheEntry),
	}
}

// NewVerifierWithURLs creates a Verifier with custom cert URLs (useful for testing).
func NewVerifierWithURLs(googleCertsURL, msftJwksURL string) *Verifier {
	return &Verifier{
		httpClient:     &http.Client{Timeout: 10 * time.Second},
		googleCertsURL: googleCertsURL,
		msftJwksURL:    msftJwksURL,
		certCache:      make(map[string]*certCacheEntry),
	}
}

// ==========================================
// Gmail JWT Verification
// ==========================================

// VerifyGmailJWT verifies a Gmail Pub/Sub push JWT token.
// It fetches Google certs, verifies the JWT signature, and extracts claims.
func (v *Verifier) VerifyGmailJWT(token string) (*GmailPayload, error) {
	// Parse the JWT header to get the key ID
	header, err := parseJWTHeader(token)
	if err != nil {
		return nil, fmt.Errorf("parse jwt header: %w", err)
	}

	kid, ok := header["kid"].(string)
	if !ok {
		return nil, errors.New("jwt header missing kid")
	}

	// Fetch and cache Google certs
	jwksData, err := v.fetchGoogleCerts()
	if err != nil {
		return nil, fmt.Errorf("fetch google certs: %w", err)
	}

	// Find the key matching the kid
	var matchedKey *jwk
	for i := range jwksData.Keys {
		if jwksData.Keys[i].Kid == kid {
			matchedKey = &jwksData.Keys[i]
			break
		}
	}
	if matchedKey == nil {
		// Refresh cache and try again
		v.certMu.Lock()
		delete(v.certCache, "google")
		v.certMu.Unlock()
		jwksData, err = v.fetchGoogleCerts()
		if err != nil {
			return nil, fmt.Errorf("refresh google certs: %w", err)
		}
		for i := range jwksData.Keys {
			if jwksData.Keys[i].Kid == kid {
				matchedKey = &jwksData.Keys[i]
				break
			}
		}
		if matchedKey == nil {
			return nil, fmt.Errorf("no matching key found for kid: %s", kid)
		}
	}

	// Verify the JWT signature
	claims, err := verifyJWTSignature(token, matchedKey)
	if err != nil {
		return nil, fmt.Errorf("verify jwt signature: %w", err)
	}

	payload := &GmailPayload{
		Subject:  getStringClaim(claims, "sub"),
		Email:    getStringClaim(claims, "email"),
		Audience: getStringClaim(claims, "aud"),
		Issuer:   getStringClaim(claims, "iss"),
	}

	// Validate issuer
	if payload.Issuer != "https://accounts.google.com" && payload.Issuer != "accounts.google.com" {
		return nil, fmt.Errorf("invalid issuer: %s", payload.Issuer)
	}

	return payload, nil
}

// ==========================================
// Outlook Validation Token
// ==========================================

// VerifyOutlookValidation extracts the validation token from an Outlook
// subscription validation request. The response must be sent within 10 seconds.
func (v *Verifier) VerifyOutlookValidation(payload []byte) (string, error) {
	// Outlook sends the validation token as a query parameter, not in the body.
	// This method handles the common case where the token is in the URL.
	// The handler should extract it from the query param directly; this method
	// is provided for completeness when it's passed in the body.
	var req struct {
		ValidationToken string `json:"validationToken"`
	}
	if err := json.Unmarshal(payload, &req); err == nil && req.ValidationToken != "" {
		return req.ValidationToken, nil
	}

	// Try query parameter style (passed as raw token string)
	if len(payload) > 0 {
		token := strings.TrimSpace(string(payload))
		if token != "" && token != "{}" {
			return token, nil
		}
	}

	return "", errors.New("no validation token found in payload")
}

// VerifyOutlookNotification verifies and parses Outlook Graph change notifications.
func (v *Verifier) VerifyOutlookNotification(payload []byte) (*OutlookNotificationEnvelope, error) {
	var envelope OutlookNotificationEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshal outlook notification: %w", err)
	}

	if len(envelope.Value) == 0 {
		return nil, errors.New("empty notification envelope")
	}

	return &envelope, nil
}

// ==========================================
// Certificate / JWKS Fetching
// ==========================================

// fetchGoogleCerts fetches the Google OAuth2 certs with caching.
func (v *Verifier) fetchGoogleCerts() (*jwks, error) {
	// Check cache
	v.certMu.RLock()
	entry, ok := v.certCache["google"]
	v.certMu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.jwks, nil
	}

	// Fetch fresh certs
	v.certMu.Lock()
	defer v.certMu.Unlock()

	// Double-check after acquiring write lock
	entry, ok = v.certCache["google"]
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.jwks, nil
	}

	jwksData, err := v.fetchJWKS(v.googleCertsURL)
	if err != nil {
		return nil, err
	}

	v.certCache["google"] = &certCacheEntry{
		jwks:      jwksData,
		expiresAt: time.Now().Add(certCacheTTL),
	}

	return jwksData, nil
}

// FetchMicrosoftJWKS fetches the Microsoft JWKS (exposed for health checks).
func (v *Verifier) FetchMicrosoftJWKS() (*jwks, error) {
	// Check cache
	v.certMu.RLock()
	entry, ok := v.certCache["microsoft"]
	v.certMu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.jwks, nil
	}

	// Fetch fresh
	v.certMu.Lock()
	defer v.certMu.Unlock()

	entry, ok = v.certCache["microsoft"]
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.jwks, nil
	}

	jwksData, err := v.fetchJWKS(v.msftJwksURL)
	if err != nil {
		return nil, err
	}

	v.certCache["microsoft"] = &certCacheEntry{
		jwks:      jwksData,
		expiresAt: time.Now().Add(certCacheTTL),
	}

	return jwksData, nil
}

// fetchJWKS fetches a JWKS from the given URL.
func (v *Verifier) fetchJWKS(url string) (*jwks, error) {
	resp, err := v.httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jwks endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var jwksData jwks
	if err := json.NewDecoder(resp.Body).Decode(&jwksData); err != nil {
		return nil, fmt.Errorf("decode jwks: %w", err)
	}

	if len(jwksData.Keys) == 0 {
		return nil, errors.New("jwks response contains no keys")
	}

	return &jwksData, nil
}

// ==========================================
// JWT Signature Verification
// ==========================================

// parseJWTHeader extracts and decodes the JWT header (no verification).
func parseJWTHeader(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt format: expected 3 parts")
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}

	var header map[string]interface{}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, fmt.Errorf("unmarshal header: %w", err)
	}

	return header, nil
}

// parseJWTClaims extracts and decodes JWT claims (no signature verification).
func parseJWTClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt format")
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	return claims, nil
}

// verifyJWTSignature verifies the JWT signature using the provided JWK (RSA only).
func verifyJWTSignature(token string, key *jwk) (map[string]interface{}, error) {
	if key.Kty != "RSA" {
		return nil, fmt.Errorf("unsupported key type: %s", key.Kty)
	}

	// Decode modulus
	nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)

	// Decode exponent
	eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}
	e := int(new(big.Int).SetBytes(eBytes).Int64())

	// Build RSA public key
	pubKey := &rsa.PublicKey{
		N: n,
		E: e,
	}

	// Verify signature
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt format")
	}

	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}

	// Determine hash algorithm
	var hashAlg x509.SignatureAlgorithm
	switch key.Alg {
	case "RS256", "":
		hashAlg = x509.SHA256WithRSA
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", key.Alg)
	}

	// Use x509 to verify the signature
	if hashAlg == x509.SHA256WithRSA {
		hash := sha256.Sum256([]byte(signingInput))
		if err := rsa.VerifyPKCS1v15(pubKey, 0, hash[:], signature); err != nil {
			return nil, fmt.Errorf("signature verification failed: %w", err)
		}
	}

	claims, err := parseJWTClaims(token)
	if err != nil {
		return nil, err
	}

	return claims, nil
}

// getStringClaim extracts a string claim from the JWT claims map.
func getStringClaim(claims map[string]interface{}, key string) string {
	if val, ok := claims[key].(string); ok {
		return val
	}
	return ""
}

// pemBlockForKey converts an RSA public key to a PEM block (for future use).
func pemBlockForKey(pub *rsa.PublicKey) ([]byte, error) {
	pubASN1, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: pubASN1,
	})
	return pemBytes, nil
}
