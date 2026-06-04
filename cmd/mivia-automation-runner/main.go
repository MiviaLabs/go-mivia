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

	"github.com/MiviaLabs/go-mivia/internal/platform/config"
	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	"github.com/MiviaLabs/go-mivia/internal/projectgitops"
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
	codexSandbox := flags.String("codex-sandbox", "workspace-write", "sandbox mode passed to codex exec")
	codexBypass := flags.Bool("codex-bypass-approvals-and-sandbox", false, "pass Codex CLI's non-interactive approval and sandbox bypass flag")
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
	codexOptions := codexLaunchOptions{Path: strings.TrimSpace(*codexPath), Launcher: strings.TrimSpace(*codexLauncher), WorkDir: strings.TrimSpace(*codexCD), Sandbox: strings.TrimSpace(*codexSandbox), BypassApprovalsAndSandbox: *codexBypass}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		return 1
	}
	if cfg.GitOperations.Enabled && cfg.GitOperations.CommitAfterTask && !filepath.IsAbs(strings.TrimSpace(codexOptions.WorkDir)) {
		fmt.Fprintln(os.Stderr, "git operations require an absolute --codex-cd worktree")
		return 1
	}
	gitOps := projectgitops.New(gitOpsOptionsFromConfig(cfg.GitOperations))
	if err := checkCodexLauncher(context.Background(), codexOptions); err != nil {
		fmt.Fprintf(os.Stderr, "codex launcher unavailable: %v\n", err)
		return 1
	}
	if err := checkCodexConfigReadable(); err != nil {
		fmt.Fprintf(os.Stderr, "codex runtime config unavailable: %v\n", err)
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
		status, keepWatching, claimed := claimProjectRunsExecuteAndReport(context.Background(), client, projectIDs, strings.TrimSpace(*agentID), codexOptions, gitOps)
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

func checkCodexConfigReadable() error {
	configPath := codexConfigPath()
	if strings.TrimSpace(configPath) == "" {
		return nil
	}
	info, err := os.Stat(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w: codex_config_unreadable", projectautomation.ErrInvalidInput)
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%w: codex_config_unreadable", projectautomation.ErrInvalidInput)
	}
	file, err := os.Open(configPath)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w: codex_config_unreadable", projectautomation.ErrInvalidInput)
		}
		return err
	}
	return file.Close()
}

func codexConfigPath() string {
	if codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME")); codexHome != "" {
		return filepath.Join(codexHome, "config.toml")
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".codex", "config.toml")
	}
	return ""
}

func runnerProjectIDs(ctx context.Context, client *runnerClient, configuredProjectID string) ([]string, error) {
	if configuredProjectID != "" {
		return []string{configuredProjectID}, nil
	}
	return client.listProjectIDs(ctx)
}

type codexLaunchOptions struct {
	Path                      string
	Launcher                  string
	WorkDir                   string
	Sandbox                   string
	BypassApprovalsAndSandbox bool
}

