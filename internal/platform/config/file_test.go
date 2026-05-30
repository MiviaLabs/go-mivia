package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFileConfig_ExampleConfigParses(t *testing.T) {
	path := filepath.Join(repoRoot(t), "configs", "agent-server.example.toml")

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected example config to parse: %v", err)
	}

	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected example config to apply: %v", err)
	}
	if len(merged.Projects) != 2 {
		t.Fatalf("expected two example projects, got %d", len(merged.Projects))
	}
}

func TestLoadFileConfig_RejectsUnknownTopLevelField(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[unexpected]
enabled = true
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unknown top-level field to fail")
	}
}

func TestLoadFileConfig_RejectsUnknownProjectField(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
unexpected = true
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unknown project field to fail")
	}
}

func TestLoadFileConfig_RejectsUnknownIngestionField(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[ingestion]
unexpected = true
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unknown ingestion field to fail")
	}
}

func TestLoadFileConfig_AcceptsContentGraphAndLiveContractValues(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
digest_mode = "content_graph"
update_policy = "live"
graph_storage = "in_memory"
max_file_bytes = 1024
max_chunk_bytes = 512
sensitive_marker_policy = "skip_file"
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected config contract values to parse: %v", err)
	}
	merged, err := cfg.applyTo(defaultConfig(path))
	if err != nil {
		t.Fatalf("expected config contract values to apply: %v", err)
	}
	project := merged.Projects[0]
	if project.DigestMode != digestModeContentGraph || project.UpdatePolicy != updatePolicyLive {
		t.Fatalf("unexpected project modes: %+v", project)
	}
	if project.GraphStorage != graphStorageInMemory {
		t.Fatalf("unexpected graph storage: %+v", project)
	}
	if project.MaxFileBytes != 1024 || project.MaxChunkBytes != 512 {
		t.Fatalf("unexpected project caps: %+v", project)
	}
}

func TestLoadFileConfig_RejectsUnsupportedDigestMode(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
digest_mode = "embedding"
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unsupported digest mode to fail")
	}
	if !strings.Contains(err.Error(), "digest_mode") {
		t.Fatalf("expected digest_mode error, got %v", err)
	}
}

func TestLoadFileConfig_RejectsUnsupportedUpdatePolicy(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
update_policy = "watch"
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unsupported update policy to fail")
	}
	if !strings.Contains(err.Error(), "update_policy") {
		t.Fatalf("expected update_policy error, got %v", err)
	}
}

func TestLoadFileConfig_RejectsUnsupportedGraphStorage(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
graph_storage = "remote"
`)

	_, err := loadFileConfig(path)
	if err == nil {
		t.Fatal("expected unsupported graph storage to fail")
	}
	if !strings.Contains(err.Error(), "graph_storage") {
		t.Fatalf("expected graph_storage error, got %v", err)
	}
}

func TestFileConfigApplyTo_RejectsInvalidDuration(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[server]
request_timeout = "soon"
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected TOML to parse before apply validation: %v", err)
	}
	if _, err := cfg.applyTo(defaultConfig(path)); err == nil {
		t.Fatal("expected invalid duration to fail")
	}
}

func TestFileConfigApplyTo_RejectsInvalidIngestionDuration(t *testing.T) {
	path := writeTempConfig(t, `
version = 1

[ingestion]
debounce_interval = "soon"
`)

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("expected TOML to parse before apply validation: %v", err)
	}
	if _, err := cfg.applyTo(defaultConfig(path)); err == nil {
		t.Fatal("expected invalid ingestion duration to fail")
	}
}

func TestLoadFileConfig_RejectsInvalidIngestionValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
		message string
	}{
		{
			name: "global max file bytes",
			content: `
version = 1

[ingestion]
max_file_bytes = 0
`,
			message: "max_file_bytes",
		},
		{
			name: "project max chunk bytes",
			content: `
version = 1

[[projects]]
id = "example"
display_name = "Example"
root_path = "/absolute/path/to/project"
max_chunk_bytes = 0
`,
			message: "max_chunk_bytes",
		},
		{
			name: "sensitive marker policy",
			content: `
version = 1

[ingestion]
sensitive_marker_policy = "store"
`,
			message: "sensitive_marker_policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempConfig(t, tt.content)
			_, err := loadFileConfig(path)
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("expected %q error, got %v", tt.message, err)
			}
		})
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent-server.local.toml")
	writeFile(t, path, content)
	return path
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found while locating repo root")
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
