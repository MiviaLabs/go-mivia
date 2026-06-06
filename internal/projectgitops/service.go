package projectgitops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	ErrInvalidInput       = errors.New("invalid git operations input")
	ErrBranchPolicy       = errors.New("git operations branch policy failed")
	ErrCommandFailed      = errors.New("git operations command failed")
	ErrDirtyWorktree      = errors.New("git operations dirty worktree")
	ErrDirtyWorktreeScope = errors.New("git operations dirty worktree outside task scope")
	ErrVerificationFailed = errors.New("git operations verification failed")
)

var safeRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

type Service struct {
	options Options
	runner  CommandRunner
}

func New(options Options) *Service {
	return NewWithRunner(options, execCommandRunner{})
}

func NewWithRunner(options Options, runner CommandRunner) *Service {
	if strings.TrimSpace(options.RemoteName) == "" {
		options.RemoteName = "origin"
	}
	if strings.TrimSpace(options.GitHubCLIPath) == "" {
		options.GitHubCLIPath = "gh"
	}
	if strings.TrimSpace(options.CommitAuthorName) == "" {
		options.CommitAuthorName = "Mivia Automation"
	}
	options.Conventions = normalizeConventions(options.Conventions)
	return &Service{options: options, runner: runner}
}

func (svc *Service) PreTask(ctx context.Context, workDir string) error {
	if !svc.options.Enabled || !svc.options.CommitAfterTask || !svc.options.RequireCleanBeforeTask {
		return nil
	}
	status, err := svc.preTaskStatus(ctx, workDir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return ErrDirtyWorktree
	}
	return nil
}

func (svc *Service) PreTaskWithinScope(ctx context.Context, workDir string, allowedPathspecs []string) error {
	if !svc.options.Enabled || !svc.options.CommitAfterTask || !svc.options.RequireCleanBeforeTask {
		return nil
	}
	status, err := svc.preTaskStatus(ctx, workDir)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) == "" {
		return nil
	}
	allowed := sanitizePathspecs(append(allowedPathspecs, svc.generatedArtifactPathspecs()...))
	if len(allowed) == 0 || changedPathspecsOutsideAllowed(status, allowed) {
		return ErrDirtyWorktreeScope
	}
	return nil
}

func (svc *Service) preTaskStatus(ctx context.Context, workDir string) (string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" || !filepath.IsAbs(workDir) {
		return "", fmt.Errorf("%w: workdir must be absolute", ErrInvalidInput)
	}
	if err := svc.ensureSafeDirectory(ctx, workDir); err != nil {
		return "", err
	}
	status, err := svc.git(ctx, workDir, nil, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	return status.Stdout, nil
}

