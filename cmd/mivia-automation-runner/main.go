package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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
	projectID := flags.String("project", "", "project id; omit to watch all configured projects")
	agentID := flags.String("agent", "", "optional agent id filter")
	codexPath := flags.String("codex", "codex", "codex CLI binary path")
	codexLauncher := flags.String("codex-launcher", "direct", "codex launcher: direct or windows-cmd")
	codexCD := flags.String("codex-cd", "", "optional workspace directory passed to codex exec --cd")
	once := flags.Bool("once", true, "claim and run one queued task, then exit")
	watch := flags.Bool("watch", false, "continuously claim queued tasks until interrupted")
	pollInterval := flags.Duration("poll-interval", 5*time.Second, "poll interval when once is false")
	idleExitAfter := flags.Duration("idle-exit-after", 0, "optional idle duration after which watch mode exits; 0 disables idle exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: mivia-automation-runner --server http://127.0.0.1:8080 [--project <project_id>] [--agent <agent_id>] [--codex codex] [--once=false|--watch]")
		return 2
	}
	if *watch {
		*once = false
	}
	codexOptions := codexLaunchOptions{Path: strings.TrimSpace(*codexPath), Launcher: strings.TrimSpace(*codexLauncher), WorkDir: strings.TrimSpace(*codexCD)}
	if err := checkCodexLauncher(context.Background(), codexOptions); err != nil {
		fmt.Fprintf(os.Stderr, "codex launcher unavailable: %v\n", err)
		return 1
	}
	client := &runnerClient{baseURL: strings.TrimRight(strings.TrimSpace(*server), "/"), http: http.DefaultClient}
	var idleSince time.Time
	for {
		projectIDs, err := runnerProjectIDs(context.Background(), client, strings.TrimSpace(*projectID))
		if err != nil {
			fmt.Fprintf(os.Stderr, "project discovery failed: %v\n", err)
			return 1
		}
		status, keepWatching, claimed := claimProjectRunsExecuteAndReport(context.Background(), client, projectIDs, strings.TrimSpace(*agentID), codexOptions)
		if *once || !keepWatching {
			return status
		}
		if !claimed && *idleExitAfter > 0 {
			now := time.Now()
			if idleSince.IsZero() {
				idleSince = now
			}
			if now.Sub(idleSince) >= *idleExitAfter {
				return 0
			}
		}
		if claimed {
			idleSince = time.Time{}
		}
		time.Sleep(*pollInterval)
	}
}

func checkCodexLauncher(ctx context.Context, codexOptions codexLaunchOptions) error {
	launcher := strings.TrimSpace(codexOptions.Launcher)
	if launcher == "" {
		launcher = "direct"
	}
	binaryPath := strings.TrimSpace(codexOptions.Path)
	if binaryPath == "" {
		binaryPath = "codex"
	}
	switch launcher {
	case "direct":
		command := exec.CommandContext(ctx, binaryPath, "--version")
		var stderr bytes.Buffer
		command.Stderr = &stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("%s --version failed: %w: %s", binaryPath, err, strings.TrimSpace(stderr.String()))
		}
		return nil
	case "windows-cmd":
		command := exec.CommandContext(ctx, "cmd.exe", "/c", binaryPath, "--version")
		var stderr bytes.Buffer
		command.Stderr = &stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("cmd.exe /c %s --version failed: %w: %s", binaryPath, err, strings.TrimSpace(stderr.String()))
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown codex launcher", projectautomation.ErrInvalidInput)
	}
}

func runnerProjectIDs(ctx context.Context, client *runnerClient, configuredProjectID string) ([]string, error) {
	if configuredProjectID != "" {
		return []string{configuredProjectID}, nil
	}
	return client.listProjectIDs(ctx)
}

type codexLaunchOptions struct {
	Path     string
	Launcher string
	WorkDir  string
}

func claimRunExecuteAndReport(ctx context.Context, client *runnerClient, projectID string, agentID string, codexOptions codexLaunchOptions) (int, bool, bool) {
	claimed, ok, err := client.claimNext(ctx, projectID, agentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claim failed: %v\n", err)
		return 1, false, false
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "no queued automation run")
		return 0, true, false
	}
	status, failureCategory, durationMS := runCodex(ctx, claimed, codexOptions)
	result := projectautomation.CompleteAttemptInput{
		Status:          status,
		FailureCategory: failureCategory,
		DurationMS:      durationMS,
	}
	if _, err := client.completeAttempt(ctx, projectID, claimed.Run.ID, result); err != nil {
		fmt.Fprintf(os.Stderr, "attempt result report failed for %s: %v\n", claimed.Run.ID, err)
		return 1, false, true
	}
	fmt.Fprintf(os.Stdout, "automation run %s reported %s\n", claimed.Run.ID, status)
	if status == projectautomation.RunStatusCompleted {
		return 0, true, true
	}
	return 1, true, true
}

