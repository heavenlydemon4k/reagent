// Package contact provides contact deduplication for the Ingestion Mesh.
// normalize.go implements email and name normalization utilities.
package contact

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode"
)

// googleWorkspaceDomains is a set of known Google Workspace domains.
// In production this could be loaded from configuration or a database table.
var knownGoogleWorkspaceDomains = map[string]struct{}{
	"gmail.com": {},
}

// emailPlusRe matches the +tag portion in an email local part.
var emailPlusRe = regexp.MustCompile(`\+[^@]+`)

// multipleWhitespaceRe matches consecutive whitespace characters.
var multipleWhitespaceRe = regexp.MustCompile(`\s+`)

// NormalizeEmail canonicalizes an email address for deduplication:
//   - lowercases the entire address
//   - strips +aliases (user+tag@gmail.com -> user@gmail.com)
//   - validates the address parses as RFC 5322
//   - trims whitespace
func NormalizeEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}

	// Parse to validate and extract the address portion (ignores display name)
	addr, err := mail.ParseAddress(email)
	if err == nil && addr.Address != "" {
		email = addr.Address
	}

	// Strip +alias from local part
	email = emailPlusRe.ReplaceAllString(email, "")

	return email
}

// NormalizeName canonicalizes a display name:
//   - trims leading/trailing whitespace
//   - collapses consecutive whitespace to a single space
//   - removes control characters
func NormalizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}

	// Remove control characters except normal whitespace
	var sb strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			continue
		}
		sb.WriteRune(r)
	}
	name = sb.String()

	// Collapse consecutive whitespace
	name = multipleWhitespaceRe.ReplaceAllString(name, " ")
	name = strings.TrimSpace(name)

	return name
}

// ExtractDomain returns the domain portion of an email address (after @).
// Returns empty string if no valid domain found.
func ExtractDomain(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndex(email, "@")
	if at == -1 || at == len(email)-1 {
		return ""
	}
	return email[at+1:]
}

// IsGoogleWorkspaceAlias checks whether the given domain is known to use
// Google Workspace. This helps distinguish true aliases from independent accounts.
// Gmail itself is the primary consumer of +aliases, but custom domains using
// Google Workspace have the same behavior.
func IsGoogleWorkspaceAlias(email string, domain string) bool {
	// Check built-in known domains
	if _, ok := knownGoogleWorkspaceDomains[domain]; ok {
		return true
	}

	// For custom domains, we'd do an MX record lookup in production.
	// Here we provide the hook; the implementation would check if the
	// domain's MX records point to Google.
	// TODO: implement MX lookup for custom domains in production
	return false
}

// GenerateNameVariants produces a set of name variants for fuzzy matching:
//   - full name
//   - first name only (if space-separated)
//   - last name only (if space-separated)
//   - initials (e.g., "John Doe" -> "JD")
func GenerateNameVariants(name string) []string {
	name = NormalizeName(name)
	if name == "" {
		return nil
	}

	seen := map[string]struct{}{name: {}}
	variants := []string{name}

	parts := strings.Fields(name)
	if len(parts) >= 2 {
		// First name
		first := parts[0]
		if _, ok := seen[first]; !ok {
			seen[first] = struct{}{}
			variants = append(variants, first)
		}

		// Last name
		last := parts[len(parts)-1]
		if _, ok := seen[last]; !ok {
			seen[last] = struct{}{}
			variants = append(variants, last)
		}

		// Initials
		var initials strings.Builder
		for _, p := range parts {
			if len(p) > 0 {
				initials.WriteRune(unicode.ToUpper(rune(p[0])))
			}
		}
		ini := initials.String()
		if _, ok := seen[ini]; !ok && len(ini) >= 2 {
			seen[ini] = struct{}{}
			variants = append(variants, ini)
		}
	}

	return variants
}