func (svc *Service) PostTask(ctx context.Context, input PostTaskInput) (PostTaskResult, error) {
	if !svc.options.Enabled || !svc.options.CommitAfterTask {
		return PostTaskResult{Skipped: true}, nil
	}
	workDir := strings.TrimSpace(input.WorkDir)
	if workDir == "" || !filepath.IsAbs(workDir) {
		return PostTaskResult{}, fmt.Errorf("%w: workdir must be absolute", ErrInvalidInput)
	}
	if err := svc.validatePushConfig(); err != nil {
		return PostTaskResult{}, err
	}

	if err := svc.ensureSafeDirectory(ctx, workDir); err != nil {
		return PostTaskResult{}, err
	}
	status, err := svc.git(ctx, workDir, nil, "status", "--porcelain")
	if err != nil {
		return PostTaskResult{}, err
	}
	if strings.TrimSpace(status.Stdout) == "" {
		return PostTaskResult{NoChanges: true, EvidenceRefs: []string{"git-no-changes"}}, nil
	}

	verificationRefs, verificationTests, err := svc.runVerification(ctx, workDir)
	if err != nil {
		return PostTaskResult{}, err
	}
	if len(verificationTests) > 0 {
		input.TestResults = append(input.TestResults, verificationTests...)
	}
	if len(verificationRefs) > 0 {
		status, err = svc.git(ctx, workDir, nil, "status", "--porcelain")
		if err != nil {
			return PostTaskResult{}, err
		}
		if strings.TrimSpace(status.Stdout) == "" {
			result := PostTaskResult{NoChanges: true, EvidenceRefs: []string{"git-no-changes"}}
			result.EvidenceRefs = append(result.EvidenceRefs, verificationRefs...)
			return result, nil
		}
	}

	allowedPathspecs := sanitizePathspecs(append(input.AllowedPathspecs, svc.generatedArtifactPathspecs()...))
	if len(allowedPathspecs) == 0 {
		return PostTaskResult{}, fmt.Errorf("%w: no safe task pathspecs", ErrInvalidInput)
	}
	if changedPathspecsOutsideAllowed(status.Stdout, allowedPathspecs) {
		return PostTaskResult{}, ErrDirtyWorktreeScope
	}
	changedPathspecs := changedPathspecsWithinAllowed(status.Stdout, allowedPathspecs)
	if len(changedPathspecs) == 0 {
		return PostTaskResult{}, fmt.Errorf("%w: no changed files matched safe task pathspecs", ErrInvalidInput)
	}
	if strings.TrimSpace(input.BranchName) == "" {
		if branch, err := svc.currentBranch(ctx, workDir); err == nil {
			input.BranchName = branch
		}
	}
	branch, err := svc.normalizeBranchForPolicy(ctx, workDir, input)
	if err != nil {
		return PostTaskResult{}, err
	}
	input.BranchName = branch
	rendered, err := Render(input, svc.options.Conventions)
	if err != nil {
		return PostTaskResult{}, err
	}
	addArgs := append([]string{"add", "--"}, changedPathspecs...)
	if _, err := svc.git(ctx, workDir, nil, addArgs...); err != nil {
		return PostTaskResult{}, err
	}
	if _, err := svc.git(ctx, workDir, nil, "diff", "--cached", "--check"); err != nil {
		return PostTaskResult{}, err
	}
	email, err := svc.authorEmail()
	if err != nil {
		return PostTaskResult{}, err
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + svc.options.CommitAuthorName,
		"GIT_COMMITTER_NAME=" + svc.options.CommitAuthorName,
	}
	if email != "" {
		env = append(env, "GIT_AUTHOR_EMAIL="+email, "GIT_COMMITTER_EMAIL="+email)
	}
	if _, err := svc.git(ctx, workDir, env, "commit", "--no-verify", "-m", rendered.CommitSubject+"\n\n"+rendered.CommitBody); err != nil {
		return PostTaskResult{}, err
	}
	sha, err := svc.git(ctx, workDir, nil, "rev-parse", "--short=12", "HEAD")
	if err != nil {
		return PostTaskResult{}, err
	}
	result := PostTaskResult{
		CommitRef:    "git-commit-" + strings.TrimSpace(sha.Stdout),
		EvidenceRefs: append([]string{"git-commit-created"}, verificationRefs...),
	}
	if svc.options.PushAfterTask {
		branch := strings.TrimSpace(input.BranchName)
		if branch == "" {
			var err error
			branch, err = svc.currentBranch(ctx, workDir)
			if err != nil {
				return PostTaskResult{}, err
			}
		}
		if err := svc.validateBranchPolicy(branch); err != nil {
			return PostTaskResult{}, err
		}
		if _, err := svc.git(ctx, workDir, svc.gitSSHEnv(), "push", "--no-verify", svc.options.RemoteName, "HEAD:"+branch); err != nil {
			return PostTaskResult{}, err
		}
		result.PushRef = "git-push-" + safeHash(branch)
		result.EvidenceRefs = append(result.EvidenceRefs, "git-push-completed")
		if svc.options.DraftPRAfterPush {
			prRef, err := svc.ensureDraftPR(ctx, workDir, rendered)
			if err != nil {
				return PostTaskResult{}, err
			}
			result.PullRequestRef = prRef
			result.EvidenceRefs = append(result.EvidenceRefs, "draft-pr-ready")
		}
	}
	return result, nil
}

func FailureCategory(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrDirtyWorktree):
		return "gitops_dirty_worktree"
	case errors.Is(err, ErrDirtyWorktreeScope):
		return "gitops_dirty_worktree_scope"
	case errors.Is(err, ErrVerificationFailed):
		return "gitops_verification_failed"
	case errors.Is(err, ErrBranchPolicy):
		return "gitops_branch_policy_failed"
	case errors.Is(err, ErrInvalidInput):
		return "gitops_invalid_input"
	case errors.Is(err, ErrCommandFailed):
		return "gitops_command_failed"
	default:
		return "gitops_post_task_failed"
	}
}

