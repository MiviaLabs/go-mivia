package projectgitops

import "context"

type Options struct {
	Enabled                      bool
	CommitAfterTask              bool
	PushAfterTask                bool
	DraftPRAfterPush             bool
	RequireCleanBeforeTask       bool
	CleanupWorktreeAfterPlanDone bool
	RemoteName                   string
	BranchPrefix                 string
	CommitAuthorName             string
	CommitAuthorEmailEnv         string
	CommitAuthorEmailFile        string
	SSHPrivateKeyPath            string
	SSHPublicKeyPath             string
	SSHKnownHostsPath            string
	GitHubTokenEnv               string
	GitHubTokenFile              string
	GitHubCLIPath                string
}

type Command struct {
	Path string
	Args []string
	Dir  string
	Env  []string
}

type CommandRunner interface {
	Run(context.Context, Command) (CommandResult, error)
}

type CommandResult struct {
	Stdout string
	Stderr string
}

type PostTaskInput struct {
	WorkDir          string
	PlanID           string
	TaskID           string
	AutomationRunID  string
	CommitSubject    string
	CommitBody       string
	AllowedPathspecs []string
}

type PostTaskResult struct {
	Skipped        bool
	NoChanges      bool
	CommitRef      string
	PushRef        string
	PullRequestRef string
	EvidenceRefs   []string
}
