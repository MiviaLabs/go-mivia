package projectingestion

import (
	"bytes"
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

func sensitiveFixture(parts ...string) string {
	return strings.Join(parts, "")
}

func syntheticPhoneFixture() string {
	return sensitiveFixture("+", "1 202 ", "555 ", "0101")
}

func TestEvaluateSafety_AllowsSafeUnityGeneratedYAMLScalars(t *testing.T) {
	content := []byte(sensitiveFixture(`%YAML 1.1
%TAG !u! tag:unity3d.com,2011:
--- !u!114 &11400000
MonoBehaviour:
  m_ObjectHideFlags: 0
  m_CorrespondingSourceObject: {fileID: 0}
  m_PrefabInstance: {fileID: 0}
  m_PrefabAsset: {fileID: 0}
  m_GameObject: {fileID: 1048576000000000000}
  m_Enabled: 1
  m_Script: {fileID: 11500000, guid: 0123456789abcdef0123456789abcdef, type: 3}
  m_Name: ENV_PROP_PANEL_TOKEN_LABEL
  access_`, "token: 0\n", "  secret", `: false
  password: null
  api_key: uuid
`))

	result := EvaluateSafety("Assets/Generated/ENV_PROP_PANEL_TOKEN_LABEL.prefab", content, DefaultSafetyOptions())
	if !result.Eligible {
		t.Fatalf("expected safe Unity YAML to be eligible, got %#v", result)
	}
}

func TestEvaluateSafety_AllowsOpenAPIYAMLSensitiveSchemaFieldNames(t *testing.T) {
	content := []byte(sensitiveFixture(`openapi: 3.0.0
components:
  schemas:
    Account:
      type: object
      properties:
        pass`, "word", `:
          type: string
          format: password
        phone:
          type: string
          format: phone
        access`, "Token", `:
          type: string
          nullable: true
`))

	result := EvaluateSafety("api/openapi.yaml", content, DefaultSafetyOptions())
	if !result.Eligible {
		t.Fatalf("expected OpenAPI schema YAML to be eligible, got %#v", result)
	}
}

func TestEvaluateSafety_RejectsSensitiveNestedYAMLValues(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "api key scalar", content: sensitiveFixture("auth:\n  api_", "key: ", "placeholder", "\n")},
		{name: "token nested value", content: sensitiveFixture("auth:\n  token:\n    value: ", "placeholder", "\n")},
		{name: "secret sequence", content: sensitiveFixture("items:\n  - secret:\n      - ", "placeholder", "\n")},
		{name: "password alias", content: sensitiveFixture("shared: &credential ", "placeholder", "\npassword: *credential\n")},
		{name: "password schema example", content: sensitiveFixture("properties:\n  password:\n    type: string\n    example: ", "placeholder", "\n")},
		{name: "phone field", content: sensitiveFixture("contact:\n  phone: \"", syntheticPhoneFixture(), "\"\n")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := EvaluateSafety("configs/service.yaml", []byte(test.content), DefaultSafetyOptions())
			if result.Reason != SkipReasonSensitiveContent {
				t.Fatalf("expected sensitive YAML skip, got %#v", result)
			}
		})
	}
}

func TestEvaluateSafety_MalformedYAMLFallsBackToSensitiveAssignment(t *testing.T) {
	content := []byte(sensitiveFixture("auth:\n  token: \"", "syntheticmarker", "\n"))
	result := EvaluateSafety("configs/service.yaml", content, DefaultSafetyOptions())
	if result.Reason != SkipReasonSensitiveContent {
		t.Fatalf("expected malformed YAML fallback to skip sensitive assignment, got %#v", result)
	}
}

func TestEvaluateSafety_RejectsSensitiveYAMLComments(t *testing.T) {
	content := []byte(sensitiveFixture("service: demo\n# token: ", "syntheticmarker", "\n"))
	result := EvaluateSafety("configs/service.yaml", content, DefaultSafetyOptions())
	if result.Reason != SkipReasonSensitiveContent {
		t.Fatalf("expected sensitive YAML comment to be skipped, got %#v", result)
	}
}