func (svc *Service) runVerification(ctx context.Context, workDir string) ([]string, []string, error) {
	if len(svc.options.Verification.BootstrapCommands) == 0 &&
		len(svc.options.Verification.AlwaysBeforePR) == 0 &&
		len(svc.options.Verification.GeneratedArtifacts) == 0 {
		return nil, nil, nil
	}
	refs := make([]string, 0)
	tests := make([]string, 0)
	for _, command := range svc.options.Verification.BootstrapCommands {
		if err := svc.runVerifierCommand(ctx, workDir, command); err != nil {
			return nil, nil, err
		}
		refs = append(refs, "verify-bootstrap-"+safeHash(command))
		tests = append(tests, safeTestResult(command, "passed"))
	}
	for _, command := range svc.options.Verification.AlwaysBeforePR {
		if err := svc.runVerifierCommand(ctx, workDir, command); err != nil {
			return nil, nil, err
		}
		refs = append(refs, "verify-project-"+safeHash(command))
		tests = append(tests, safeTestResult(command, "passed"))
	}
	for _, generated := range svc.options.Verification.GeneratedArtifacts {
		if !generated.RequiredBeforePR {
			continue
		}
		if err := svc.runVerifierCommand(ctx, workDir, generated.Command); err != nil {
			return nil, nil, err
		}
		refs = append(refs, "verify-generated-"+safeHash(generated.Command))
		tests = append(tests, safeTestResult(generated.Command, "passed"))
	}
	if len(refs) > 0 {
		refs = append(refs, "project-verification-passed")
	}
	return refs, tests, nil
}

func (svc *Service) runVerifierCommand(ctx context.Context, workDir string, command string) error {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\x00\r\n") {
		return fmt.Errorf("%w: unsafe verifier command", ErrInvalidInput)
	}
	if _, err := svc.run(ctx, Command{Path: "sh", Args: []string{"-lc", command}, Dir: workDir, Env: verifierEnv(svc.options.Verification.Env)}); err != nil {
		return fmt.Errorf("%w: %s", ErrVerificationFailed, safeHash(command))
	}
	return nil
}

func verifierEnv(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

func (svc *Service) generatedArtifactPathspecs() []string {
	out := make([]string, 0)
	for _, generated := range svc.options.Verification.GeneratedArtifacts {
		if !generated.RequiredBeforePR {
			continue
		}
		out = append(out, generated.Paths...)
	}
	return out
}

func safeTestResult(command string, status string) string {
	summary := strings.TrimSpace(command)
	if len(summary) > 120 {
		summary = summary[:120]
	}
	return summary + ": " + status
}

func (svc *Service) validatePushConfig() error {
	if !svc.options.PushAfterTask {
		return nil
	}
	if strings.TrimSpace(svc.options.SSHPrivateKeyPath) == "" || strings.TrimSpace(svc.options.SSHKnownHostsPath) == "" {
		return fmt.Errorf("%w: ssh key and known hosts are required for push", ErrInvalidInput)
	}
	for name, value := range map[string]string{
		"ssh key":         svc.options.SSHPrivateKeyPath,
		"ssh known hosts": svc.options.SSHKnownHostsPath,
	} {
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("%w: %s path must be absolute and safe", ErrInvalidInput, name)
		}
	}
	if svc.options.DraftPRAfterPush && strings.TrimSpace(svc.options.GitHubTokenEnv) == "" && strings.TrimSpace(svc.options.GitHubTokenFile) == "" {
		return fmt.Errorf("%w: github token reference required for draft PR", ErrInvalidInput)
	}
	return nil
}

func (svc *Service) currentBranch(ctx context.Context, workDir string) (string, error) {
	out, err := svc.git(ctx, workDir, nil, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out.Stdout)
	if err := validateSafeRef("branch", branch); err != nil {
		return "", err
	}
	return branch, nil
}

func (svc *Service) validateBranchPolicy(branch string) error {
	prefix := strings.TrimSpace(svc.options.BranchPrefix)
	if prefix != "" && !strings.HasPrefix(branch, prefix) {
		return fmt.Errorf("%w: %w: branch %q does not match required prefix %q", ErrInvalidInput, ErrBranchPolicy, branch, prefix)
	}
	pattern := strings.TrimSpace(svc.options.BranchNamePattern)
	if pattern == "" {
		return nil
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("%w: invalid branch pattern", ErrInvalidInput)
	}
	if !compiled.MatchString(branch) {
		return fmt.Errorf("%w: %w: branch %q does not match required pattern", ErrInvalidInput, ErrBranchPolicy, branch)
	}
	return nil
}

