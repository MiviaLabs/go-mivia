package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
)

func TestBaselineGovernedCloseoutMetadataOnlySafety(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"raw_prompt",
		"raw_completion",
		"raw_stderr",
		"provider_payload",
		"source dump",
		"api_key",
	} {
		t.Run(value, func(t *testing.T) {
			if !unsafeCloseoutRefMaterial(value) {
				t.Fatalf("expected unsafe closeout ref material to reject %q", value)
			}
		})
	}
}

func TestBaselineGovernedCloseoutParserContract(t *testing.T) {
	t.Parallel()

	valid := `{"closeout_action":"needs_review","outcome":"metadata only","safe_next_action":"run verifier","evidence_refs":["evidence:baseline"],"verifier_result_refs":["verifier:baseline"],"child_tasks":[],"block_reason":"","failure_reason":""}`
	output, err := parseGovernedCloseoutOutput(valid)
	if err != nil {
		t.Fatalf("parse valid closeout: %v", err)
	}
	if output.CloseoutAction != "needs_review" || len(output.EvidenceRefs) != 1 || len(output.VerifierRefs) != 1 {
		t.Fatalf("unexpected valid closeout output: %+v", output)
	}

	for name, payload := range map[string]string{
		"missing":       "",
		"prose":         "closeout complete",
		"multiple_json": valid + "\n{}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGovernedCloseoutOutput(payload); err == nil {
				t.Fatalf("expected closeout parse to reject %s", name)
			}
		})
	}

	for name, payload := range map[string]string{
		"unsafe_ref":     strings.Replace(valid, "evidence:baseline", "raw_prompt", 1),
		"unsafe_path":    strings.Replace(valid, `"child_tasks":[]`, `"child_tasks":[{"task_ref":"child","title":"Child","description":"metadata","status":"planned","owner_agent":"worker","files_to_read":["../unsafe"],"verification_requirement":"go test ./cmd/mivia-automation-runner","decomposition_quality":"ready"}]`, 1),
		"unsafe_summary": strings.Replace(valid, "metadata only", "raw_prompt", 1),
	} {
		t.Run(name, func(t *testing.T) {
			output, err := parseGovernedCloseoutOutput(payload)
			if err != nil {
				t.Fatalf("parse %s before validation: %v", name, err)
			}
			if err := validateGovernedCloseoutOutput(output, runnerWorkTaskMetadata{}); err == nil {
				t.Fatalf("expected closeout validation to reject %s", name)
			}
		})
	}
}

func TestBaselineGovernedCloseoutFailureCategoryContract(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err         error
		want        string
		wantPrefix  string
		wantContain string
	}{
		"readback_failure": {
			err:  governedCloseoutError{category: governedCloseoutReadbackFailed, err: errors.New("wrapper_readback_failed: wrapper remained open after closeout apply")},
			want: "governed_closeout_readback_failed_wrapper_readback_failed",
		},
		"child_task_create_invalid_input": {
			err:         governedCloseoutError{category: governedCloseoutApplyFailed, err: errors.New("child_task_create_failed: server returned 400 Bad Request: invalid_project_work_task_input: resume_instructions contains unsafe content")},
			wantPrefix:  "governed_closeout_apply_failed_child_task_create_failed_",
			wantContain: "invalid_project_work_task_input",
		},
		"unsafe_child_task_path": {
			err:  governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe child task path /workspace/private/root")},
			want: "governed_closeout_validation_failed_unsafe_child_task_path",
		},
		"unsafe_ref": {
			err:  governedCloseoutError{category: governedCloseoutValidationFailed, err: errors.New("unsafe ref raw_prompt")},
			want: "governed_closeout_validation_failed_unsafe_ref",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := governedCloseoutFailureCategory(tc.err)
			if tc.want != "" && got != tc.want {
				t.Fatalf("failure category mismatch: got %q want %q", got, tc.want)
			}
			if tc.wantPrefix != "" && !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("failure category %q missing prefix %q", got, tc.wantPrefix)
			}
			if tc.wantContain != "" && !strings.Contains(got, tc.wantContain) {
				t.Fatalf("failure category %q missing %q", got, tc.wantContain)
			}
			for _, forbidden := range []string{"raw_prompt", "private/root", "resume_instructions contains unsafe content"} {
				if strings.Contains(got, forbidden) {
					t.Fatalf("failure category leaked unsafe detail %q in %q", forbidden, got)
				}
			}
		})
	}
}

