package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	flags := flag.NewFlagSet("mivia-automation-runner", flag.ContinueOnError)
	server := flags.String("server", "http://127.0.0.1:8080", "mivia-server base URL")
	projectID := flags.String("project", "", "project id")
	agentID := flags.String("agent", "", "optional agent id filter")
	codexPath := flags.String("codex", "codex", "codex CLI binary path")
	once := flags.Bool("once", true, "claim and run one queued task, then exit")
	pollInterval := flags.Duration("poll-interval", 5*time.Second, "poll interval when once is false")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*projectID) == "" {
		fmt.Fprintln(os.Stderr, "usage: mivia-automation-runner --server http://127.0.0.1:8080 --project <project_id> [--agent <agent_id>] [--codex codex] [--once=false]")
		return 2
	}
	client := &runnerClient{baseURL: strings.TrimRight(strings.TrimSpace(*server), "/"), http: http.DefaultClient}
	for {
		status := claimRunExecuteAndReport(context.Background(), client, strings.TrimSpace(*projectID), strings.TrimSpace(*agentID), strings.TrimSpace(*codexPath))
		if *once || status != 0 {
			return status
		}
		time.Sleep(*pollInterval)
	}
}

func claimRunExecuteAndReport(ctx context.Context, client *runnerClient, projectID string, agentID string, codexPath string) int {
	claimed, ok, err := client.claimNext(ctx, projectID, agentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claim failed: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "no queued automation run")
		return 0
	}
	status, failureCategory, durationMS := runCodex(ctx, claimed, codexPath)
	result := projectautomation.CompleteAttemptInput{
		Status:          status,
		FailureCategory: failureCategory,
		DurationMS:      durationMS,
	}
	if _, err := client.completeAttempt(ctx, projectID, claimed.Run.ID, result); err != nil {
		fmt.Fprintf(os.Stderr, "attempt result report failed for %s: %v\n", claimed.Run.ID, err)
		return 1
	}
	fmt.Fprintf(os.Stdout, "automation run %s reported %s\n", claimed.Run.ID, status)
	if status == projectautomation.RunStatusCompleted {
		return 0
	}
	return 1
}

func runCodex(ctx context.Context, claimed projectautomation.ClaimedRun, codexPath string) (string, string, int64) {
	inputPath, cleanup, err := writeCodexInput(claimed.CodexInput)
	if err != nil {
		return projectautomation.RunStatusFailed, "codex_input_create_failed", 0
	}
	defer cleanup()
	timeout := time.Duration(claimed.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	command, err := projectautomation.BuildCodexCommand(projectautomation.CodexCommandInput{
		BinaryPath: codexPath,
		InputPath:  inputPath,
		Timeout:    timeout,
		EnvAllow:   map[string]string{},
	})
	if err != nil {
		return projectautomation.RunStatusFailed, "codex_command_denied", 0
	}
	result, err := projectautomation.RunCodexCommand(ctx, command, 64*1024)
	durationMS := result.Duration.Milliseconds()
	if err == nil {
		return projectautomation.RunStatusCompleted, "", durationMS
	}
	if result.TimedOut {
		return projectautomation.RunStatusTimeout, "codex_cli_timeout", durationMS
	}
	return projectautomation.RunStatusFailed, "codex_cli_failed", durationMS
}

func writeCodexInput(input projectautomation.CodexTaskInput) (string, func(), error) {
	data, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "mivia-external-automation-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "codex-input.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

type runnerClient struct {
	baseURL string
	http    *http.Client
}

func (client *runnerClient) claimNext(ctx context.Context, projectID string, agentID string) (projectautomation.ClaimedRun, bool, error) {
	input := projectautomation.ClaimNextRunInput{AgentID: agentID, RunnerKind: projectautomation.RunnerKindCodexCLI}
	var claimed projectautomation.ClaimedRun
	status, err := client.post(ctx, fmt.Sprintf("/api/v1/projects/%s/automation-runs/claim-next", url.PathEscape(projectID)), input, &claimed)
	if status == http.StatusBadRequest {
		return projectautomation.ClaimedRun{}, false, nil
	}
	if err != nil {
		return projectautomation.ClaimedRun{}, false, err
	}
	return claimed, true, nil
}

func (client *runnerClient) completeAttempt(ctx context.Context, projectID string, runID string, input projectautomation.CompleteAttemptInput) (projectautomation.AutomationRun, error) {
	var run projectautomation.AutomationRun
	_, err := client.post(ctx, fmt.Sprintf("/api/v1/projects/%s/automation-runs/%s/attempt-result", url.PathEscape(projectID), url.PathEscape(runID)), input, &run)
	return run, err
}

func (client *runnerClient) post(ctx context.Context, path string, input any, output any) (int, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body bytes.Buffer
		_, _ = body.ReadFrom(resp.Body)
		return resp.StatusCode, fmt.Errorf("server returned %s: %s", resp.Status, strings.TrimSpace(body.String()))
	}
	if output == nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(output)
}