func claimRunExecuteAndReport(ctx context.Context, client *runnerClient, projectID string, agentID string, codexOptions codexLaunchOptions, gitOps *projectgitops.Service) (int, bool, bool) {
	claimed, ok, err := client.claimNext(ctx, projectID, agentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claim failed: %v\n", err)
		return 1, false, false
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "no queued automation run")
		return 0, true, false
	}
	runWorkDir, err := client.resolveRunWorkDir(ctx, projectID, claimed.Run.PlanID, strings.TrimSpace(codexOptions.WorkDir))
	if err != nil {
		result := projectautomation.CompleteAttemptInput{
			Status:          projectautomation.RunStatusFailed,
			FailureCategory: "worktree_resolve_failed",
		}
		if _, reportErr := client.completeAttempt(ctx, projectID, claimed.Run.ID, result); reportErr != nil {
			fmt.Fprintf(os.Stderr, "attempt result report failed for %s: %v\n", claimed.Run.ID, reportErr)
			return 1, false, true
		}
		fmt.Fprintf(os.Stdout, "automation run %s reported %s\n", claimed.Run.ID, result.Status)
		return 1, true, true
	}
	runCodexOptions := codexOptions
	runCodexOptions.WorkDir = runWorkDir
	if err := gitOps.PreTask(ctx, runWorkDir); err != nil {
		result := projectautomation.CompleteAttemptInput{
			Status:          projectautomation.RunStatusFailed,
			FailureCategory: "gitops_pre_task_failed",
		}
		if errors.Is(err, projectgitops.ErrDirtyWorktree) {
			result.FailureCategory = "gitops_dirty_worktree"
		}
		if _, reportErr := client.completeAttempt(ctx, projectID, claimed.Run.ID, result); reportErr != nil {
			fmt.Fprintf(os.Stderr, "attempt result report failed for %s: %v\n", claimed.Run.ID, reportErr)
			return 1, false, true
		}
		fmt.Fprintf(os.Stdout, "automation run %s reported %s\n", claimed.Run.ID, result.Status)
		return 1, true, true
	}
	status, failureCategory, durationMS := runCodex(ctx, claimed, runCodexOptions)
	var evidenceRefs []string
	if status == projectautomation.RunStatusCompleted {
		gitResult, err := gitOps.PostTask(ctx, projectgitops.PostTaskInput{
			WorkDir:          runWorkDir,
			PlanID:           claimed.Run.PlanID,
			TaskID:           claimed.Run.TaskID,
			AutomationRunID:  claimed.Run.ID,
			CommitSubject:    "chore: complete " + claimed.Run.TaskID,
			AllowedPathspecs: claimed.CodexInput.LikelyFilesAffected,
		})
		if err != nil {
			status = projectautomation.RunStatusFailed
			failureCategory = "gitops_post_task_failed"
		} else {
			evidenceRefs = append(evidenceRefs, gitResult.EvidenceRefs...)
			for _, ref := range []string{gitResult.CommitRef, gitResult.PushRef, gitResult.PullRequestRef} {
				if strings.TrimSpace(ref) != "" {
					evidenceRefs = append(evidenceRefs, ref)
				}
			}
		}
	}
	result := projectautomation.CompleteAttemptInput{
		Status:          status,
		FailureCategory: failureCategory,
		DurationMS:      durationMS,
		EvidenceRefs:    evidenceRefs,
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

func claimProjectRunsExecuteAndReport(ctx context.Context, client *runnerClient, projectIDs []string, agentID string, codexOptions codexLaunchOptions, gitOps *projectgitops.Service) (int, bool, bool) {
	if len(projectIDs) == 0 {
		fmt.Fprintln(os.Stdout, "no configured projects")
		return 0, true, false
	}
	for _, projectID := range projectIDs {
		status, keepWatching, claimed := claimRunExecuteAndReport(ctx, client, projectID, agentID, codexOptions, gitOps)
		if claimed || !keepWatching || status != 0 {
			return status, keepWatching, claimed
		}
	}
	return 0, true, false
}

func gitOpsOptionsFromConfig(cfg config.GitOperations) projectgitops.Options {
	return projectgitops.Options{
		Enabled:                      cfg.Enabled,
		CommitAfterTask:              cfg.CommitAfterTask,
		PushAfterTask:                cfg.PushAfterTask,
		DraftPRAfterPush:             cfg.DraftPRAfterPush,
		RequireCleanBeforeTask:       cfg.RequireCleanBeforeTask,
		CleanupWorktreeAfterPlanDone: cfg.CleanupWorktreeAfterPlanDone,
		RemoteName:                   cfg.RemoteName,
		BranchPrefix:                 cfg.BranchPrefix,
		CommitAuthorName:             cfg.CommitAuthorName,
		CommitAuthorEmailEnv:         cfg.CommitAuthorEmailEnv,
		CommitAuthorEmailFile:        cfg.CommitAuthorEmailFile,
		SSHPrivateKeyPath:            cfg.SSHPrivateKeyPath,
		SSHPublicKeyPath:             cfg.SSHPublicKeyPath,
		SSHKnownHostsPath:            cfg.SSHKnownHostsPath,
		GitHubTokenEnv:               cfg.GitHubTokenEnv,
		GitHubTokenFile:              cfg.GitHubTokenFile,
		GitHubCLIPath:                cfg.GitHubCLIPath,
	}
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
	if result.SafeFailureCategory != "" {
		return projectautomation.RunStatusFailed, result.SafeFailureCategory, durationMS
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
		args = appendCodexExecutionOptions(args, codexOptions)
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
		args := []string{"exec"}
		args = appendCodexExecutionOptions(args, codexOptions)
		command.Args = append(args, "--cd", strings.TrimSpace(codexOptions.WorkDir), "-")
	} else {
		args := []string{"exec"}
		args = appendCodexExecutionOptions(args, codexOptions)
		command.Args = append(args, "-")
	}
	return command, nil
}

func appendCodexExecutionOptions(args []string, codexOptions codexLaunchOptions) []string {
	if codexOptions.BypassApprovalsAndSandbox {
		return append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if sandbox := strings.TrimSpace(codexOptions.Sandbox); sandbox != "" {
		return append(args, "--sandbox", sandbox)
	}
	return args
}

func writeCodexInput(input projectautomation.CodexTaskInput) (string, func(), error) {
	data := []byte(projectautomation.RenderCodexTaskPrompt(input))
	dir, err := os.MkdirTemp("", "mivia-external-automation-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "codex-input.txt")
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

type runnerWorkPlan struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	IsolationMode  string `json:"isolation_mode"`
	GitWorktreeRef string `json:"git_worktree_ref"`
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

func (client *runnerClient) resolveRunWorkDir(ctx context.Context, projectID string, planID string, baseWorkDir string) (string, error) {
	baseWorkDir = strings.TrimSpace(baseWorkDir)
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return baseWorkDir, nil
	}
	plan, err := client.getWorkPlan(ctx, projectID, planID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(plan.IsolationMode) != "dedicated_worktree" || strings.TrimSpace(plan.GitWorktreeRef) == "" {
		return baseWorkDir, nil
	}
	return dedicatedWorktreePath(baseWorkDir, projectID, plan.GitWorktreeRef)
}

func (client *runnerClient) getWorkPlan(ctx context.Context, projectID string, planID string) (runnerWorkPlan, error) {
	var plan runnerWorkPlan
	_, err := client.get(ctx, fmt.Sprintf("/api/v1/projects/%s/work-plans/%s", url.PathEscape(projectID), url.PathEscape(planID)), &plan)
	return plan, err
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

func dedicatedWorktreePath(baseWorkDir string, projectID string, worktreeRef string) (string, error) {
	baseWorkDir = strings.TrimSpace(baseWorkDir)
	if baseWorkDir == "" || !filepath.IsAbs(baseWorkDir) {
		return "", fmt.Errorf("%w: dedicated worktree requires an absolute base workdir", projectautomation.ErrInvalidInput)
	}
	projectSegment, err := safeProjectWorktreeSegment(projectID)
	if err != nil {
		return "", fmt.Errorf("invalid project id for worktree path: %w", err)
	}
	if err := validateWorktreeRef(worktreeRef); err != nil {
		return "", fmt.Errorf("invalid worktree ref for worktree path: %w", err)
	}
	worktreeSegment := safeWorktreeDirName(projectID + "-" + worktreeRef)
	if worktreeSegment == "" {
		return "", fmt.Errorf("invalid worktree ref for worktree path: %w", projectautomation.ErrInvalidInput)
	}
	root := filepath.Clean(filepath.Join(baseWorkDir, ".mivia-worktrees", projectSegment))
	target := filepath.Clean(filepath.Join(root, worktreeSegment))
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: worktree path escapes project worktree root", projectautomation.ErrInvalidInput)
	}
	return target, nil
}

func safeProjectWorktreeSegment(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", projectautomation.ErrInvalidInput
	}
	if strings.Contains(value, "..") || strings.ContainsAny(value, `/\`) {
		return "", projectautomation.ErrInvalidInput
	}
	if len(value) >= 2 && value[1] == ':' {
		return "", projectautomation.ErrInvalidInput
	}
	return value, nil
}

func validateWorktreeRef(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "..") || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || filepath.IsAbs(value) {
		return projectautomation.ErrInvalidInput
	}
	if len(value) >= 2 && value[1] == ':' {
		return projectautomation.ErrInvalidInput
	}
	return nil
}

func safeWorktreeDirName(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			continue
		}
		switch r {
		case '.', '_', '-':
			builder.WriteRune(r)
		case '/', ':', '@', '+':
			builder.WriteRune('-')
		}
	}
	return strings.Trim(builder.String(), ".-")
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
