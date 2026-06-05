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
	BranchNamePattern            string
	CommitAuthorName             string
	CommitAuthorEmailEnv         string
	CommitAuthorEmailFile        string
	SSHPrivateKeyPath            string
	SSHPublicKeyPath             string
	SSHKnownHostsPath            string
	GitHubTokenEnv               string
	GitHubTokenFile              string
	GitHubCLIPath                string
	Conventions                  Conventions
	Verification                 VerificationProfile
}

type Conventions struct {
	CommitType               string
	CommitScope              string
	CommitSummaryTemplate    string
	PullRequestTitleTemplate string
	WhatChangedTemplate      string
	HowVerifiedTemplate      string
	TestsTemplate            string
}

type VerificationProfile struct {
	BootstrapCommands  []string
	AlwaysBeforePR     []string
	GeneratedArtifacts []GeneratedArtifactVerifier
	Env                map[string]string
}

type GeneratedArtifactVerifier struct {
	Paths            []string
	Command          string
	RequiredBeforePR bool
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
	ProjectID        string
	PlanID           string
	TaskID           string
	TaskRef          string
	TaskTitle        string
	BranchName       string
	AutomationID     string
	AutomationRunID  string
	OperatorID       string
	CommitBody       string
	AllowedPathspecs []string
	ReviewRefs       []string
	VerifierRefs     []string
	TestResults      []string
}

type PostTaskResult struct {
	Skipped        bool
	NoChanges      bool
	CommitRef      string
	PushRef        string
	PullRequestRef string
	EvidenceRefs   []string
}

type RenderedOutput struct {
	CommitSubject    string
	CommitBody       string
	PullRequestTitle string
	PullRequestBody  string
}
