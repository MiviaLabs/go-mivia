package projectworkflowchain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var ErrInvalidInput = errors.New("invalid project workflow chain input")

var (
	refPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)
	jiraKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]+-[0-9]+$`)
	emailPattern   = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phonePattern   = regexp.MustCompile(`\+?[0-9][0-9 .()\-]{7,}[0-9]`)
)

func validateConfig(cfg Config) error {
	if _, err := safeRef(cfg.ProjectID, "project_id"); err != nil {
		return err
	}
	if _, err := safeRef(cfg.ChainRef, "chain_ref"); err != nil {
		return err
	}
	inputKind := firstNonEmpty(cfg.InputKind, InputKindSafeRef)
	switch inputKind {
	case InputKindJiraIssueKey, InputKindObjectiveText, InputKindSafeRef:
	default:
		return fmt.Errorf("%w: input_kind must be %q, %q, or %q", ErrInvalidInput, InputKindJiraIssueKey, InputKindObjectiveText, InputKindSafeRef)
	}
	if err := validateInputPattern(cfg.InputPattern); err != nil {
		return err
	}
	switch strings.TrimSpace(cfg.ContextProvider) {
	case "", ContextProviderJira, ContextProviderConfluence, ContextProviderIndexedRepo:
	default:
		return fmt.Errorf("%w: context_provider is unsupported", ErrInvalidInput)
	}
	switch strings.TrimSpace(cfg.ContextMode) {
	case "", ContextModeLocalIngested, ContextModeIndexed:
	default:
		return fmt.Errorf("%w: context_mode is unsupported", ErrInvalidInput)
	}
	if cfg.ContextProvider == ContextProviderJira && cfg.ContextMode != "" && cfg.ContextMode != ContextModeLocalIngested {
		return fmt.Errorf("%w: jira context requires local_ingested mode", ErrInvalidInput)
	}
	if cfg.GitOpsMode != "" && cfg.GitOpsMode != GitOpsModeDraftPRAfterValidation {
		return fmt.Errorf("%w: gitops_mode is unsupported", ErrInvalidInput)
	}
	if cfg.GitOpsMode == GitOpsModeDraftPRAfterValidation && !cfg.GitOpsEnabled {
		return fmt.Errorf("%w: draft_pr_after_post_validation requires project GitOps config", ErrInvalidInput)
	}
	if len(cfg.Stages) == 0 {
		return fmt.Errorf("%w: at least one stage is required", ErrInvalidInput)
	}
	seen := map[string]StageConfig{}
	hasPostValidation := false
	for i, stage := range cfg.Stages {
		if _, err := safeRef(stage.StageRef, fmt.Sprintf("stages[%d].stage_ref", i)); err != nil {
			return err
		}
		if _, ok := seen[stage.StageRef]; ok {
			return fmt.Errorf("%w: duplicate stage_ref %s", ErrInvalidInput, stage.StageRef)
		}
		seen[stage.StageRef] = stage
		if _, err := safeRef(stage.WorkflowRef, fmt.Sprintf("stages[%d].workflow_ref", i)); err != nil {
			return err
		}
		trigger := firstNonEmpty(stage.Trigger, TriggerAfterStageReviewPassed)
		if i == 0 && trigger != TriggerOnChainStart {
			return fmt.Errorf("%w: first stage trigger must be on_chain_start", ErrInvalidInput)
		}
		if trigger != TriggerOnChainStart && trigger != TriggerAfterStageReviewPassed {
			return fmt.Errorf("%w: unsupported stage trigger %s", ErrInvalidInput, trigger)
		}
		if stage.AutomationRefTemplate != "" {
			if _, err := safeText(stage.AutomationRefTemplate, fmt.Sprintf("stages[%d].automation_ref_template", i), 300); err != nil {
				return err
			}
		}
		switch strings.TrimSpace(stage.RequiredStatusBeforeNext) {
		case "", StageStatusCompleted, "done":
		default:
			return fmt.Errorf("%w: unsupported required_status_before_next", ErrInvalidInput)
		}
		if stage.StageRef == "post-validation" || stage.StageRef == "post_validation" {
			hasPostValidation = true
		}
	}
	for _, stage := range cfg.Stages {
		for _, dep := range stage.DependsOn {
			dep, err := safeRef(dep, "depends_on")
			if err != nil {
				return err
			}
			if _, ok := seen[dep]; !ok {
				return fmt.Errorf("%w: missing stage dependency %s", ErrInvalidInput, dep)
			}
		}
	}
	if hasDependencyCycle(cfg.Stages) {
		return fmt.Errorf("%w: workflow chain stage dependency cycle", ErrInvalidInput)
	}
	if cfg.GitOpsMode == GitOpsModeDraftPRAfterValidation && !hasPostValidation {
		return fmt.Errorf("%w: draft PR GitOps requires a post-validation stage", ErrInvalidInput)
	}
	return nil
}

func validateInputPattern(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil
	}
	if len(pattern) > 128 {
		return fmt.Errorf("%w: input_pattern is too long", ErrInvalidInput)
	}
	if !strings.HasPrefix(pattern, "^") || !strings.HasSuffix(pattern, "$") {
		return fmt.Errorf("%w: input_pattern must be anchored", ErrInvalidInput)
	}
	for _, unsafe := range []string{".*", ".+", "(?s", "[\\s\\S]", "(?="} {
		if strings.Contains(pattern, unsafe) {
			return fmt.Errorf("%w: input_pattern must be bounded", ErrInvalidInput)
		}
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("%w: input_pattern must compile", ErrInvalidInput)
	}
	return nil
}

func hasDependencyCycle(stages []StageConfig) bool {
	byRef := map[string]StageConfig{}
	for _, stage := range stages {
		byRef[stage.StageRef] = stage
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(ref string) bool {
		if visited[ref] {
			return false
		}
		if visiting[ref] {
			return true
		}
		visiting[ref] = true
		for _, dep := range byRef[ref].DependsOn {
			if visit(dep) {
				return true
			}
		}
		visiting[ref] = false
		visited[ref] = true
		return false
	}
	for _, stage := range stages {
		if visit(stage.StageRef) {
			return true
		}
	}
	return false
}

func normalizeInputRef(cfg Config, inputText string) (string, error) {
	value := strings.TrimSpace(inputText)
	if value == "" {
		return "", fmt.Errorf("%w: input_text is required", ErrInvalidInput)
	}
	if containsSensitive(value) {
		return "", fmt.Errorf("%w: input_text contains unsafe content", ErrInvalidInput)
	}
	inputKind := firstNonEmpty(cfg.InputKind, InputKindSafeRef)
	if inputKind == InputKindJiraIssueKey && strings.HasPrefix(strings.ToLower(value), "jira:") {
		value = strings.TrimSpace(value[len("jira:"):])
		if value == "" {
			return "", fmt.Errorf("%w: input_text must be a Jira issue key", ErrInvalidInput)
		}
	}
	if pattern := strings.TrimSpace(cfg.InputPattern); pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return "", fmt.Errorf("%w: input_pattern must compile", ErrInvalidInput)
		}
		if !compiled.MatchString(value) {
			return "", fmt.Errorf("%w: input_text does not match configured input_pattern", ErrInvalidInput)
		}
	}
	switch inputKind {
	case InputKindJiraIssueKey:
		value = strings.ToUpper(value)
		if !jiraKeyPattern.MatchString(value) {
			return "", fmt.Errorf("%w: input_text must be a Jira issue key", ErrInvalidInput)
		}
		return "jira:" + value, nil
	case InputKindSafeRef:
		ref, err := safeRef(value, "input_text")
		if err != nil {
			return "", err
		}
		return "input:" + ref, nil
	case InputKindObjectiveText:
		ref, err := safeRef(value, "input_text")
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(ref, "objective:") {
			return "", fmt.Errorf("%w: input_text must be a normalized objective ref", ErrInvalidInput)
		}
		return ref, nil
	default:
		return "", fmt.Errorf("%w: unsupported input_kind", ErrInvalidInput)
	}
}

func safeRef(value string, name string) (string, error) {
	value = strings.TrimSpace(value)
	if !refPattern.MatchString(value) {
		return "", fmt.Errorf("%w: %s must be a safe ref", ErrInvalidInput, name)
	}
	return value, nil
}

func safeOptionalRef(value string, name string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	return safeRef(value, name)
}

func safeText(value string, name string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > max {
		return "", fmt.Errorf("%w: %s is too long", ErrInvalidInput, name)
	}
	if containsSensitive(value) {
		return "", fmt.Errorf("%w: %s contains unsafe content", ErrInvalidInput, name)
	}
	return value, nil
}

func containsSensitive(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "token=") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "raw prompt") {
		return true
	}
	return emailPattern.MatchString(value) || phonePattern.MatchString(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
