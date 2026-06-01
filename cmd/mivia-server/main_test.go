package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

func TestEffectiveInitialScanOnStartSkipsWhenRestartRecoveryQueued(t *testing.T) {
	if effectiveInitialScanOnStart(true, 3) {
		t.Fatalf("expected restart recovery scans to suppress live initial scans")
	}
}

func TestEffectiveInitialScanOnStartKeepsConfiguredValueWithoutRecovery(t *testing.T) {
	if !effectiveInitialScanOnStart(true, 0) {
		t.Fatalf("expected configured initial scan to remain enabled")
	}
	if effectiveInitialScanOnStart(false, 0) {
		t.Fatalf("expected configured disabled initial scan to remain disabled")
	}
}

func TestProjectPersistentGraphMaxOpenDerivesFromConfiguredProjects(t *testing.T) {
	projects := make([]config.Project, 0, ladybug.DefaultPebbleGraphMaxOpen+4)
	for index := 0; index < ladybug.DefaultPebbleGraphMaxOpen+4; index++ {
		id := fmt.Sprintf("project-%02d", index)
		projects = append(projects, config.Project{
			ID:             id,
			DisplayName:    "Project",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: id,
			GraphStorage:   projectregistry.GraphStoragePersistent,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		})
	}
	registry, err := projectregistry.NewRegistry(projects, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  t.TempDir(),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if got := projectPersistentGraphMaxOpen(registry); got != ladybug.DefaultPebbleGraphMaxOpen {
		t.Fatalf("expected cap %d, got %d", ladybug.DefaultPebbleGraphMaxOpen, got)
	}
}

func TestProjectPersistentGraphMaxOpenUsesConfiguredCountBelowCap(t *testing.T) {
	registry, err := projectregistry.NewRegistry([]config.Project{
		{
			ID:             "first",
			DisplayName:    "First",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "first",
			GraphStorage:   projectregistry.GraphStoragePersistent,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		},
		{
			ID:             "memory",
			DisplayName:    "Memory",
			RootPath:       t.TempDir(),
			Enabled:        true,
			GraphNamespace: "memory",
			GraphStorage:   projectregistry.GraphStorageInMemory,
			DigestMode:     projectregistry.DigestModeContentGraph,
			UpdatePolicy:   projectregistry.UpdatePolicyManual,
		},
	}, projectregistry.Options{
		ContentGraphEnabled:          true,
		ContentGraphApprovalAccepted: true,
		LadybugPath:                  t.TempDir(),
		SQLitePath:                   ":memory:",
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if got := projectPersistentGraphMaxOpen(registry); got != 1 {
		t.Fatalf("expected one persistent content graph project, got %d", got)
	}
}

func TestConfigCheckRedactedJSONValidConfig(t *testing.T) {
	projectRoot := t.TempDir()
	configPath := writeConfigFixture(t, fmt.Sprintf(`
version = 1

[server]
http_addr = "127.0.0.1:9090"

[storage]
ladybug_path = "/tmp/private/mivialabs.lbug"
sqlite_path = "/tmp/private/mivialabs.sqlite"

[logging]
file_enabled = true
file_path = "/tmp/private/mivia.log"

[ingestion]
content_graph_enabled = true
live_updates_enabled = true

[[projects]]
id = "example-service"
display_name = "Example"
root_path = %q
enabled = true
graph_namespace = "example-service"
digest_mode = "content_graph"
update_policy = "live"
workspace_mode = "edit"

[projects.integrations.jira]
enabled = true
site_url = "https://example.atlassian.net"
auth_mode = "api_token_basic"
email_env = "JIRA_EMAIL"
api_token_file = "/home/user/.secrets/jira-token"
project_keys = ["MIVIA"]
`, projectRoot))

	var stdout, stderr bytes.Buffer
	exitCode := runConfigCheck([]string{"--config", configPath, "--redacted-json"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected valid config, exit=%d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	raw := stdout.String()
	for _, forbidden := range []string{configPath, projectRoot, "/tmp/private", "/home/user", "127.0.0.1:9090", "https://example.atlassian.net", "JIRA_EMAIL", "jira-token"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("redacted report leaked %q: %s", forbidden, raw)
		}
	}
	var report configCheckReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != "valid" || report.Effective == nil {
		t.Fatalf("expected valid effective report, got %#v", report)
	}
	if got := report.Effective.Projects[0].Integrations.Jira.CredentialRefs; !got.Present || got.Class != "credential_ref" {
		t.Fatalf("expected credential refs to be class-only, got %#v", got)
	}
}

func TestConfigCheckRedactedJSONInvalidBind(t *testing.T) {
	configPath := writeConfigFixture(t, `
version = 1

[server]
http_addr = "0.0.0.0:8080"
`)

	assertConfigCheckInvalid(t, configPath, "invalid_bind", []string{"0.0.0.0:8080", configPath})
}

func TestConfigCheckRedactedJSONInvalidIngestion(t *testing.T) {
	configPath := writeConfigFixture(t, `
version = 1

[ingestion]
live_updates_enabled = true
content_graph_enabled = false
`)

	assertConfigCheckInvalid(t, configPath, "invalid_ingestion", []string{configPath})
}

func TestConfigCheckRedactedJSONInvalidWorkspace(t *testing.T) {
	configPath := writeConfigFixture(t, `
version = 1

[[projects]]
id = "example-service"
root_path = "/home/user/private/example"
workspace_mode = "edit"
digest_mode = "metadata_only"
`)

	assertConfigCheckInvalid(t, configPath, "invalid_workspace", []string{"/home/user/private/example", configPath})
}

func TestRunCLIConfigCheckRequiresRedactedJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	handled, exitCode := runCLI([]string{"config", "check", "--config", "config.toml"}, &stdout, &stderr)
	if !handled || exitCode != 2 {
		t.Fatalf("expected handled usage error, handled=%v exit=%d", handled, exitCode)
	}
}

func assertConfigCheckInvalid(t *testing.T, configPath string, category string, forbidden []string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exitCode := runConfigCheck([]string{"--config", configPath, "--redacted-json"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected invalid config exit 1, got %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	raw := stdout.String()
	for _, value := range forbidden {
		if strings.Contains(raw, value) {
			t.Fatalf("redacted invalid report leaked %q: %s", value, raw)
		}
	}
	var report configCheckReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != "invalid" || report.ErrorCategory != category {
		t.Fatalf("expected invalid category %q, got %#v", category, report)
	}
	if report.Effective != nil {
		t.Fatalf("invalid report must not include effective config: %#v", report.Effective)
	}
}

func writeConfigFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mivia-server.toml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o600); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return path
}
