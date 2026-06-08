package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"sync"
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
	codexPath := flags.String("codex", "/usr/local/bin/codex", "codex CLI binary path")
	codexLauncher := flags.String("codex-launcher", "direct", "codex launcher: direct")
	codexCD := flags.String("codex-cd", "", "optional workspace directory passed to codex exec --cd")
	codexSandbox := flags.String("codex-sandbox", "workspace-write", "sandbox mode passed to codex exec")
	codexBypass := flags.Bool("codex-bypass-approvals-and-sandbox", true, "pass Codex CLI's non-interactive approval and sandbox bypass flag")
	codexSmokePreflight := flags.Bool("codex-smoke-preflight", false, "run a non-mutating codex exec smoke test in --codex-cd before claiming work")
	once := flags.Bool("once", true, "claim and run one queued task, then exit")
	watch := flags.Bool("watch", false, "continuously claim queued tasks until interrupted")
	pollInterval := flags.Duration("poll-interval", 5*time.Second, "poll interval when once is false")
	requestTimeout := flags.Duration("request-timeout", 30*time.Second, "HTTP timeout for server discovery, claim, and report requests")
	heartbeatInterval := flags.Duration("heartbeat-interval", 15*time.Second, "external run heartbeat interval")
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
	codexOptions := codexLaunchOptions{Path: strings.TrimSpace(*codexPath), Launcher: strings.TrimSpace(*codexLauncher), WorkDir: strings.TrimSpace(*codexCD), Sandbox: strings.TrimSpace(*codexSandbox), BypassApprovalsAndSandbox: *codexBypass, SmokePreflight: *codexSmokePreflight}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config load failed: %v\n", err)
		return 1
	}
	if cfg.GitOperations.Enabled && cfg.GitOperations.CommitAfterTask && !filepath.IsAbs(strings.TrimSpace(codexOptions.WorkDir)) {
		fmt.Fprintln(os.Stderr, "git operations require an absolute --codex-cd worktree")
		return 1
	}
	if err := checkCodexLauncher(context.Background(), codexOptions); err != nil {
		fmt.Fprintf(os.Stderr, "codex launcher unavailable: %v\n", err)
		return 1
	}
	if err := checkCodexConfigReadable(); err != nil {
		fmt.Fprintf(os.Stderr, "codex runtime config unavailable: %v\n", err)
		return 1
	}
	if err := checkRunnerCodexPreflight(context.Background(), codexOptions); err != nil {
		fmt.Fprintf(os.Stderr, "codex runtime preflight failed: %v\n", err)
		return 1
	}
	runnerID := defaultRunnerID()
	client := &runnerClient{baseURL: strings.TrimRight(strings.TrimSpace(*server), "/"), http: &http.Client{Timeout: normalizedRequestTimeout(*requestTimeout)}, runnerID: runnerID, heartbeatInterval: normalizedHeartbeatInterval(*heartbeatInterval), projectCleanupInterval: defaultProjectCleanupInterval}
	var idleSince time.Time
	for {
		projectIDs, err := runnerProjectIDs(context.Background(), client, strings.TrimSpace(*projectID))
		if err != nil {
			fmt.Fprintf(os.Stderr, "project discovery failed: %v\n", err)
			if *once {
				return 1
			}
			time.Sleep(*pollInterval)
			continue
		}
		status, keepWatching, claimed := claimProjectRunsExecuteAndReport(context.Background(), client, cfg, projectIDs, strings.TrimSpace(*agentID), codexOptions)
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

func normalizedRequestTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 30 * time.Second
	}
	if timeout < time.Second {
		return time.Second
	}
	return timeout
}

func normalizedHeartbeatInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 15 * time.Second
	}
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func defaultRunnerID() string {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "runner"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
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

func checkRunnerCodexPreflight(ctx context.Context, codexOptions codexLaunchOptions) error {
	workDir := strings.TrimSpace(codexOptions.WorkDir)
	if workDir == "" {
		return nil
	}
	if err := checkRunnerWorkDir(ctx, workDir); err != nil {
		return err
	}
	if !codexOptions.SmokePreflight {
		return nil
	}
	inputPath, cleanupInput, err := writeCodexInput(projectautomation.CodexTaskInput{
		SchemaVersion:  1,
		ProjectID:      "runner-preflight",
		TaskID:         "runner-preflight",
		TaskRef:        "runner-preflight",
		Title:          "Runner preflight",
		Description:    "Verify Codex can execute a read-only shell command in the configured workdir without modifying files. Run pwd before returning.",
		ExpectedOutput: "Return only {\"ok\":true} after a successful pwd shell command. Return {\"ok\":false} if shell execution fails. Do not modify files.",
	})
	if err != nil {
		return fmt.Errorf("%w: codex_preflight_input_create_failed", projectautomation.ErrInvalidInput)
	}
	defer cleanupInput()
	outputPath, cleanupOutput, err := createCodexOutputFile()
	if err != nil {
		return fmt.Errorf("%w: codex_preflight_output_create_failed", projectautomation.ErrInvalidInput)
	}
	defer cleanupOutput()
	smokeOptions := codexOptions
	smokeOptions.OutputSchemaPath = ""
	command, err := buildRunnerCodexCommand(inputPath, outputPath, 2*time.Minute, smokeOptions)
	if err != nil {
		return err
	}
	result, err := projectautomation.RunCodexCommand(ctx, command, 16*1024)
	if err != nil {
		if result.SafeFailureCategory != "" {
			return fmt.Errorf("%w: %s", projectautomation.ErrInvalidInput, result.SafeFailureCategory)
		}
		return fmt.Errorf("%w: codex_preflight_exec_failed", projectautomation.ErrInvalidInput)
	}
	if !codexPreflightOutputOK(readCodexLastMessage(outputPath, result.Output)) {
		return fmt.Errorf("%w: codex_preflight_unexpected_output", projectautomation.ErrInvalidInput)
	}
	return nil
}

func codexPreflightOutputOK(message string) bool {
	var output struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(message)), &output); err != nil {
		return false
	}
	return output.OK
}

func checkRunnerWorkDir(ctx context.Context, workDir string) error {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return nil
	}
	if !filepath.IsAbs(workDir) || strings.ContainsAny(workDir, "\x00\r\n") {
		return fmt.Errorf("%w: workdir must be absolute and safe", projectautomation.ErrInvalidInput)
	}
	info, err := os.Stat(workDir)
	if err != nil {
		return fmt.Errorf("%w: codex_workdir_unavailable", projectautomation.ErrInvalidInput)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: codex_workdir_not_directory", projectautomation.ErrInvalidInput)
	}
	if _, err := os.ReadDir(workDir); err != nil {
		return fmt.Errorf("%w: codex_workdir_unreadable", projectautomation.ErrInvalidInput)
	}
	return prepareRunWorktree(ctx, workDir)
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
	SmokePreflight            bool
	OutputSchemaPath          string
}