func (svc *Service) normalizeBranchForPolicy(ctx context.Context, workDir string, input PostTaskInput) (string, error) {
	branch := strings.TrimSpace(input.BranchName)
	if branch == "" {
		var err error
		branch, err = svc.currentBranch(ctx, workDir)
		if err != nil {
			return "", err
		}
	}
	if svc.validateBranchPolicy(branch) == nil {
		return branch, nil
	}
	derived := svc.derivePolicyBranch(input)
	if derived == "" || derived == branch {
		return branch, svc.validateBranchPolicy(branch)
	}
	if err := svc.validateBranchPolicy(derived); err != nil {
		return branch, svc.validateBranchPolicy(branch)
	}
	if _, err := svc.git(ctx, workDir, nil, "checkout", "-B", derived); err != nil {
		return "", err
	}
	return derived, nil
}

func (svc *Service) derivePolicyBranch(input PostTaskInput) string {
	projectKey := branchPatternProjectKey(svc.options.BranchNamePattern)
	if projectKey == "" {
		projectKey = ticketProjectKey(input.TaskRef, input.TaskTitle, input.BranchName)
	}
	if projectKey == "" {
		return ""
	}
	kind := branchKind(svc.options.BranchNamePattern, svc.options.Conventions.CommitType)
	slug := safeBranchToken(firstNonEmpty(input.TaskRef, input.TaskTitle, input.AutomationRunID))
	if slug == "" {
		slug = "automation-task"
	}
	return kind + "-" + projectKey + "-0000-" + slug
}

func branchKind(pattern string, preferred string) string {
	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if preferred != "" && strings.Contains(pattern, preferred) {
		return preferred
	}
	if strings.Contains(pattern, "fix") {
		return "fix"
	}
	for _, candidate := range []string{"chore", "docs", "feat", "refactor"} {
		if strings.Contains(pattern, candidate) {
			return candidate
		}
	}
	if preferred != "" {
		return preferred
	}
	return "chore"
}

func branchPatternProjectKey(pattern string) string {
	match := regexp.MustCompile(`[A-Z][A-Z0-9]+-\[0-9\]`).FindString(pattern)
	if match == "" {
		return ""
	}
	return strings.TrimSuffix(match, "-[0-9]")
}

func ticketProjectKey(values ...string) string {
	for _, value := range values {
		match := regexp.MustCompile(`\b([A-Z][A-Z0-9]+)-[0-9]+\b`).FindStringSubmatch(strings.TrimSpace(value))
		if len(match) == 2 {
			return match[1]
		}
	}
	return ""
}

func safeBranchToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (svc *Service) ensureDraftPR(ctx context.Context, workDir string, rendered RenderedOutput) (string, error) {
	env := svc.githubEnv()
	view, err := svc.run(ctx, Command{Path: svc.options.GitHubCLIPath, Args: []string{"pr", "view", "--json", "number", "--jq", ".number"}, Dir: workDir, Env: env})
	if err == nil && strings.TrimSpace(view.Stdout) != "" {
		if _, err := svc.run(ctx, Command{Path: svc.options.GitHubCLIPath, Args: []string{"pr", "edit", "--title", rendered.PullRequestTitle, "--body", rendered.PullRequestBody}, Dir: workDir, Env: env}); err != nil {
			return "", err
		}
		return "github-pr-" + strings.TrimSpace(view.Stdout), nil
	}
	create, err := svc.run(ctx, Command{Path: svc.options.GitHubCLIPath, Args: []string{"pr", "create", "--draft", "--title", rendered.PullRequestTitle, "--body", rendered.PullRequestBody}, Dir: workDir, Env: env})
	if err != nil {
		return "", err
	}
	return "github-pr-" + safeHash(create.Stdout), nil
}

func (svc *Service) git(ctx context.Context, dir string, env []string, args ...string) (CommandResult, error) {
	if safeDir := safeGitDirectoryArg(dir); safeDir != "" {
		args = append([]string{"-c", "safe.directory=" + safeDir}, args...)
	}
	return svc.run(ctx, Command{Path: "git", Args: args, Dir: dir, Env: env})
}