func TestEvaluateSafety_AllowsSafeYAMLComments(t *testing.T) {
	content := []byte("service: demo\n# password: string\n# token: 0\n")
	result := EvaluateSafety("configs/service.yaml", content, DefaultSafetyOptions())
	if !result.Eligible {
		t.Fatalf("expected safe YAML comments to be eligible, got %#v", result)
	}
}

func TestEvaluateSafety_DoesNotTreatYAMLBlockScalarLinesAsComments(t *testing.T) {
	content := []byte(sensitiveFixture("description: |\n  # token: ", "syntheticmarker", "\n  rendered as documentation text\n"))
	result := EvaluateSafety("configs/service.yaml", content, DefaultSafetyOptions())
	if !result.Eligible {
		t.Fatalf("expected YAML block scalar content to be eligible, got %#v", result)
	}
}

func TestBuildChunksFromReader_YAMLSafetyMatchesStreamingReader(t *testing.T) {
	safeContent := []byte(sensitiveFixture(`openapi: 3.0.0
components:
  schemas:
    Account:
      properties:
        pass`, "word", `:
          type: string
          format: password
        phone:
          type: string
`))
	chunks, result, err := BuildChunksFromReader("api/openapi.yaml", bytes.NewReader(safeContent), int64(len(safeContent)), SafetyOptions{
		MaxChunkBytes:         32,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	})
	if err != nil {
		t.Fatalf("build safe chunks: %v", err)
	}
	if !result.Eligible {
		t.Fatalf("expected safe OpenAPI YAML reader content to be eligible, got %#v", result)
	}
	if len(chunks.Chunks) == 0 {
		t.Fatal("expected safe OpenAPI YAML reader content to produce chunks")
	}

	sensitiveContent := []byte(sensitiveFixture("auth:\n  token:\n    value: ", "placeholder", "\n"))
	_, result, err = BuildChunksFromReader("configs/service.yaml", bytes.NewReader(sensitiveContent), int64(len(sensitiveContent)), SafetyOptions{
		MaxChunkBytes:         16,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	})
	if err != nil {
		t.Fatalf("build sensitive chunks: %v", err)
	}
	if result.Reason != SkipReasonSensitiveContent {
		t.Fatalf("expected sensitive YAML reader content to be skipped, got %#v", result)
	}
}

func TestStreamingSensitiveScanner_HardMarkersDoNotUseStructuredAssignments(t *testing.T) {
	scanner := newStreamingSensitiveScanner("Assets/Generated/scene.prefab")
	safeUnityContent := []byte(sensitiveFixture("api_", "key: uuid\n", "token: 0\nm_GameObject: {fileID: 1048576000000000000}\n"))
	if scanner.WriteHardMarkers(safeUnityContent) {
		t.Fatal("expected hard-marker scan to ignore safe structured assignment fields")
	}
	if !scanner.WriteHardMarkers([]byte("authorization: Bearer abcdefgh12345678\n")) {
		t.Fatal("expected hard-marker scan to detect bearer marker")
	}
}

func TestBuildChunksFromReader_SkipsOversizedStructuredSafetyBuffer(t *testing.T) {
	content := strings.Repeat("a", structuredStreamingSafetyMaxBytes+1)
	_, result, err := BuildChunksFromReader("configs/large.yaml", strings.NewReader(content), int64(len(content)), SafetyOptions{
		MaxChunkBytes:         1024,
		SensitiveMarkerPolicy: SensitiveMarkerPolicySkipFile,
	})
	if err != nil {
		t.Fatalf("build oversized structured chunks: %v", err)
	}
	if result.Reason != SkipReasonSemanticTooLarge {
		t.Fatalf("expected oversized structured content skip, got %#v", result)
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
