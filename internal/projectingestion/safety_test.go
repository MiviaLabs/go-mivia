package projectingestion

import (
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/agentactivity"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
)

func TestEvaluateSafety_EligibleTextReturnsSafeMetadataOnly(t *testing.T) {
	result := EvaluateSafety("cmd/synthetic/main.go", []byte("package synthetic\n"), SafetyOptions{
		MaxFileBytes:          1024,
		MaxChunkBytes:         128,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	})

	if !result.Eligible {
		t.Fatalf("expected eligible result, got %q", result.Reason)
	}
	if result.RelativePath != "cmd/synthetic/main.go" {
		t.Fatalf("expected normalized relative path, got %q", result.RelativePath)
	}
	hash, err := EligibleContentSHA256(result, []byte("package synthetic\n"))
	if err != nil {
		t.Fatalf("hash eligible content: %v", err)
	}
	if !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("expected sha256 hash prefix, got %q", hash)
	}
}

func TestEvaluateSafety_RejectsUnsafePaths(t *testing.T) {
	for _, relativePath := range []string{
		"",
		"../outside.go",
		"docs/../outside.go",
		"/absolute/path.go",
		`dir\file.go`,
		"C:/Users/dev/project/main.go",
		"a\x00b.go",
	} {
		result := EvaluateSafety(relativePath, []byte("synthetic\n"), DefaultSafetyOptions())
		if result.Reason != SkipReasonUnsafePath {
			t.Fatalf("expected unsafe path for %q, got %#v", relativePath, result)
		}
	}
}

func TestEvaluateSafety_RejectsDeniedPathPatterns(t *testing.T) {
	for _, relativePath := range []string{
		".env.local",
		"src/secrets/config.txt",
		"data/cache.bin",
		"lib-ladybug/local.dat",
		"node_modules/pkg/index.js",
		"vendor/module/file.go",
		".venv/lib/site.py",
		"dist/app.js",
		"build/output.txt",
		"coverage/report.out",
		"certs/dev.pem",
		"certs/dev.key",
		"certs/dev.p12",
	} {
		result := EvaluateSafety(relativePath, []byte("synthetic\n"), DefaultSafetyOptions())
		if result.Reason != SkipReasonDeniedPath {
			t.Fatalf("expected denied path for %q, got %#v", relativePath, result)
		}
		if result.RelativePath != "" || result.RelativePathSafe {
			t.Fatalf("denied sensitive path must not return safe path metadata: %#v", result)
		}
	}
}

func TestEvaluateSafety_RejectsContentWithoutReturningMatchedTextOrHash(t *testing.T) {
	content := []byte("token = synthetic_marker_value\n")
	result := EvaluateSafety("src/config.txt", content, DefaultSafetyOptions())

	if result.Reason != SkipReasonSensitiveContent {
		t.Fatalf("expected sensitive content skip, got %#v", result)
	}
	if !result.RelativePathSafe || result.RelativePath != "src/config.txt" {
		t.Fatalf("expected only safe relative path metadata for content skip, got %#v", result)
	}
	if _, err := EligibleContentSHA256(result, content); err == nil {
		t.Fatal("expected skipped content hash to be rejected")
	}
	if strings.Contains(string(result.Reason), "synthetic_marker_value") {
		t.Fatalf("reason must not contain matched content: %q", result.Reason)
	}
}

func TestEvaluateSafety_AllowsOperationalDocsAndCodeMarkers(t *testing.T) {
	content := []byte(`MCP-Protocol-Version: 2025-06-18
Use http://127.0.0.1:8080/mcp for localhost-only development.
The workspace uses token-guarded exact edits and no raw patch endpoint.
`)
	result := EvaluateSafety("docs/configuration/local-projects.md", content, DefaultSafetyOptions())
	if !result.Eligible {
		t.Fatalf("expected operational docs to be eligible, got %#v", result)
	}

	code := []byte("secret := make([]byte, 32)\n")
	result = EvaluateSafety("internal/projectworkspace/service.go", code, DefaultSafetyOptions())
	if !result.Eligible {
		t.Fatalf("expected code declaration to be eligible, got %#v", result)
	}
}

func TestEvaluateSafety_RejectsBinaryInvalidUTF8AndOversizedContent(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		options SafetyOptions
		reason  SkipReason
	}{
		{
			name:    "oversized",
			content: []byte("abcdef"),
			options: SafetyOptions{MaxFileBytes: 4, SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile},
			reason:  SkipReasonFileTooLarge,
		},
		{
			name:    "nul",
			content: []byte{'a', 0, 'b'},
			options: DefaultSafetyOptions(),
			reason:  SkipReasonNULByte,
		},
		{
			name:    "binary",
			content: []byte{1, 2, 3, 4, 5, 'a'},
			options: DefaultSafetyOptions(),
			reason:  SkipReasonBinaryContent,
		},
		{
			name:    "invalid utf8",
			content: []byte{0xff, 0xfe, 'a'},
			options: DefaultSafetyOptions(),
			reason:  SkipReasonInvalidUTF8,
		},
	}

	for _, test := range tests {
		result := EvaluateSafety("src/synthetic.txt", test.content, test.options)
		if result.Reason != test.reason {
			t.Fatalf("%s: expected %q, got %#v", test.name, test.reason, result)
		}
	}
}

func TestEvaluateSafety_DefaultMaxFileBytesIsUnlimited(t *testing.T) {
	content := []byte(strings.Repeat("a", 2<<20))
	result := EvaluateSafety("src/large.txt", content, DefaultSafetyOptions())
	if !result.Eligible {
		t.Fatalf("expected default max_file_bytes to be unlimited, got %#v", result)
	}
}

func TestEvaluateSafety_RejectsUnsupportedSensitiveMarkerPolicy(t *testing.T) {
	result := EvaluateSafety("src/synthetic.txt", []byte("plain\n"), SafetyOptions{
		SensitiveMarkerPolicy: "store_marker",
	})

	if result.Reason != SkipReasonUnsupportedPolicy {
		t.Fatalf("expected unsupported policy skip, got %#v", result)
	}
}

func TestSkippedStateRecordsPolicyEventsForDeniedAndSensitiveSkips(t *testing.T) {
	recorder := agentactivity.NewRecorder(10)
	svc := &Service{}
	svc.SetPolicyRecorder(recorder)
	project := projectregistry.Project{ID: "example-service"}

	denied := svc.skippedState(project, "secrets/config.txt", SkipReasonDeniedPath, 10, time.Time{}, true, time.Now())
	sensitive := svc.skippedState(project, "src/config.txt", SkipReasonSensitiveContent, 10, time.Time{}, true, time.Now())

	if denied.RelativePath != "" || denied.RelativePathSafe {
		t.Fatalf("denied path must not persist relative path, got %#v", denied)
	}
	if sensitive.RelativePath != "" || sensitive.RelativePathSafe {
		t.Fatalf("sensitive content state must not persist relative path, got %#v", sensitive)
	}
	events := recorder.Recent("example-service", 10)
	if len(events) != 2 || events[0].PolicyCategory != "denied_path" || events[1].PolicyCategory != "sensitive_content" {
		t.Fatalf("expected normalized policy events, got %#v", events)
	}
	if events[0].RelativePath != "" || events[1].RelativePath != "" {
		t.Fatalf("policy events must omit unsafe/sensitive paths from these skips, got %#v", events)
	}
}