func (svc *Service) ensureSafeDirectory(ctx context.Context, workDir string) error {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" || !filepath.IsAbs(workDir) || strings.ContainsAny(workDir, "\x00\r\n") {
		return fmt.Errorf("%w: workdir must be absolute and safe", ErrInvalidInput)
	}
	if _, err := svc.git(ctx, workDir, nil, "rev-parse", "--show-toplevel"); err == nil {
		return nil
	}
	home := filepath.Join(os.TempDir(), "mivia-gitops-home-"+safeHash(workDir))
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	env := []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config")}
	_, err := svc.git(ctx, workDir, env, "config", "--global", "--add", "safe.directory", workDir)
	return err
}

func safeGitDirectoryArg(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || !filepath.IsAbs(dir) || strings.ContainsAny(dir, "\x00\r\n") {
		return ""
	}
	return dir
}

func (svc *Service) run(ctx context.Context, command Command) (CommandResult, error) {
	result, err := svc.runner.Run(ctx, command)
	if err != nil {
		return result, fmt.Errorf("%w: %s", ErrCommandFailed, command.Path)
	}
	return result, nil
}

func (svc *Service) authorEmail() (string, error) {
	if envName := strings.TrimSpace(svc.options.CommitAuthorEmailEnv); envName != "" {
		return strings.TrimSpace(os.Getenv(envName)), nil
	}
	if path := strings.TrimSpace(svc.options.CommitAuthorEmailFile); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return "", nil
}

func (svc *Service) gitSSHEnv() []string {
	command := "ssh -i " + shellQuote(svc.options.SSHPrivateKeyPath) + " -o IdentitiesOnly=yes -o UserKnownHostsFile=" + shellQuote(svc.options.SSHKnownHostsPath)
	return []string{"GIT_SSH_COMMAND=" + command}
}

func (svc *Service) githubEnv() []string {
	if envName := strings.TrimSpace(svc.options.GitHubTokenEnv); envName != "" {
		if token := strings.TrimSpace(os.Getenv(envName)); token != "" {
			return []string{"GH_TOKEN=" + token}
		}
		return nil
	}
	if path := strings.TrimSpace(svc.options.GitHubTokenFile); path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			return []string{"GH_TOKEN=" + strings.TrimSpace(string(data))}
		}
	}
	return nil
}

func sanitizePathspecs(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, "\x00\r\n") {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func changedPathspecsWithinAllowed(status string, allowed []string) []string {
	changed := changedPathsFromStatus(status)
	out := make([]string, 0, len(changed))
	seen := make(map[string]struct{}, len(changed))
	for _, path := range changed {
		if !isSafeRelativePathspec(path) {
			continue
		}
		for _, allow := range allowed {
			if pathMatchesAllowedPathspec(path, allow) {
				if _, ok := seen[path]; !ok {
					seen[path] = struct{}{}
					out = append(out, path)
				}
				break
			}
		}
	}
	return out
}

func changedPathspecsOutsideAllowed(status string, allowed []string) bool {
	for _, path := range changedPathsFromStatus(status) {
		if !isSafeRelativePathspec(path) {
			return true
		}
		matched := false
		for _, allow := range allowed {
			if pathMatchesAllowedPathspec(path, allow) {
				matched = true
				break
			}
		}
		if !matched {
			return true
		}
	}
	return false
}

func changedPathsFromStatus(status string) []string {
	lines := strings.Split(strings.ReplaceAll(status, "\x00", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			source := strings.TrimSpace(path[:idx])
			destination := strings.TrimSpace(path[idx+4:])
			if source != "" {
				out = append(out, source)
			}
			if destination != "" {
				out = append(out, destination)
			}
			continue
		}
		if path != "" {
			out = append(out, path)
		}
	}
	return out
}

func pathMatchesAllowedPathspec(path, allow string) bool {
	allow = strings.TrimSuffix(strings.TrimSpace(allow), "/")
	if allow == "" {
		return false
	}
	return path == allow || strings.HasPrefix(path, allow+"/")
}

func isSafeRelativePathspec(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	return true
}

func validateSafeRef(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !safeRefPattern.MatchString(trimmed) || strings.Contains(trimmed, "..") || strings.Contains(trimmed, "//") {
		return fmt.Errorf("%w: unsafe %s", ErrInvalidInput, name)
	}
	return nil
}

func safeHash(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])[:12]
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = append(os.Environ(), command.Env...)
	out, err := cmd.Output()
	result := CommandResult{Stdout: string(out)}
	if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
		result.Stderr = string(exitErr.Stderr)
	}
	return result, err
}