func claimProjectRunsExecuteAndReport(ctx context.Context, client *runnerClient, projectIDs []string, agentID string, codexOptions codexLaunchOptions) (int, bool, bool) {
	if len(projectIDs) == 0 {
		fmt.Fprintln(os.Stdout, "no configured projects")
		return 0, true, false
	}
	for _, projectID := range projectIDs {
		status, keepWatching, claimed := claimRunExecuteAndReport(ctx, client, projectID, agentID, codexOptions)
		if claimed || !keepWatching || status != 0 {
			return status, keepWatching, claimed
		}
	}
	return 0, true, false
}

func runCodex(ctx context.Context, claimed projectautomation.ClaimedRun, codexOptions codexLaunchOptions) (string, string, int64) {
	inputPath, cleanup, err := writeCodexInput(claimed.CodexInput)
	if err != nil {
		return projectautomation.RunStatusFailed, "codex_input_create_failed", 0
	}
	defer cleanup()
	timeout := time.Duration(claimed.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	command, err := buildRunnerCodexCommand(inputPath, timeout, codexOptions)
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

func buildRunnerCodexCommand(inputPath string, timeout time.Duration, codexOptions codexLaunchOptions) (projectautomation.CodexCommand, error) {
	launcher := strings.TrimSpace(codexOptions.Launcher)
	if launcher == "" {
		launcher = "direct"
	}
	binaryPath := strings.TrimSpace(codexOptions.Path)
	if binaryPath == "" {
		binaryPath = "codex"
	}
	if launcher == "windows-cmd" {
		convertedInputPath, err := windowsPathForRunner(inputPath)
		if err != nil {
			return projectautomation.CodexCommand{}, err
		}
		args := []string{"/c", "type", convertedInputPath, "|", binaryPath, "exec"}
		if strings.TrimSpace(codexOptions.WorkDir) != "" {
			convertedWorkDir, err := windowsPathForRunner(strings.TrimSpace(codexOptions.WorkDir))
			if err != nil {
				return projectautomation.CodexCommand{}, err
			}
			args = append(args, "--cd", convertedWorkDir)
		}
		args = append(args, "-")
		return projectautomation.CodexCommand{
			Path:    "cmd.exe",
			Args:    args,
			Timeout: timeout,
		}, nil
	}
	if launcher != "direct" {
		return projectautomation.CodexCommand{}, fmt.Errorf("%w: unknown codex launcher", projectautomation.ErrInvalidInput)
	}
	command, err := projectautomation.BuildCodexCommand(projectautomation.CodexCommandInput{
		BinaryPath: binaryPath,
		InputPath:  inputPath,
		Timeout:    timeout,
		EnvAllow:   map[string]string{},
	})
	if err != nil {
		return projectautomation.CodexCommand{}, err
	}
	if strings.TrimSpace(codexOptions.WorkDir) != "" {
		command.Args = []string{"exec", "--cd", strings.TrimSpace(codexOptions.WorkDir), "-"}
	}
	return command, nil
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

type projectListResponse struct {
	Projects []projectListItem `json:"projects"`
}

type projectListItem struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

var errNoQueuedRun = errors.New("no queued automation run")

var windowsPathForRunner = func(path string) (string, error) {
	if strings.HasPrefix(path, `\\`) || (len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/')) {
		return path, nil
	}
	out, err := exec.Command("wslpath", "-w", path).Output()
	if err != nil {
		return "", err
	}
	converted := strings.TrimSpace(string(out))
	if converted == "" || strings.ContainsAny(converted, "\x00\r\n") {
		return "", fmt.Errorf("%w: unsafe converted input path", projectautomation.ErrInvalidInput)
	}
	return converted, nil
}

func (client *runnerClient) claimNext(ctx context.Context, projectID string, agentID string) (projectautomation.ClaimedRun, bool, error) {
	input := projectautomation.ClaimNextRunInput{AgentID: agentID, RunnerKind: projectautomation.RunnerKindCodexCLI}
	var claimed projectautomation.ClaimedRun
	status, err := client.post(ctx, fmt.Sprintf("/api/v1/projects/%s/automation-runs/claim-next", url.PathEscape(projectID)), input, &claimed)
	if status == http.StatusBadRequest {
		if errors.Is(err, errNoQueuedRun) {
			return projectautomation.ClaimedRun{}, false, nil
		}
		return projectautomation.ClaimedRun{}, false, err
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

func (client *runnerClient) listProjectIDs(ctx context.Context) ([]string, error) {
	var output projectListResponse
	if _, err := client.get(ctx, "/api/v1/projects", &output); err != nil {
		return nil, err
	}
	projectIDs := make([]string, 0, len(output.Projects))
	for _, project := range output.Projects {
		if strings.TrimSpace(project.ID) == "" || !project.Enabled {
			continue
		}
		projectIDs = append(projectIDs, project.ID)
	}
	return projectIDs, nil
}

func (client *runnerClient) get(ctx context.Context, path string, output any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+path, nil)
	if err != nil {
		return 0, err
	}
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
		bodyText := strings.TrimSpace(body.String())
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(bodyText, "no queued automation run") {
			return resp.StatusCode, errNoQueuedRun
		}
		return resp.StatusCode, fmt.Errorf("server returned %s: %s", resp.Status, bodyText)
	}
	if output == nil {
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(output)
}
