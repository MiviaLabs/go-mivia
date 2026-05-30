package redaction_test

import (
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/research/redaction"
)

func TestRedact_RemovesSecretsAndPII(t *testing.T) {
	input := "Contact user@example.com with token=abc123 and +1 555 123 4567"
	got := redaction.Redact(input)

	for _, forbidden := range []string{"user@example.com", "abc123", "+1 555 123 4567"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redacted output leaked %q: %s", forbidden, got)
		}
	}
}

func TestRedact_DoesNotTreatDatesIPsOrCodeDeclarationsAsPII(t *testing.T) {
	input := `protocol = "2025-06-18"
endpoint = "127.0.0.1:8080"
secret := make([]byte, 32)
token-guarded exact edit`

	got := redaction.Redact(input)
	if got != input {
		t.Fatalf("expected non-sensitive operational text to remain unchanged: %q", got)
	}
}

func TestRedactURL_RemovesSensitiveQueryValues(t *testing.T) {
	got := redaction.RedactURL("https://example.test/path?token=abc123&q=public")
	if strings.Contains(got, "abc123") {
		t.Fatalf("redacted URL leaked token: %s", got)
	}
	if !strings.Contains(got, "q=public") {
		t.Fatalf("redacted URL dropped non-sensitive query: %s", got)
	}
}

func TestHashContent_IsStable(t *testing.T) {
	first := redaction.HashContent("fixture metadata")
	second := redaction.HashContent("fixture metadata")
	if first != second || !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("unexpected hash values: %s %s", first, second)
	}
}