func claimRunExecuteAndReport(ctx context.Context, client *runnerClient, cfg config.Config, projectID string, agentID string, codexOptions codexLaunchOptions) (int, bool, bool) {
	gitOpsOptions := gitOpsOptionsForProject(cfg, projectID)
	if client.shouldRunProjectCleanup(projectID, strings.TrimSpace(codexOptions.WorkDir)) {
		cleanupTerminalProjectWorktrees(ctx, client, gitOpsOptions, projectID, strings.TrimSpace(codexOptions.WorkDir))
	}

	claimed, ok, err := client.claimNext(ctx, projectID, agentID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "claim failed: %v\n", err)
		return 1, true, false
	}
	if !ok {
		fmt.Fprintln(os.Stdout, "no queued automation run")
		return 0, true, false
	}
	stopHeartbeat := client.startHeartbeat(ctx, projectID, claimed.Run)
	defer stopHeartbeat()
	runWorkDir, err := client.resolveRunWorkDir(ctx, projectID, claimed.Run.PlanID, strings.TrimSpace(codexOptions.WorkDir))
	if err != nil {
		result := projectautomation.CompleteAttemptInput{
			Status:          projectautomation.RunStatusFailed,
			FailureCategory: "worktree_resolve_failed",
		}
		completedRun, reportErr := client.completeAttempt(ctx, projectID, claimed.Run, result)
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "attempt result report failed for %s: %v\n", claimed.Run.ID, reportErr)
			return 1, false, true
		}
		fmt.Fprintf(os.Stdout, "automation run %s durably reported %s\n", claimed.Run.ID, completedRun.Status)
		return 1, true, true
	}
	runCodexOptions := codexOptions
	runCodexOptions.WorkDir = runWorkDir
	claimed.CodexInput.MCPServerURL = client.baseURL
	claimed.CodexInput.RunnerInstructions = append(claimed.CodexInput.RunnerInstructions, verificationInstructionsForProject(cfg, projectID)...)
	gitOps := projectgitops.New(gitOpsOptions)
	readOnlyReviewRun := isReadOnlyReviewRun(claimed)
	taskMetadata, taskMetadataErr := runnerWorkTaskMetadata{}, error(nil)
	if strings.TrimSpace(claimed.Run.TaskID) != "" {
		taskMetadata, taskMetadataErr = client.getWorkTaskMetadata(ctx, projectID, claimed.Run.TaskID)
	}
	runGitOps := !readOnlyReviewRun && (taskMetadataErr != nil || shouldRunGitOpsForTask(taskMetadata))
	if isGitOpsPostTaskRecoveryRun(claimed.Run) {
		status, failureCategory, durationMS, evidenceRefs := runGitOpsPostTaskRecovery(ctx, client, gitOps, projectID, runWorkDir, agentID, claimed)
		result := projectautomation.CompleteAttemptInput{
			Status:          status,
			FailureCategory: failureCategory,
			DurationMS:      durationMS,
			EvidenceRefs:    evidenceRefs,
		}
		if status != projectautomation.RunStatusCompleted && strings.TrimSpace(failureCategory) != "" {
			fmt.Fprintf(os.Stdout, "automation run %s reporting %s failure_category=%s\n", claimed.Run.ID, status, failureCategory)
		}
		completedRun, err := client.completeAttempt(ctx, projectID, claimed.Run, result)
		if err != nil {
			fmt.Fprintf(os.Stderr, "attempt result report failed for %s: %v\n", claimed.Run.ID, err)
			return 1, false, true
		}
		cleanupTerminalPlanWorktree(ctx, client, gitOpsOptions, projectID, firstNonEmpty(completedRun.PlanID, claimed.Run.PlanID), strings.TrimSpace(codexOptions.WorkDir), runWorkDir)
		fmt.Fprintf(os.Stdout, "automation run %s durably reported %s\n", claimed.Run.ID, completedRun.Status)
		if status == projectautomation.RunStatusCompleted {
			return 0, true, true
		}
		return 1, true, true
	}
	if runGitOps {
		var preTaskErr error
		if shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed, taskMetadata) {
			preTaskErr = gitOps.PreTaskWithinScope(ctx, runWorkDir, gitOpsTaskPathspecs(claimed, taskMetadata))
		} else {
			preTaskErr = gitOps.PreTask(ctx, runWorkDir)
		}
		if preTaskErr != nil {
			if errors.Is(preTaskErr, projectgitops.ErrDirtyWorktreeScope) && shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed, taskMetadata) {
				if scopedWorkDir, scopedErr := client.resolveTaskScopedRunWorkDir(ctx, projectID, claimed.Run.PlanID, claimed.Run.TaskID, strings.TrimSpace(codexOptions.WorkDir)); scopedErr == nil {
					runWorkDir = scopedWorkDir
					runCodexOptions.WorkDir = scopedWorkDir
					if prepareErr := prepareRunWorktree(ctx, runWorkDir); prepareErr != nil {
						preTaskErr = prepareErr
					} else {
						preTaskErr = gitOps.PreTaskWithinScope(ctx, runWorkDir, gitOpsTaskPathspecs(claimed, taskMetadata))
					}
				}
			}
		}
		if preTaskErr != nil {
			result := projectautomation.CompleteAttemptInput{
				Status:          projectautomation.RunStatusFailed,
				FailureCategory: "gitops_pre_task_failed",
				EvidenceRefs:    gitOpsDirtyScopeEvidenceRefs(preTaskErr),
			}
			switch {
			case errors.Is(preTaskErr, projectgitops.ErrDirtyWorktreeScope):
				result.FailureCategory = "gitops_dirty_worktree_scope"
			case errors.Is(preTaskErr, projectgitops.ErrDirtyWorktree):
				result.FailureCategory = "gitops_dirty_worktree"
			}
			completedRun, reportErr := client.completeAttempt(ctx, projectID, claimed.Run, result)
			if reportErr != nil {
				fmt.Fprintf(os.Stderr, "attempt result report failed for %s: %v\n", claimed.Run.ID, reportErr)
				return 1, false, true
			}
			fmt.Fprintf(os.Stdout, "automation run %s durably reported %s\n", claimed.Run.ID, completedRun.Status)
			return 1, true, true
		}
	}
	if strings.TrimSpace(claimed.Run.TaskID) != "" && taskMetadataErr == nil && taskRequiresExplicitGovernedCloseout(taskMetadata) && shouldUseCodexOutputSchemaForGovernedCloseout(taskMetadata) {
		schemaPath, cleanupSchema, schemaErr := createGovernedCloseoutSchemaFile()
		if schemaErr != nil {
			result := projectautomation.CompleteAttemptInput{
				Status:          projectautomation.RunStatusFailed,
				FailureCategory: "governed_closeout_schema_create_failed",
			}
			completedRun, reportErr := client.completeAttempt(ctx, projectID, claimed.Run, result)
			if reportErr != nil {
				fmt.Fprintf(os.Stderr, "attempt result report failed for %s: %v\n", claimed.Run.ID, reportErr)
				return 1, false, true
			}
			fmt.Fprintf(os.Stdout, "automation run %s durably reported %s\n", claimed.Run.ID, completedRun.Status)
			return 1, true, true
		}
		defer cleanupSchema()
		runCodexOptions.OutputSchemaPath = schemaPath
	}
	codexResult := runCodex(ctx, claimed, runCodexOptions)
	status, failureCategory, durationMS := codexResult.Status, codexResult.FailureCategory, codexResult.DurationMS
	var evidenceRefs []string
	if status == projectautomation.RunStatusCompleted {
		taskMetadata, taskMetadataErr = client.getWorkTaskMetadata(ctx, projectID, claimed.Run.TaskID)
		if status == projectautomation.RunStatusCompleted && strings.TrimSpace(claimed.Run.TaskID) != "" && taskMetadataErr == nil && taskRequiresExplicitGovernedCloseout(taskMetadata) && !taskHasGovernedCloseout(taskMetadata) {
			if closeoutErr := client.applyGovernedCloseoutFromOutput(ctx, projectID, claimed, taskMetadata, codexResult.LastMessage); closeoutErr != nil {
				status = projectautomation.RunStatusFailed
				failureCategory = governedCloseoutFailureCategory(closeoutErr)
			} else {
				taskMetadata, taskMetadataErr = client.getWorkTaskMetadata(ctx, projectID, claimed.Run.TaskID)
			}
		}
		if status == projectautomation.RunStatusCompleted && strings.TrimSpace(claimed.Run.TaskID) != "" && taskMetadataErr == nil && !taskHasGovernedCloseout(taskMetadata) && shouldAutoCloseoutMetadataOnlyTask(readOnlyReviewRun, taskMetadata) {
			if closeoutErr := client.closeoutMetadataOnlyTask(ctx, projectID, claimed, readOnlyReviewRun); closeoutErr != nil {
				status = projectautomation.RunStatusFailed
				failureCategory = "automation_task_closeout_failed"
			} else {
				taskMetadata, taskMetadataErr = client.getWorkTaskMetadata(ctx, projectID, claimed.Run.TaskID)
			}
		}
		if status == projectautomation.RunStatusCompleted && strings.TrimSpace(claimed.Run.TaskID) != "" && taskMetadataErr == nil && !taskHasGovernedCloseout(taskMetadata) {
			status = projectautomation.RunStatusFailed
			failureCategory = "automation_task_closeout_missing"
		}
		if status == projectautomation.RunStatusCompleted && !readOnlyReviewRun && shouldRunGitOpsForTask(taskMetadata) {
			gitResult, err := gitOps.PostTask(ctx, gitOpsPostTaskInput(projectID, runWorkDir, agentID, claimed, taskMetadata))
			if err != nil {
				status = projectautomation.RunStatusFailed
				failureCategory = gitOpsFailureCategoryForRunner(err)
				evidenceRefs = append(evidenceRefs, gitOpsFailureEvidenceRefs(err)...)
			} else {
				evidenceRefs = append(evidenceRefs, gitResult.EvidenceRefs...)
				for _, ref := range []string{gitResult.CommitRef, gitResult.PushRef, gitResult.PullRequestRef} {
					if strings.TrimSpace(ref) != "" {
						evidenceRefs = append(evidenceRefs, ref)
					}
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
	if status != projectautomation.RunStatusCompleted && strings.TrimSpace(failureCategory) != "" {
		fmt.Fprintf(os.Stdout, "automation run %s reporting %s failure_category=%s\n", claimed.Run.ID, status, failureCategory)
	}
	completedRun, err := client.completeAttempt(ctx, projectID, claimed.Run, result)
	if err != nil {
		fmt.Fprintf(os.Stderr, "attempt result report failed for %s: %v\n", claimed.Run.ID, err)
		return 1, false, true
	}
	cleanupTerminalPlanWorktree(ctx, client, gitOpsOptions, projectID, firstNonEmpty(completedRun.PlanID, claimed.Run.PlanID), strings.TrimSpace(codexOptions.WorkDir), runWorkDir)
	fmt.Fprintf(os.Stdout, "automation run %s durably reported %s\n", claimed.Run.ID, completedRun.Status)
	if status == projectautomation.RunStatusCompleted {
		return 0, true, true
	}
	return 1, true, true
}

const defaultProjectCleanupInterval = 5 * time.Minute

func (client *runnerClient) shouldRunProjectCleanup(projectID string, baseWorkDir string) bool {
	baseWorkDir = strings.TrimSpace(baseWorkDir)
	if baseWorkDir == "" {
		return false
	}
	interval := client.projectCleanupInterval
	if interval <= 0 {
		interval = defaultProjectCleanupInterval
	}
	key := strings.TrimSpace(projectID) + "\x00" + filepath.Clean(baseWorkDir)
	now := time.Now()
	client.projectCleanupMu.Lock()
	defer client.projectCleanupMu.Unlock()
	if client.projectCleanupLast == nil {
		client.projectCleanupLast = map[string]time.Time{}
	}
	if last, ok := client.projectCleanupLast[key]; ok && now.Sub(last) < interval {
		return false
	}
	client.projectCleanupLast[key] = now
	return true
}

func shouldRunGitOpsForTask(task runnerWorkTaskMetadata) bool {
	if strings.TrimSpace(task.TaskRef) == "final-pr-readiness" {
		return true
	}
	return len(task.FilesToEdit) > 0
}

func shouldAllowScopedDirtyWorktreeForExistingImplementation(claimed projectautomation.ClaimedRun, task runnerWorkTaskMetadata) bool {
	switch strings.TrimSpace(claimed.Run.SafeSummary) {
	case "dependency_ready_automation_queued", "pre_execution_recovery", "external_runner_queued":
	default:
		return false
	}
	if len(task.FilesToEdit) == 0 {
		return false
	}
	if strings.TrimSpace(task.BlockedReason) != "" && len(task.EvidenceRefs) > 0 {
		return true
	}
	return len(task.EvidenceRefs) > 0 || len(task.ClaimRefs) > 0 || len(task.ReviewResultRefs) > 0 || len(task.VerifierResultRefs) > 0
}

func gitOpsTaskPathspecs(claimed projectautomation.ClaimedRun, task runnerWorkTaskMetadata) []string {
	if len(task.FilesToEdit) > 0 {
		return append([]string(nil), task.FilesToEdit...)
	}
	return append([]string(nil), claimed.CodexInput.LikelyFilesAffected...)
}

func gitOpsDirtyScopeEvidenceRefs(err error) []string {
	paths := projectgitops.DirtyWorktreeScopePaths(err)
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		out = append(out, "gitops-dirty-path:"+path)
		if len(out) >= 20 {
			break
		}
	}
	return out
}

func gitOpsFailureEvidenceRefs(err error) []string {
	category := gitOpsFailureCategoryForRunner(err)
	out := make([]string, 0, 1)
	if category != "" {
		out = append(out, "gitops-failure:"+category)
	}
	out = append(out, gitOpsDirtyScopeEvidenceRefs(err)...)
	return out
}

func gitOpsFailureCategoryForRunner(err error) string {
	category := strings.TrimSpace(projectgitops.FailureCategoryWithDetail(err))
	if category == "gitops_post_task_failed" || category == "gitops_post_task_failed_unclassified" {
		return "gitops_post_task_failed_runner_post_task"
	}
	return category
}

func isReadOnlyReviewRun(claimed projectautomation.ClaimedRun) bool {
	if strings.TrimSpace(claimed.Run.SafeSummary) == projectautomation.RunSafeSummaryPostImplementationReviewQueued {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(claimed.CodexInput.TaskRef), "review-")
}

func runGitOpsPostTaskRecovery(ctx context.Context, client *runnerClient, gitOps *projectgitops.Service, projectID string, runWorkDir string, agentID string, claimed projectautomation.ClaimedRun) (string, string, int64, []string) {
	started := time.Now()
	taskMetadata, err := client.getWorkTaskMetadata(ctx, projectID, claimed.Run.TaskID)
	if err != nil {
		return projectautomation.RunStatusFailed, "automation_task_metadata_unavailable", time.Since(started).Milliseconds(), nil
	}
	if !taskHasGovernedCloseout(taskMetadata) {
		return projectautomation.RunStatusFailed, "automation_task_closeout_missing", time.Since(started).Milliseconds(), nil
	}
	gitResult, err := gitOps.PostTask(ctx, gitOpsPostTaskInput(projectID, runWorkDir, agentID, claimed, taskMetadata))
	if err != nil {
		return projectautomation.RunStatusFailed, gitOpsFailureCategoryForRunner(err), time.Since(started).Milliseconds(), gitOpsFailureEvidenceRefs(err)
	}
	evidenceRefs := append([]string(nil), gitResult.EvidenceRefs...)
	for _, ref := range []string{gitResult.CommitRef, gitResult.PushRef, gitResult.PullRequestRef} {
		if strings.TrimSpace(ref) != "" {
			evidenceRefs = append(evidenceRefs, ref)
		}
	}
	return projectautomation.RunStatusCompleted, "", time.Since(started).Milliseconds(), evidenceRefs
}

func isGitOpsPostTaskRecoveryRun(run projectautomation.AutomationRun) bool {
	return strings.TrimSpace(run.SafeSummary) == projectautomation.RunSafeSummaryGitOpsPostTaskRecovery
}

func claimProjectRunsExecuteAndReport(ctx context.Context, client *runnerClient, cfg config.Config, projectIDs []string, agentID string, codexOptions codexLaunchOptions) (int, bool, bool) {
	if len(projectIDs) == 0 {
		fmt.Fprintln(os.Stdout, "no configured projects")
		return 0, true, false
	}
	for _, projectID := range projectIDs {
		status, keepWatching, claimed := claimRunExecuteAndReport(ctx, client, cfg, projectID, agentID, codexOptions)
		if claimed || !keepWatching || status != 0 {
			return status, keepWatching, claimed
		}
	}
	return 0, true, false
}

func gitOpsOptionsForProject(cfg config.Config, projectID string) projectgitops.Options {
	gitops := cfg.GitOperations
	verification := cfg.Verification
	for _, project := range cfg.Projects {
		if project.ID != projectID {
			continue
		}
		if project.GitOperations != nil {
			gitops = mergeGitOperations(gitops, *project.GitOperations)
		}
		if project.Verification != nil {
			verification = *project.Verification
		}
		break
	}
	options := gitOpsOptionsFromConfig(gitops)
	options.Verification = gitOpsVerificationFromConfig(verification)
	return options
}

func mergeGitOperations(base config.GitOperations, override config.GitOperations) config.GitOperations {
	merged := base
	if override.Enabled {
		merged.Enabled = true
	}
	if override.CommitAfterTask {
		merged.CommitAfterTask = true
	}
	if override.PushAfterTask {
		merged.PushAfterTask = true
	}
	if override.DraftPRAfterPush {
		merged.DraftPRAfterPush = true
	}
	if override.RequireCleanBeforeTask {
		merged.RequireCleanBeforeTask = true
	}
	if override.CleanupWorktreeAfterPlanDone {
		merged.CleanupWorktreeAfterPlanDone = true
	}
	if strings.TrimSpace(override.RemoteName) != "" {
		merged.RemoteName = override.RemoteName
	}
	if strings.TrimSpace(override.BranchNamePattern) != "" {
		merged.BranchPrefix = override.BranchPrefix
		merged.BranchNamePattern = override.BranchNamePattern
	} else if strings.TrimSpace(override.BranchPrefix) != "" {
		merged.BranchPrefix = override.BranchPrefix
	}
	if strings.TrimSpace(override.CommitAuthorName) != "" {
		merged.CommitAuthorName = override.CommitAuthorName
	}
	if strings.TrimSpace(override.CommitAuthorEmailEnv) != "" {
		merged.CommitAuthorEmailEnv = override.CommitAuthorEmailEnv
	}
	if strings.TrimSpace(override.CommitAuthorEmailFile) != "" {
		merged.CommitAuthorEmailFile = override.CommitAuthorEmailFile
	}
	if strings.TrimSpace(override.SSHPrivateKeyPath) != "" {
		merged.SSHPrivateKeyPath = override.SSHPrivateKeyPath
	}
	if strings.TrimSpace(override.SSHPublicKeyPath) != "" {
		merged.SSHPublicKeyPath = override.SSHPublicKeyPath
	}
	if strings.TrimSpace(override.SSHKnownHostsPath) != "" {
		merged.SSHKnownHostsPath = override.SSHKnownHostsPath
	}
	if strings.TrimSpace(override.GitHubTokenEnv) != "" {
		merged.GitHubTokenEnv = override.GitHubTokenEnv
	}
	if strings.TrimSpace(override.GitHubTokenFile) != "" {
		merged.GitHubTokenFile = override.GitHubTokenFile
	}
	if strings.TrimSpace(override.GitHubCLIPath) != "" {
		merged.GitHubCLIPath = override.GitHubCLIPath
	}
	merged.Conventions = mergeGitOpsConventions(merged.Conventions, override.Conventions)
	if len(override.DirtyScopeRecovery.AllowedSupportPathspecs) > 0 {
		merged.DirtyScopeRecovery.AllowedSupportPathspecs = append([]string(nil), override.DirtyScopeRecovery.AllowedSupportPathspecs...)
	}
	return merged
}

func mergeGitOpsConventions(base config.GitOpsConventions, override config.GitOpsConventions) config.GitOpsConventions {
	merged := base
	if strings.TrimSpace(override.CommitType) != "" {
		merged.CommitType = override.CommitType
	}
	if strings.TrimSpace(override.CommitScope) != "" {
		merged.CommitScope = override.CommitScope
	}
	if strings.TrimSpace(override.CommitSummaryTemplate) != "" {
		merged.CommitSummaryTemplate = override.CommitSummaryTemplate
	}
	if strings.TrimSpace(override.PullRequestTitleTemplate) != "" {
		merged.PullRequestTitleTemplate = override.PullRequestTitleTemplate
	}
	if strings.TrimSpace(override.WhatChangedTemplate) != "" {
		merged.WhatChangedTemplate = override.WhatChangedTemplate
	}
	if strings.TrimSpace(override.HowVerifiedTemplate) != "" {
		merged.HowVerifiedTemplate = override.HowVerifiedTemplate
	}
	if strings.TrimSpace(override.TestsTemplate) != "" {
		merged.TestsTemplate = override.TestsTemplate
	}
	return merged
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
		BranchNamePattern:            cfg.BranchNamePattern,
		CommitAuthorName:             cfg.CommitAuthorName,
		CommitAuthorEmailEnv:         cfg.CommitAuthorEmailEnv,
		CommitAuthorEmailFile:        cfg.CommitAuthorEmailFile,
		SSHPrivateKeyPath:            cfg.SSHPrivateKeyPath,
		SSHPublicKeyPath:             cfg.SSHPublicKeyPath,
		SSHKnownHostsPath:            cfg.SSHKnownHostsPath,
		GitHubTokenEnv:               cfg.GitHubTokenEnv,
		GitHubTokenFile:              cfg.GitHubTokenFile,
		GitHubCLIPath:                cfg.GitHubCLIPath,
		Conventions: projectgitops.Conventions{
			CommitType:               cfg.Conventions.CommitType,
			CommitScope:              cfg.Conventions.CommitScope,
			CommitSummaryTemplate:    cfg.Conventions.CommitSummaryTemplate,
			PullRequestTitleTemplate: cfg.Conventions.PullRequestTitleTemplate,
			WhatChangedTemplate:      cfg.Conventions.WhatChangedTemplate,
			HowVerifiedTemplate:      cfg.Conventions.HowVerifiedTemplate,
			TestsTemplate:            cfg.Conventions.TestsTemplate,
		},
	}
}

func gitOpsVerificationFromConfig(cfg config.Verification) projectgitops.VerificationProfile {
	generated := make([]projectgitops.GeneratedArtifactVerifier, 0, len(cfg.GeneratedArtifacts))
	for _, item := range cfg.GeneratedArtifacts {
		generated = append(generated, projectgitops.GeneratedArtifactVerifier{
			Paths:            append([]string(nil), item.Paths...),
			Command:          item.Command,
			RequiredBeforePR: item.RequiredBeforePR,
		})
	}
	return projectgitops.VerificationProfile{
		BootstrapCommands:  append([]string(nil), cfg.BootstrapCommands...),
		AlwaysBeforePR:     append([]string(nil), cfg.AlwaysBeforePR...),
		GeneratedArtifacts: generated,
		Env:                cloneStringMap(cfg.Env),
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func verificationInstructionsForProject(cfg config.Config, projectID string) []string {
	verification := cfg.Verification
	for _, project := range cfg.Projects {
		if project.ID == projectID && project.Verification != nil {
			verification = *project.Verification
			break
		}
	}
	instructions := make([]string, 0)
	if len(verification.AlwaysBeforePR) > 0 {
		instructions = append(instructions, "Project verification before PR is enforced by the runner; keep changes compatible with these commands:")
		for _, command := range verification.AlwaysBeforePR {
			instructions = append(instructions, command)
		}
	}
	for _, generated := range verification.GeneratedArtifacts {
		if !generated.RequiredBeforePR {
			continue
		}
		instructions = append(instructions, "Generated artifact verification is required before PR; run or satisfy: "+generated.Command)
		if len(generated.Paths) > 0 {
			instructions = append(instructions, "Generated artifact paths that may need committing: "+strings.Join(generated.Paths, ", "))
		}
	}
	return instructions
}

func gitOpsPostTaskInput(projectID string, workDir string, fallbackOperatorID string, claimed projectautomation.ClaimedRun, taskMetadata runnerWorkTaskMetadata) projectgitops.PostTaskInput {
	taskRef := strings.TrimSpace(taskMetadata.TaskRef)
	if taskRef == "" {
		taskRef = claimed.CodexInput.TaskRef
	}
	taskTitle := strings.TrimSpace(taskMetadata.Title)
	if taskTitle == "" {
		taskTitle = claimed.CodexInput.Title
	}
	return projectgitops.PostTaskInput{
		WorkDir:          workDir,
		ProjectID:        firstNonEmpty(claimed.Run.ProjectID, projectID),
		PlanID:           firstNonEmpty(claimed.Run.PlanID, claimed.CodexInput.PlanID),
		TaskID:           firstNonEmpty(claimed.Run.TaskID, claimed.CodexInput.TaskID),
		TaskRef:          taskRef,
		TaskTitle:        taskTitle,
		AutomationID:     claimed.Run.AutomationID,
		AutomationRunID:  firstNonEmpty(claimed.Run.ID, claimed.CodexInput.AutomationRunID),
		OperatorID:       firstNonEmpty(claimed.Run.AgentID, fallbackOperatorID),
		AllowedPathspecs: gitOpsTaskPathspecs(claimed, taskMetadata),
		ReviewRefs:       append([]string(nil), taskMetadata.ReviewResultRefs...),
		VerifierRefs:     append([]string(nil), taskMetadata.VerifierResultRefs...),
	}
}

func taskHasGovernedCloseout(task runnerWorkTaskMetadata) bool {
	switch strings.TrimSpace(task.Status) {
	case "needs_review", "verifying", "done", "blocked", "failed", "cancelled", "superseded":
		return true
	case "":
		return len(task.EvidenceRefs) > 0 || len(task.ClaimRefs) > 0 || len(task.ReviewResultRefs) > 0 || len(task.VerifierResultRefs) > 0
	default:
		return false
	}
}

type codexRunOutcome struct {
	Status          string
	FailureCategory string
	DurationMS      int64
	LastMessage     string
}

func runCodex(ctx context.Context, claimed projectautomation.ClaimedRun, codexOptions codexLaunchOptions) codexRunOutcome {
	inputPath, cleanup, err := writeCodexInput(claimed.CodexInput)
	if err != nil {
		return codexRunOutcome{Status: projectautomation.RunStatusFailed, FailureCategory: "codex_input_create_failed"}
	}
	defer cleanup()
	outputFile, cleanupOutput, err := createCodexOutputFile()
	if err != nil {
		return codexRunOutcome{Status: projectautomation.RunStatusFailed, FailureCategory: "codex_output_create_failed"}
	}
	defer cleanupOutput()
	timeout := time.Duration(claimed.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	command, err := buildRunnerCodexCommand(inputPath, outputFile, timeout, codexOptions)
	if err != nil {
		return codexRunOutcome{Status: projectautomation.RunStatusFailed, FailureCategory: "codex_command_denied"}
	}
	result, err := projectautomation.RunCodexCommand(ctx, command, 64*1024)
	durationMS := result.Duration.Milliseconds()
	lastMessage := readCodexLastMessage(outputFile, result.Output)
	if err == nil {
		return codexRunOutcome{Status: projectautomation.RunStatusCompleted, DurationMS: durationMS, LastMessage: lastMessage}
	}
	if result.TimedOut {
		return codexRunOutcome{Status: projectautomation.RunStatusTimeout, FailureCategory: "codex_cli_timeout", DurationMS: durationMS, LastMessage: lastMessage}
	}
	if result.SafeFailureCategory != "" {
		return codexRunOutcome{Status: projectautomation.RunStatusFailed, FailureCategory: result.SafeFailureCategory, DurationMS: durationMS, LastMessage: lastMessage}
	}
	return codexRunOutcome{Status: projectautomation.RunStatusFailed, FailureCategory: "codex_cli_failed", DurationMS: durationMS, LastMessage: lastMessage}
}

func createCodexOutputFile() (string, func(), error) {
	dir, err := os.MkdirTemp("", "mivia-codex-output-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "last-message.txt")
	return path, cleanup, nil
}

func readCodexLastMessage(path string, fallback string) string {
	if strings.TrimSpace(path) != "" {
		if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
			return strings.TrimSpace(string(data))
		}
	}
	return strings.TrimSpace(fallback)
}

func buildRunnerCodexCommand(inputPath string, outputPath string, timeout time.Duration, codexOptions codexLaunchOptions) (projectautomation.CodexCommand, error) {
	launcher := strings.TrimSpace(codexOptions.Launcher)
	if launcher == "" {
		launcher = "direct"
	}
	binaryPath := strings.TrimSpace(codexOptions.Path)
	if binaryPath == "" {
		binaryPath = "codex"
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
		if strings.TrimSpace(codexOptions.OutputSchemaPath) != "" {
			args = append(args, "--output-schema", strings.TrimSpace(codexOptions.OutputSchemaPath))
		}
		if strings.TrimSpace(outputPath) != "" {
			args = append(args, "--output-last-message", strings.TrimSpace(outputPath))
		}
		command.Args = append(args, "--cd", strings.TrimSpace(codexOptions.WorkDir), "-")
		command.Dir = strings.TrimSpace(codexOptions.WorkDir)
	} else {
		args := []string{"exec"}
		args = appendCodexExecutionOptions(args, codexOptions)
		if strings.TrimSpace(codexOptions.OutputSchemaPath) != "" {
			args = append(args, "--output-schema", strings.TrimSpace(codexOptions.OutputSchemaPath))
		}
		if strings.TrimSpace(outputPath) != "" {
			args = append(args, "--output-last-message", strings.TrimSpace(outputPath))
		}
		command.Args = append(args, "-")
	}
	return command, nil
}

func prepareRunWorktree(ctx context.Context, workDir string) error {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return nil
	}
	if !filepath.IsAbs(workDir) || strings.ContainsAny(workDir, "\x00\r\n") {
		return fmt.Errorf("%w: workdir must be absolute and safe", projectautomation.ErrInvalidInput)
	}
	if err := runGitForWorktreePrep(ctx, workDir, "-c", "safe.directory="+workDir, "rev-parse", "--show-toplevel"); err != nil {
		return err
	}
	return runGitForWorktreePrep(ctx, workDir, "config", "--global", "--add", "safe.directory", workDir)
}

func runGitForWorktreePrep(ctx context.Context, workDir string, args ...string) error {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = workDir
	if output, err := command.CombinedOutput(); err != nil {
		summary := strings.TrimSpace(string(output))
		if summary == "" {
			summary = err.Error()
		}
		return fmt.Errorf("%w: git worktree preparation failed: %s", projectautomation.ErrInvalidInput, summary)
	}
	return nil
}

func dedicatedWorktreeRunnerInstructions(runWorkDir string, baseWorkDir string) []string {
	runWorkDir = filepath.Clean(strings.TrimSpace(runWorkDir))
	baseWorkDir = filepath.Clean(strings.TrimSpace(baseWorkDir))
	if runWorkDir == "" || baseWorkDir == "" || runWorkDir == "." || baseWorkDir == "." || runWorkDir == baseWorkDir {
		return nil
	}
	separator := string(os.PathSeparator)
	if !strings.Contains(runWorkDir, separator+".mivia-worktrees"+separator) {
		return nil
	}
	return []string{
		"dedicated worktree run: the runner launched you in the claimed worktree. Treat the current process workspace as the only writable repository checkout.",
		"Do not use projects.workspace.file_edit, projects.workspace.file_create, or projects.workspace.file_delete for source changes in this run; those MCP workspace edit tools target the canonical base checkout unless an explicit dedicated-worktree edit path is provided. Use local file editing in the current process workspace, then use MCP only for bounded evidence and task status updates.",
	}
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
	baseURL                string
	http                   *http.Client
	runnerID               string
	heartbeatInterval      time.Duration
	projectCleanupInterval time.Duration
	projectCleanupMu       sync.Mutex
	projectCleanupLast     map[string]time.Time
}

type projectListResponse struct {
	Projects []projectListItem `json:"projects"`
}

type projectListItem struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type runnerWorkPlanListResponse struct {
	WorkPlans     []runnerWorkPlan `json:"work_plans"`
	NextPageToken string           `json:"next_page_token,omitempty"`
}

type runnerWorkPlan struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	Status         string `json:"status"`
	IsolationMode  string `json:"isolation_mode"`
	GitBaseRef     string `json:"git_base_ref"`
	GitBranchRef   string `json:"git_branch_ref"`
	GitWorktreeRef string `json:"git_worktree_ref"`
}

type runnerWorkTaskMetadata struct {
	ID                 string   `json:"id"`
	TaskRef            string   `json:"task_ref,omitempty"`
	Title              string   `json:"title,omitempty"`
	Status             string   `json:"status,omitempty"`
	BlockedReason      string   `json:"blocked_reason,omitempty"`
	FilesToEdit        []string `json:"files_to_edit,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	ClaimRefs          []string `json:"claim_refs,omitempty"`
	ReviewResultRefs   []string `json:"review_result_refs,omitempty"`
	VerifierResultRefs []string `json:"verifier_result_refs,omitempty"`
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
	input := projectautomation.ClaimNextRunInput{AgentID: agentID, RunnerKind: projectautomation.RunnerKindCodexCLI, RunnerID: client.runnerID}
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

func (client *runnerClient) completeAttempt(ctx context.Context, projectID string, claimed projectautomation.AutomationRun, input projectautomation.CompleteAttemptInput) (projectautomation.AutomationRun, error) {
	input.ClaimID = claimed.ClaimID
	input.RunnerID = client.runnerID
	var run projectautomation.AutomationRun
	_, err := client.post(ctx, fmt.Sprintf("/api/v1/projects/%s/automation-runs/%s/attempt-result", url.PathEscape(projectID), url.PathEscape(claimed.ID)), input, &run)
	if err != nil {
		return run, err
	}
	if strings.TrimSpace(claimed.ClaimID) == "" {
		return run, nil
	}
	durable, err := client.getRun(ctx, projectID, claimed.ID)
	if err != nil {
		return run, err
	}
	if durable.Status != run.Status || durable.FailureCategory != run.FailureCategory {
		return run, fmt.Errorf("%w: automation run %s report mismatch returned=%s durable=%s", projectautomation.ErrInvalidInput, claimed.ID, run.Status, durable.Status)
	}
	return durable, nil
}

func (client *runnerClient) getRun(ctx context.Context, projectID string, runID string) (projectautomation.AutomationRun, error) {
	var run projectautomation.AutomationRun
	_, err := client.get(ctx, fmt.Sprintf("/api/v1/projects/%s/automation-runs/%s", url.PathEscape(projectID), url.PathEscape(runID)), &run)
	return run, err
}

func (client *runnerClient) heartbeatRun(ctx context.Context, projectID string, run projectautomation.AutomationRun) error {
	if strings.TrimSpace(run.ClaimID) == "" {
		return nil
	}
	input := projectautomation.HeartbeatRunInput{ClaimID: run.ClaimID, RunnerID: client.runnerID}
	var updated projectautomation.AutomationRun
	_, err := client.post(ctx, fmt.Sprintf("/api/v1/projects/%s/automation-runs/%s/heartbeat", url.PathEscape(projectID), url.PathEscape(run.ID)), input, &updated)
	return err
}

func (client *runnerClient) startHeartbeat(ctx context.Context, projectID string, run projectautomation.AutomationRun) func() {
	if strings.TrimSpace(run.ClaimID) == "" {
		return func() {}
	}
	interval := client.heartbeatInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	hbCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				_ = client.heartbeatRun(hbCtx, projectID, run)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
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
	target, err := dedicatedWorktreePath(baseWorkDir, projectID, plan.GitWorktreeRef)
	if err != nil {
		return "", err
	}
	if worktreePathReady(target) {
		return target, nil
	}
	if strings.TrimSpace(plan.GitBranchRef) == "" {
		return "", fmt.Errorf("%w: dedicated worktree plan requires git_branch_ref", projectautomation.ErrInvalidInput)
	}
	if err := client.createDedicatedWorktree(ctx, projectID, plan); err != nil {
		return "", err
	}
	if !worktreePathReady(target) {
		return "", fmt.Errorf("%w: dedicated worktree was not created", projectautomation.ErrInvalidInput)
	}
	return target, nil
}

func (client *runnerClient) resolveTaskScopedRunWorkDir(ctx context.Context, projectID string, planID string, taskID string, baseWorkDir string) (string, error) {
	baseWorkDir = strings.TrimSpace(baseWorkDir)
	planID = strings.TrimSpace(planID)
	taskID = strings.TrimSpace(taskID)
	if planID == "" || taskID == "" {
		return "", fmt.Errorf("%w: task scoped worktree requires plan and task", projectautomation.ErrInvalidInput)
	}
	plan, err := client.getWorkPlan(ctx, projectID, planID)
	if err != nil {
		return "", err
	}
	switch strings.TrimSpace(plan.IsolationMode) {
	case "dedicated_worktree", "shared":
	default:
		return "", fmt.Errorf("%w: task scoped worktree requires dedicated or legacy shared plan", projectautomation.ErrInvalidInput)
	}
	if strings.TrimSpace(plan.GitWorktreeRef) == "" {
		return "", fmt.Errorf("%w: task scoped worktree requires git_worktree_ref", projectautomation.ErrInvalidInput)
	}
	taskToken := taskScopedRefToken(taskID)
	if taskToken == "" {
		return "", fmt.Errorf("%w: unsafe task scoped worktree token", projectautomation.ErrInvalidInput)
	}
	scopedPlan := plan
	scopedPlan.GitWorktreeRef = appendRefToken(plan.GitWorktreeRef, taskToken)
	scopedPlan.GitBranchRef = appendRefToken(firstNonEmpty(plan.GitBranchRef, "mivia/task"), taskToken)
	target, err := dedicatedWorktreePath(baseWorkDir, projectID, scopedPlan.GitWorktreeRef)
	if err != nil {
		return "", err
	}
	if worktreePathReady(target) {
		return target, nil
	}
	if err := client.createDedicatedWorktree(ctx, projectID, scopedPlan); err != nil {
		return "", err
	}
	if !worktreePathReady(target) {
		return "", fmt.Errorf("%w: task scoped worktree was not created", projectautomation.ErrInvalidInput)
	}
	return target, nil
}

func (client *runnerClient) createDedicatedWorktree(ctx context.Context, projectID string, plan runnerWorkPlan) error {
	input := struct {
		WorktreeRef string `json:"worktree_ref"`
		BranchRef   string `json:"branch_ref"`
		BaseRef     string `json:"base_ref,omitempty"`
	}{
		WorktreeRef: strings.TrimSpace(plan.GitWorktreeRef),
		BranchRef:   strings.TrimSpace(plan.GitBranchRef),
		BaseRef:     strings.TrimSpace(plan.GitBaseRef),
	}
	_, err := client.post(ctx, fmt.Sprintf("/api/v1/projects/%s/workspace/git/worktrees", url.PathEscape(projectID)), input, nil)
	return err
}

func worktreePathReady(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	gitPath := filepath.Join(path, ".git")
	info, err = os.Stat(gitPath)
	if err != nil {
		return false
	}
	if info.IsDir() {
		return true
	}
	data, err := os.ReadFile(gitPath)
	if err != nil {
		return false
	}
	const prefix = "gitdir: "
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitDir == "" {
		return false
	}
	resolved := filepath.Clean(gitDir)
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Clean(filepath.Join(path, filepath.FromSlash(gitDir)))
	}
	for _, required := range []string{"HEAD", "commondir", "gitdir"} {
		if _, err := os.Stat(filepath.Join(resolved, required)); err != nil {
			return false
		}
	}
	return true
}

func (client *runnerClient) getWorkPlan(ctx context.Context, projectID string, planID string) (runnerWorkPlan, error) {
	var plan runnerWorkPlan
	_, err := client.get(ctx, fmt.Sprintf("/api/v1/projects/%s/work-plans/%s", url.PathEscape(projectID), url.PathEscape(planID)), &plan)
	return plan, err
}

func (client *runnerClient) listWorkPlans(ctx context.Context, projectID string) ([]runnerWorkPlan, error) {
	var plans []runnerWorkPlan
	pageToken := ""
	seenPageTokens := map[string]struct{}{}
	for {
		query := url.Values{}
		query.Set("page_size", "100")
		if pageToken != "" {
			if _, seen := seenPageTokens[pageToken]; seen {
				return nil, fmt.Errorf("%w: repeated work plan page token", projectautomation.ErrInvalidInput)
			}
			seenPageTokens[pageToken] = struct{}{}
			query.Set("page_token", pageToken)
		}
		var output runnerWorkPlanListResponse
		_, err := client.get(ctx, fmt.Sprintf("/api/v1/projects/%s/work-plans?%s", url.PathEscape(projectID), query.Encode()), &output)
		if err != nil {
			return nil, err
		}
		plans = append(plans, output.WorkPlans...)
		pageToken = strings.TrimSpace(output.NextPageToken)
		if pageToken == "" {
			return plans, nil
		}
	}
}

func cleanupTerminalProjectWorktrees(ctx context.Context, client *runnerClient, options projectgitops.Options, projectID string, baseWorkDir string) {
	if !options.CleanupWorktreeAfterPlanDone || strings.TrimSpace(baseWorkDir) == "" {
		return
	}
	plans, err := client.listWorkPlans(ctx, projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "terminal worktree cleanup skipped for project %s: %v\n", projectID, err)
		return
	}
	protectedPaths := map[string]struct{}{}
	for _, plan := range plans {
		if strings.TrimSpace(plan.IsolationMode) != "dedicated_worktree" || strings.TrimSpace(plan.GitWorktreeRef) == "" {
			continue
		}
		expected, err := dedicatedWorktreePath(baseWorkDir, projectID, plan.GitWorktreeRef)
		if err != nil {
			fmt.Fprintf(os.Stderr, "worktree cleanup skipped for plan %s: %v\n", plan.ID, err)
			continue
		}
		if !isTerminalPlanStatus(plan.Status) {
			protectedPaths[filepath.Clean(expected)] = struct{}{}
			continue
		}
		if err := removeDedicatedWorktree(ctx, baseWorkDir, expected); err != nil {
			fmt.Fprintf(os.Stderr, "worktree cleanup skipped for plan %s: %v\n", plan.ID, err)
		}
	}
	cleanupOrphanProjectWorktrees(ctx, projectID, baseWorkDir, protectedPaths)
}

func cleanupOrphanProjectWorktrees(ctx context.Context, projectID string, baseWorkDir string, protectedPaths map[string]struct{}) {
	projectSegment, err := safeProjectWorktreeSegment(projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "orphan worktree cleanup skipped for project %s: %v\n", projectID, err)
		return
	}
	root := filepath.Join(filepath.Clean(strings.TrimSpace(baseWorkDir)), ".mivia-worktrees", projectSegment)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "orphan worktree cleanup skipped for project %s: %v\n", projectID, err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Clean(filepath.Join(root, entry.Name()))
		if _, protected := protectedPaths[path]; protected {
			continue
		}
		if err := removeDedicatedWorktree(ctx, baseWorkDir, path); err != nil {
			fmt.Fprintf(os.Stderr, "orphan worktree cleanup skipped for %s: %v\n", path, err)
		}
	}
}

func cleanupTerminalPlanWorktree(ctx context.Context, client *runnerClient, options projectgitops.Options, projectID string, planID string, baseWorkDir string, runWorkDir string) {
	if !options.CleanupWorktreeAfterPlanDone {
		return
	}
	if strings.TrimSpace(planID) == "" || strings.TrimSpace(baseWorkDir) == "" || strings.TrimSpace(runWorkDir) == "" {
		return
	}
	plan, err := client.getWorkPlan(ctx, projectID, planID)
	if err != nil || !isTerminalPlanStatus(plan.Status) {
		return
	}
	if strings.TrimSpace(plan.IsolationMode) != "dedicated_worktree" || strings.TrimSpace(plan.GitWorktreeRef) == "" {
		return
	}
	expected, err := dedicatedWorktreePath(baseWorkDir, projectID, plan.GitWorktreeRef)
	if err != nil {
		return
	}
	if filepath.Clean(expected) != filepath.Clean(runWorkDir) {
		return
	}
	if err := removeDedicatedWorktree(ctx, baseWorkDir, runWorkDir); err != nil {
		fmt.Fprintf(os.Stderr, "worktree cleanup skipped for plan %s: %v\n", plan.ID, err)
	}
}

func isTerminalPlanStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "done", "failed", "cancelled", "superseded":
		return true
	default:
		return false
	}
}

var errDirtyDedicatedWorktree = errors.New("dirty dedicated worktree")

func removeDedicatedWorktree(ctx context.Context, baseWorkDir string, runWorkDir string) error {
	baseWorkDir = filepath.Clean(strings.TrimSpace(baseWorkDir))
	runWorkDir = filepath.Clean(strings.TrimSpace(runWorkDir))
	if baseWorkDir == "" || runWorkDir == "" || !filepath.IsAbs(baseWorkDir) || !filepath.IsAbs(runWorkDir) {
		return fmt.Errorf("%w: cleanup requires absolute worktree paths", projectautomation.ErrInvalidInput)
	}
	root := filepath.Clean(filepath.Join(baseWorkDir, ".mivia-worktrees"))
	rel, err := filepath.Rel(root, runWorkDir)
	if err != nil {
		return err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: cleanup path escapes mivia worktree root", projectautomation.ErrInvalidInput)
	}
	if _, err := os.Stat(runWorkDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	registered := isRegisteredGitWorktree(ctx, baseWorkDir, runWorkDir)
	if registered {
		if dirty, err := gitWorktreeDirty(ctx, baseWorkDir, runWorkDir); err != nil {
			return err
		} else if dirty {
			return fmt.Errorf("%w: %s", errDirtyDedicatedWorktree, runWorkDir)
		}
	}
	remove := exec.CommandContext(ctx, "git", gitArgsWithSafeDirectories([]string{baseWorkDir, runWorkDir}, "-C", baseWorkDir, "worktree", "remove", "--force", runWorkDir)...)
	var removeStderr bytes.Buffer
	remove.Stderr = &removeStderr
	if err := remove.Run(); err != nil {
		if _, statErr := os.Stat(filepath.Join(runWorkDir, ".git")); statErr == nil && registered {
			return fmt.Errorf("git worktree remove failed: %w: %s", err, strings.TrimSpace(removeStderr.String()))
		}
		if err := os.RemoveAll(runWorkDir); err != nil {
			return err
		}
	}
	prune := exec.CommandContext(ctx, "git", gitArgsWithSafeDirectories([]string{baseWorkDir}, "-C", baseWorkDir, "worktree", "prune", "--expire", "now")...)
	_ = prune.Run()
	return nil
}

func gitWorktreeDirty(ctx context.Context, baseWorkDir string, runWorkDir string) (bool, error) {
	status := exec.CommandContext(ctx, "git", gitArgsWithSafeDirectories([]string{baseWorkDir, runWorkDir}, "-C", runWorkDir, "status", "--porcelain")...)
	var stderr bytes.Buffer
	status.Stderr = &stderr
	output, err := status.Output()
	if err != nil {
		return false, fmt.Errorf("git worktree status failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(output)) != "", nil
}

func isRegisteredGitWorktree(ctx context.Context, baseWorkDir string, runWorkDir string) bool {
	list := exec.CommandContext(ctx, "git", gitArgsWithSafeDirectories([]string{baseWorkDir, runWorkDir}, "-C", baseWorkDir, "worktree", "list", "--porcelain")...)
	output, err := list.Output()
	if err != nil {
		return false
	}
	cleanRunWorkDir := filepath.Clean(runWorkDir)
	for _, line := range strings.Split(string(output), "\n") {
		path, ok := strings.CutPrefix(line, "worktree ")
		if !ok {
			continue
		}
		if filepath.Clean(strings.TrimSpace(path)) == cleanRunWorkDir {
			return true
		}
	}
	return false
}

func gitArgsWithSafeDirectories(safeDirectories []string, args ...string) []string {
	out := make([]string, 0, len(args)+(len(safeDirectories)*2))
	seen := map[string]struct{}{}
	for _, dir := range safeDirectories {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, "-c", "safe.directory="+clean)
	}
	out = append(out, args...)
	return out
}

func (client *runnerClient) getWorkTaskMetadata(ctx context.Context, projectID string, taskID string) (runnerWorkTaskMetadata, error) {
	var task runnerWorkTaskMetadata
	if strings.TrimSpace(taskID) == "" {
		return task, nil
	}
	_, err := client.get(ctx, fmt.Sprintf("/api/v1/projects/%s/work-tasks/%s", url.PathEscape(projectID), url.PathEscape(taskID)), &task)
	return task, err
}

func (client *runnerClient) closeoutMetadataOnlyTask(ctx context.Context, projectID string, claimed projectautomation.ClaimedRun, reviewTask bool) error {
	taskID := strings.TrimSpace(claimed.Run.TaskID)
	runID := strings.TrimSpace(claimed.Run.ID)
	if taskID == "" || runID == "" {
		return fmt.Errorf("%w: metadata-only closeout requires run and task refs", projectautomation.ErrInvalidInput)
	}
	verifierRef := "verifier:" + runID
	pathBase := fmt.Sprintf("/api/v1/projects/%s/work-tasks/%s", url.PathEscape(projectID), url.PathEscape(taskID))
	verifierInput := struct {
		Ref             string `json:"ref"`
		AttachedByRunID string `json:"attached_by_run_id,omitempty"`
		TraceID         string `json:"trace_id,omitempty"`
		Note            string `json:"note,omitempty"`
	}{
		Ref:             verifierRef,
		AttachedByRunID: runID,
		TraceID:         firstNonEmpty(claimed.Run.TraceID, runID),
		Note:            "metadata-only automation completed without runner-level failure",
	}
	if _, err := client.post(ctx, pathBase+"/verifier-results", verifierInput, nil); err != nil {
		return err
	}
	statusInput := struct {
		RunID          string `json:"run_id,omitempty"`
		TraceID        string `json:"trace_id,omitempty"`
		Status         string `json:"status"`
		SafeNextAction string `json:"safe_next_action"`
	}{
		RunID:          runID,
		TraceID:        firstNonEmpty(claimed.Run.TraceID, runID),
		Status:         "verifying",
		SafeNextAction: "complete read-only reviewer gate after verifier metadata is attached",
	}
	if _, err := client.post(ctx, pathBase+"/status", statusInput, nil); err != nil {
		return err
	}
	completeInput := struct {
		RunID              string   `json:"run_id,omitempty"`
		TraceID            string   `json:"trace_id,omitempty"`
		Outcome            string   `json:"outcome"`
		SafeNextAction     string   `json:"safe_next_action"`
		VerifierResultRefs []string `json:"verifier_result_refs"`
		ReviewExemptReason string   `json:"review_exempt_reason"`
		EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	}{
		RunID:              runID,
		TraceID:            firstNonEmpty(claimed.Run.TraceID, runID),
		Outcome:            metadataOnlyCloseoutOutcome(reviewTask),
		SafeNextAction:     "continue governed automation after metadata-only task closeout",
		VerifierResultRefs: []string{verifierRef},
		ReviewExemptReason: metadataOnlyReviewExemption(reviewTask),
		EvidenceRefs:       []string{"automation_run:" + runID},
	}
	if _, err := client.post(ctx, pathBase+"/complete", completeInput, nil); err != nil {
		return err
	}
	return nil
}

func shouldAutoCloseoutMetadataOnlyTask(readOnlyReviewRun bool, task runnerWorkTaskMetadata) bool {
	if taskRequiresExplicitGovernedCloseout(task) {
		return false
	}
	if readOnlyReviewRun {
		return true
	}
	return len(task.FilesToEdit) == 0
}

func taskRequiresExplicitGovernedCloseout(task runnerWorkTaskMetadata) bool {
	switch strings.TrimSpace(task.TaskRef) {
	case "decompose-work-plan",
		"mark-ready-after-review",
		"select-ready-tasks",
		"run-implementation-batch",
		"review-implementation-batch",
		"orchestrator-verification",
		"pr-gitops-readiness",
		"collect-final-scope",
		"validate-regression-and-downstream",
		"run-final-verification",
		"final-pr-readiness",
		"smoke-draft-pr":
		return true
	default:
		return false
	}
}

func shouldUseCodexOutputSchemaForGovernedCloseout(task runnerWorkTaskMetadata) bool {
	return false
}

func metadataOnlyCloseoutOutcome(reviewTask bool) string {
	if reviewTask {
		return "read-only review automation completed; no reusable project knowledge because this is reviewer gate closeout metadata"
	}
	return "metadata-only automation completed; no reusable project knowledge because no repository files were changed"
}

func metadataOnlyReviewExemption(reviewTask bool) string {
	if reviewTask {
		return "read-only reviewer task is itself the independent review gate; nested self-review is prohibited"
	}
	return "metadata-only task changed no repository files; independent diff review is not applicable"
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

func appendRefToken(ref string, token string) string {
	ref = strings.TrimSpace(ref)
	token = safeWorktreeDirName(token)
	if ref == "" || token == "" {
		return ref
	}
	if strings.Contains(ref, token) {
		return ref
	}
	suffix := "-" + token
	const maxWorktreeRefLength = 512
	maxBase := maxWorktreeRefLength - len(suffix)
	if maxBase < 1 {
		if len(token) > maxWorktreeRefLength {
			return token[:maxWorktreeRefLength]
		}
		return token
	}
	if len(ref) > maxBase {
		ref = ref[:maxBase]
	}
	ref = strings.TrimRight(ref, ".:/@+-")
	if ref == "" {
		return token
	}
	return ref + suffix
}

func taskScopedRefToken(taskID string) string {
	safe := safeWorktreeDirName(taskID)
	if safe == "" {
		return ""
	}
	for strings.Contains(safe, "..") {
		safe = strings.ReplaceAll(safe, "..", ".")
	}
	safe = strings.Trim(safe, ".-_")
	if safe == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(taskID)))
	hash := hex.EncodeToString(sum[:])[:12]
	maxSafe := 80 - len(hash) - 1
	if maxSafe < 1 {
		return hash
	}
	if len(safe) > maxSafe {
		safe = strings.TrimRight(safe[:maxSafe], ".-_")
	}
	if safe == "" {
		return hash
	}
	return safe + "-" + hash
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
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
