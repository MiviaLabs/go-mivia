package projectintegrations

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/config"
)

func TestCredentialResolver_ResolveAtlassianFromEnv(t *testing.T) {
	resolver := CredentialResolver{
		LookupEnv: func(name string) (string, bool) {
			values := map[string]string{
				"ATLASSIAN_EMAIL_REF": "  agent@example.invalid  ",
				"ATLASSIAN_TOKEN_REF": "\nsynthetic-token-value\n",
			}
			value, ok := values[name]
			return value, ok
		},
	}

	credentials, err := resolver.ResolveAtlassian(config.AtlassianCredentialRefs{
		EmailEnv:    "ATLASSIAN_EMAIL_REF",
		APITokenEnv: "ATLASSIAN_TOKEN_REF",
	})
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if credentials.Email != "agent@example.invalid" || credentials.APIToken != "synthetic-token-value" {
		t.Fatalf("expected trimmed env credentials, got %#v", credentials)
	}
}

func TestCredentialResolver_ResolveAtlassianFromFiles(t *testing.T) {
	dir := t.TempDir()
	emailPath := filepath.Join(dir, "email")
	tokenPath := filepath.Join(dir, "token")
	writeSecretFixture(t, emailPath, "\nagent@example.invalid\n")
	writeSecretFixture(t, tokenPath, "  synthetic-token-value  ")

	credentials, err := NewCredentialResolver().ResolveAtlassian(config.AtlassianCredentialRefs{
		EmailFile:    emailPath,
		APITokenFile: tokenPath,
	})
	if err != nil {
		t.Fatalf("resolve credentials: %v", err)
	}
	if credentials.Email != "agent@example.invalid" || credentials.APIToken != "synthetic-token-value" {
		t.Fatalf("expected trimmed file credentials, got %#v", credentials)
	}
}

func TestCredentialResolver_ResolveAtlassianFromCredentialsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "atlassian-credentials.json")
	writeSecretFixture(t, path, `{
		"email": "  agent@example.invalid  ",
		"api_token": "\nsynthetic-token-value\n"
	}`)

	credentials, err := NewCredentialResolver().ResolveAtlassian(config.AtlassianCredentialRefs{
		CredentialsFile: path,
	})
	if err != nil {
		t.Fatalf("resolve credentials file: %v", err)
	}
	if credentials.Email != "agent@example.invalid" || credentials.APIToken != "synthetic-token-value" {
		t.Fatalf("expected trimmed credentials file values, got %#v", credentials)
	}
}

func TestCredentialResolver_RejectsMissingRefs(t *testing.T) {
	_, err := NewCredentialResolver().ResolveAtlassian(config.AtlassianCredentialRefs{})
	if !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("expected credential unavailable error, got %v", err)
	}
	assertErrorOmits(t, err, "agent@example.invalid", "synthetic-token-value")
}

func TestCredentialResolver_RejectsUnsetEnv(t *testing.T) {
	_, err := CredentialResolver{
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
	}.ResolveAtlassian(config.AtlassianCredentialRefs{
		EmailEnv:    "ATLASSIAN_EMAIL_SECRET_REF",
		APITokenEnv: "ATLASSIAN_TOKEN_SECRET_REF",
	})
	if !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("expected credential unavailable error, got %v", err)
	}
	assertErrorOmits(t, err, "ATLASSIAN_EMAIL_SECRET_REF", "ATLASSIAN_TOKEN_SECRET_REF")
}

func TestCredentialResolver_RejectsEmptyValues(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		_, err := CredentialResolver{
			LookupEnv: func(name string) (string, bool) {
				return " \n\t ", true
			},
		}.ResolveAtlassian(config.AtlassianCredentialRefs{
			EmailEnv:    "ATLASSIAN_EMAIL_REF",
			APITokenEnv: "ATLASSIAN_TOKEN_REF",
		})
		if !errors.Is(err, ErrCredentialUnavailable) {
			t.Fatalf("expected credential unavailable error, got %v", err)
		}
	})

	t.Run("file", func(t *testing.T) {
		dir := t.TempDir()
		emailPath := filepath.Join(dir, "email")
		tokenPath := filepath.Join(dir, "token")
		writeSecretFixture(t, emailPath, " \n\t ")
		writeSecretFixture(t, tokenPath, "synthetic-token-value")
		_, err := NewCredentialResolver().ResolveAtlassian(config.AtlassianCredentialRefs{
			EmailFile:    emailPath,
			APITokenFile: tokenPath,
		})
		if !errors.Is(err, ErrCredentialUnavailable) {
			t.Fatalf("expected credential unavailable error, got %v", err)
		}
		assertErrorOmits(t, err, emailPath, tokenPath, "synthetic-token-value")
	})

	t.Run("credentials file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "atlassian-credentials.json")
		writeSecretFixture(t, path, `{"email":"agent@example.invalid","api_token":"  "}`)
		_, err := NewCredentialResolver().ResolveAtlassian(config.AtlassianCredentialRefs{
			CredentialsFile: path,
		})
		if !errors.Is(err, ErrCredentialUnavailable) {
			t.Fatalf("expected credential unavailable error, got %v", err)
		}
		assertErrorOmits(t, err, path, "agent@example.invalid")
	})
}

func TestCredentialResolver_RejectsAmbiguousRefs(t *testing.T) {
	tests := []struct {
		name      string
		refs      config.AtlassianCredentialRefs
		forbidden []string
	}{
		{
			name: "split refs",
			refs: config.AtlassianCredentialRefs{
				EmailEnv:     "ATLASSIAN_EMAIL_REF",
				EmailFile:    "secrets/email",
				APITokenFile: "secrets/token",
			},
			forbidden: []string{"ATLASSIAN_EMAIL_REF", "secrets/email", "secrets/token"},
		},
		{
			name: "credentials file with split refs",
			refs: config.AtlassianCredentialRefs{
				CredentialsFile: "secrets/atlassian-credentials.json",
				APITokenFile:    "secrets/token",
			},
			forbidden: []string{"secrets/atlassian-credentials.json", "secrets/token"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCredentialResolver().ResolveAtlassian(tt.refs)
			if !errors.Is(err, ErrCredentialUnavailable) {
				t.Fatalf("expected credential unavailable error, got %v", err)
			}
			assertErrorOmits(t, err, tt.forbidden...)
		})
	}
}

func TestCredentialResolver_ReadFileErrorIsSafe(t *testing.T) {
	_, err := NewCredentialResolver().ResolveAtlassian(config.AtlassianCredentialRefs{
		EmailFile:    filepath.Join(t.TempDir(), "missing-email"),
		APITokenFile: "synthetic-token-value",
	})
	if !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("expected credential unavailable error, got %v", err)
	}
	assertErrorOmits(t, err, "missing-email", "synthetic-token-value")
}

func TestCredentialResolver_CredentialsFileDecodeErrorIsSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "atlassian-credentials.json")
	writeSecretFixture(t, path, `{"email":"agent@example.invalid","api_token":"synthetic-token-value"`)

	_, err := NewCredentialResolver().ResolveAtlassian(config.AtlassianCredentialRefs{
		CredentialsFile: path,
	})
	if !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("expected credential unavailable error, got %v", err)
	}
	assertErrorOmits(t, err, path, "agent@example.invalid", "synthetic-token-value")
}

func writeSecretFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret fixture: %v", err)
	}
}

func assertErrorOmits(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	for _, value := range forbidden {
		if value != "" && strings.Contains(message, value) {
			t.Fatalf("error leaked %q: %s", value, message)
		}
	}
}