func TestBaselineRunnerCodexCommandAndOutcomeContract(t *testing.T) {
	t.Parallel()

	inputPath := filepath.Join(t.TempDir(), "codex-input.json")
	outputPath := filepath.Join(t.TempDir(), "last-message.txt")
	command, err := buildRunnerCodexCommand(inputPath, outputPath, time.Minute, codexLaunchOptions{
		Path:             "/usr/local/bin/codex",
		Launcher:         "direct",
		WorkDir:          "/workspace/repo",
		Sandbox:          "workspace-write",
		OutputSchemaPath: "/workspace/repo/schema.json",
	})
	if err != nil {
		t.Fatalf("build runner codex command: %v", err)
	}
	joined := strings.Join(command.Args, " ")
	for _, want := range []string{
		"exec",
		"--sandbox workspace-write",
		"--output-schema /workspace/repo/schema.json",
		"--output-last-message " + outputPath,
		"--cd /workspace/repo",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("runner Codex command missing %q in %q", want, joined)
		}
	}
	if command.Path != "/usr/local/bin/codex" || command.Dir != "/workspace/repo" || command.Timeout != time.Minute {
		t.Fatalf("runner Codex command lost path/dir/timeout contract: %#v", command)
	}

	if _, err := buildRunnerCodexCommand(inputPath, outputPath, time.Minute, codexLaunchOptions{Path: "codex", Launcher: "windows-cmd", WorkDir: "/workspace/repo"}); !errors.Is(err, projectautomation.ErrInvalidInput) || !strings.Contains(err.Error(), "unknown codex launcher") {
		t.Fatalf("runner must reject unsupported launcher before execution, got %v", err)
	}
}

func TestBaselineRunnerClientCarriesRunnerIDOnClaimHeartbeatAndComplete(t *testing.T) {
	t.Parallel()

	var claimInput projectautomation.ClaimNextRunInput
	var heartbeatInput projectautomation.HeartbeatRunInput
	var completeInput projectautomation.CompleteAttemptInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/claim-next":
			if err := json.NewDecoder(r.Body).Decode(&claimInput); err != nil {
				t.Fatalf("decode claim input: %v", err)
			}
			writeJSON(t, w, projectautomation.ClaimedRun{Run: projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", ClaimID: "claim-1", RunnerID: "runner-baseline"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/heartbeat":
			if err := json.NewDecoder(r.Body).Decode(&heartbeatInput); err != nil {
				t.Fatalf("decode heartbeat input: %v", err)
			}
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", Status: projectautomation.RunStatusRunning, ClaimID: "claim-1", RunnerID: "runner-baseline"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1/attempt-result":
			if err := json.NewDecoder(r.Body).Decode(&completeInput); err != nil {
				t.Fatalf("decode complete input: %v", err)
			}
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-baseline"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/project-1/automation-runs/run-1":
			writeJSON(t, w, projectautomation.AutomationRun{ID: "run-1", ProjectID: "project-1", Status: projectautomation.RunStatusCompleted, ClaimID: "claim-1", RunnerID: "runner-baseline"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &runnerClient{baseURL: server.URL, http: server.Client(), runnerID: "runner-baseline"}
	claimed, ok, err := client.claimNext(t.Context(), "project-1", "worker")
	if err != nil || !ok {
		t.Fatalf("claim next: ok=%v err=%v", ok, err)
	}
	if err := client.heartbeatRun(t.Context(), "project-1", claimed.Run); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := client.completeAttempt(t.Context(), "project-1", claimed.Run, projectautomation.CompleteAttemptInput{Status: projectautomation.RunStatusCompleted}); err != nil {
		t.Fatalf("complete attempt: %v", err)
	}
	if claimInput.RunnerID != "runner-baseline" || claimInput.RunnerKind != projectautomation.RunnerKindCodexCLI {
		t.Fatalf("claim did not carry runner identity: %#v", claimInput)
	}
	if heartbeatInput.ClaimID != "claim-1" || heartbeatInput.RunnerID != "runner-baseline" {
		t.Fatalf("heartbeat did not carry claim and runner identity: %#v", heartbeatInput)
	}
	if completeInput.ClaimID != "claim-1" || completeInput.RunnerID != "runner-baseline" {
		t.Fatalf("complete did not carry claim and runner identity: %#v", completeInput)
	}
}
