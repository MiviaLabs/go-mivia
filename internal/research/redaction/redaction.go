package redaction

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
var phonePattern = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)
var privateKeyPattern = regexp.MustCompile(`(?is)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
var tokenAssignmentPattern = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password)\s*[:=]\s*[^,\s]+`)

var sensitiveQueryKeys = map[string]struct{}{
	"api_key":      {},
	"apikey":       {},
	"access_token": {},
	"auth":         {},
	"key":          {},
	"password":     {},
	"secret":       {},
	"signature":    {},
	"token":        {},
}

func Redact(value string) string {
	value = privateKeyPattern.ReplaceAllString(value, "[REDACTED_PRIVATE_KEY]")
	value = tokenAssignmentPattern.ReplaceAllString(value, "$1=[REDACTED_SECRET]")
	value = emailPattern.ReplaceAllString(value, "[REDACTED_EMAIL]")
	value = phonePattern.ReplaceAllString(value, "[REDACTED_PHONE]")
	return value
}

func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return Redact(raw)
	}
	query := parsed.Query()
	for key := range query {
		if _, ok := sensitiveQueryKeys[strings.ToLower(key)]; ok {
			query.Set(key, "[REDACTED]")
		}
	}
	parsed.RawQuery = query.Encode()
	return Redact(parsed.String())
}

func HashContent(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func ContainsSensitive(value string) bool {
	return Redact(value) != value || RedactURL(value) != value
}
