package projectgitops

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultCommitType               = "chore"
	defaultCommitSummaryTemplate    = "complete {{work_task_id}}"
	defaultPullRequestTitleTemplate = "{{commit_subject}}"
	defaultWhatChangedTemplate      = "Completed automation work task {{work_task_id}} for project {{project_id}}."
	defaultHowVerifiedTemplate      = "Project ID: {{project_id}}\nWork Plan ID: {{work_plan_id}}\nWork Task ID: {{work_task_id}}\nAutomation ID: {{automation_id}}\nAutomation Run ID: {{automation_run_id}}\nOperator ID: {{operator_id}}\nReview refs: {{review_refs}}\nVerifier refs: {{verifier_refs}}"
	defaultTestsTemplate            = "{{test_results}}"
)

var (
	conventionalSubjectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(\([A-Za-z0-9][A-Za-z0-9._/-]*\))?!?: [^\r\n]+$`)
	metadataRefPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)
)

var allowedTemplatePlaceholders = map[string]bool{
	"project_id":        true,
	"work_plan_id":      true,
	"work_task_id":      true,
	"work_task_ref":     true,
	"work_task_title":   true,
	"branch_name":       true,
	"ticket_ref":        true,
	"automation_id":     true,
	"automation_run_id": true,
	"operator_id":       true,
	"review_refs":       true,
	"verifier_refs":     true,
	"test_results":      true,
	"commit_subject":    true,
}

func DefaultConventions() Conventions {
	return Conventions{
		CommitType:               defaultCommitType,
		CommitSummaryTemplate:    defaultCommitSummaryTemplate,
		PullRequestTitleTemplate: defaultPullRequestTitleTemplate,
		WhatChangedTemplate:      defaultWhatChangedTemplate,
		HowVerifiedTemplate:      defaultHowVerifiedTemplate,
		TestsTemplate:            defaultTestsTemplate,
	}
}

func Render(input PostTaskInput, conventions Conventions) (RenderedOutput, error) {
	conventions = normalizeConventions(conventions)
	if err := validateConventions(conventions); err != nil {
		return RenderedOutput{}, err
	}
	if err := validateRenderInput(input); err != nil {
		return RenderedOutput{}, err
	}

	values := templateValues(input)
	summary, err := renderSingleLineTemplate("commit_summary_template", conventions.CommitSummaryTemplate, values)
	if err != nil {
		return RenderedOutput{}, err
	}
	if summary == "" {
		return RenderedOutput{}, fmt.Errorf("%w: git convention commit_summary_template rendered empty output", ErrInvalidInput)
	}
	subject := conventionalSubject(conventions, summary)
	if !conventionalSubjectPattern.MatchString(subject) {
		return RenderedOutput{}, fmt.Errorf("%w: git convention rendered invalid Conventional Commit subject %q", ErrInvalidInput, subject)
	}
	values["commit_subject"] = subject

	prTitle, err := renderSingleLineTemplate("pull_request_title_template", conventions.PullRequestTitleTemplate, values)
	if err != nil {
		return RenderedOutput{}, err
	}
	if !conventionalSubjectPattern.MatchString(prTitle) {
		return RenderedOutput{}, fmt.Errorf("%w: git convention rendered invalid Conventional Commit PR title %q", ErrInvalidInput, prTitle)
	}

	whatChanged, err := renderBodyTemplate("what_changed_template", conventions.WhatChangedTemplate, values)
	if err != nil {
		return RenderedOutput{}, err
	}
	howVerified, err := renderBodyTemplate("how_verified_template", conventions.HowVerifiedTemplate, values)
	if err != nil {
		return RenderedOutput{}, err
	}
	tests, err := renderBodyTemplate("tests_template", conventions.TestsTemplate, values)
	if err != nil {
		return RenderedOutput{}, err
	}

	return RenderedOutput{
		CommitSubject:    subject,
		CommitBody:       commitBody(input, values, whatChanged, howVerified, tests),
		PullRequestTitle: prTitle,
		PullRequestBody:  pullRequestBody(whatChanged, howVerified, tests),
	}, nil
}

func normalizeConventions(conventions Conventions) Conventions {
	defaults := DefaultConventions()
	if strings.TrimSpace(conventions.CommitType) == "" {
		conventions.CommitType = defaults.CommitType
	} else {
		conventions.CommitType = strings.TrimSpace(conventions.CommitType)
	}
	conventions.CommitScope = strings.TrimSpace(conventions.CommitScope)
	if strings.TrimSpace(conventions.CommitSummaryTemplate) == "" {
		conventions.CommitSummaryTemplate = defaults.CommitSummaryTemplate
	} else {
		conventions.CommitSummaryTemplate = strings.TrimSpace(conventions.CommitSummaryTemplate)
	}
	if strings.TrimSpace(conventions.PullRequestTitleTemplate) == "" {
		conventions.PullRequestTitleTemplate = defaults.PullRequestTitleTemplate
	} else {
		conventions.PullRequestTitleTemplate = strings.TrimSpace(conventions.PullRequestTitleTemplate)
	}
	if strings.TrimSpace(conventions.WhatChangedTemplate) == "" {
		conventions.WhatChangedTemplate = defaults.WhatChangedTemplate
	}
	if strings.TrimSpace(conventions.HowVerifiedTemplate) == "" {
		conventions.HowVerifiedTemplate = defaults.HowVerifiedTemplate
	}
	if strings.TrimSpace(conventions.TestsTemplate) == "" {
		conventions.TestsTemplate = defaults.TestsTemplate
	}
	return conventions
}

func validateConventions(conventions Conventions) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(conventions.CommitType) {
		return fmt.Errorf("%w: git convention commit_type must be a safe Conventional Commit type", ErrInvalidInput)
	}
	if conventions.CommitScope != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`).MatchString(conventions.CommitScope) {
		return fmt.Errorf("%w: git convention commit_scope must be a safe Conventional Commit scope", ErrInvalidInput)
	}
	for _, item := range []struct {
		name         string
		value        string
		allowNewline bool
	}{
		{name: "commit_summary_template", value: conventions.CommitSummaryTemplate},
		{name: "pull_request_title_template", value: conventions.PullRequestTitleTemplate},
		{name: "what_changed_template", value: conventions.WhatChangedTemplate, allowNewline: true},
		{name: "how_verified_template", value: conventions.HowVerifiedTemplate, allowNewline: true},
		{name: "tests_template", value: conventions.TestsTemplate, allowNewline: true},
	} {
		if err := validateTemplate(item.name, item.value, item.allowNewline); err != nil {
			return err
		}
	}
	return nil
}

func validateTemplate(name, value string, allowNewline bool) error {
	if strings.Contains(value, "\x00") || (!allowNewline && strings.ContainsAny(value, "\r\n")) {
		return fmt.Errorf("%w: git convention %s must not contain unsafe control characters", ErrInvalidInput, name)
	}
	for offset := 0; ; {
		start := strings.Index(value[offset:], "{{")
		if start < 0 {
			break
		}
		start += offset
		end := strings.Index(value[start+2:], "}}")
		if end < 0 {
			return fmt.Errorf("%w: git convention %s has an unclosed placeholder", ErrInvalidInput, name)
		}
		end += start + 2
		placeholder := strings.TrimSpace(value[start+2 : end])
		if !allowedTemplatePlaceholders[placeholder] {
			return fmt.Errorf("%w: git convention %s uses unknown placeholder %q", ErrInvalidInput, name, placeholder)
		}
		offset = end + 2
	}
	return nil
}

func validateRenderInput(input PostTaskInput) error {
	for name, value := range map[string]string{
		"project id":        input.ProjectID,
		"plan id":           input.PlanID,
		"task id":           input.TaskID,
		"automation id":     input.AutomationID,
		"automation run id": input.AutomationRunID,
		"operator id":       input.OperatorID,
	} {
		if err := validateSafeRef(name, value); err != nil {
			return err
		}
	}
	for name, value := range map[string]string{
		"task ref":    input.TaskRef,
		"task title":  input.TaskTitle,
		"branch name": input.BranchName,
	} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, err := safeMetadataLine(name, value, gitOpsMetadataLineMax); err != nil {
			return err
		}
	}
	for _, refs := range []struct {
		name   string
		values []string
	}{
		{name: "review refs", values: input.ReviewRefs},
		{name: "verifier refs", values: input.VerifierRefs},
	} {
		if _, err := safeMetadataRefs(refs.name, refs.values); err != nil {
			return err
		}
	}
	if _, err := safeMetadataLines("test results", input.TestResults, gitOpsTestResultLineMax); err != nil {
		return err
	}
	if strings.Contains(input.CommitBody, "\x00") {
		return fmt.Errorf("%w: commit body must not contain unsafe control characters", ErrInvalidInput)
	}
	return nil
}

func templateValues(input PostTaskInput) map[string]string {
	reviewRefs, _ := safeMetadataRefs("review refs", input.ReviewRefs)
	verifierRefs, _ := safeMetadataRefs("verifier refs", input.VerifierRefs)
	testResults, _ := safeMetadataLines("test results", input.TestResults, gitOpsTestResultLineMax)
	taskRef, _ := safeMetadataLine("task ref", input.TaskRef, gitOpsMetadataLineMax)
	taskTitle, _ := safeMetadataLine("task title", input.TaskTitle, gitOpsMetadataLineMax)
	branchName, _ := safeMetadataLine("branch name", input.BranchName, gitOpsMetadataLineMax)
	ticketRef := extractTicketRef(branchName, taskRef, taskTitle)

	return map[string]string{
		"project_id":        strings.TrimSpace(input.ProjectID),
		"work_plan_id":      strings.TrimSpace(input.PlanID),
		"work_task_id":      strings.TrimSpace(input.TaskID),
		"work_task_ref":     valueOrUnavailable(taskRef),
		"work_task_title":   valueOrUnavailable(taskTitle),
		"branch_name":       valueOrUnavailable(branchName),
		"ticket_ref":        valueOrUnavailable(ticketRef),
		"automation_id":     strings.TrimSpace(input.AutomationID),
		"automation_run_id": strings.TrimSpace(input.AutomationRunID),
		"operator_id":       strings.TrimSpace(input.OperatorID),
		"review_refs":       refsValue(reviewRefs),
		"verifier_refs":     refsValue(verifierRefs),
		"test_results":      testsValue(testResults),
	}
}

var ticketRefPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+\b`)

const (
	gitOpsMetadataLineMax   = 2048
	gitOpsTestResultLineMax = 4096
)

func extractTicketRef(values ...string) string {
	for _, value := range values {
		if match := ticketRefPattern.FindString(strings.TrimSpace(value)); match != "" {
			return match
		}
	}
	return ""
}

func renderSingleLineTemplate(name, template string, values map[string]string) (string, error) {
	rendered := strings.TrimSpace(expandTemplate(template, values))
	rendered = strings.Join(strings.Fields(rendered), " ")
	if strings.ContainsAny(rendered, "\r\n\x00") {
		return "", fmt.Errorf("%w: git convention %s rendered unsafe control characters", ErrInvalidInput, name)
	}
	return rendered, nil
}

func renderBodyTemplate(name, template string, values map[string]string) (string, error) {
	rendered := strings.TrimSpace(expandTemplate(template, values))
	if strings.Contains(rendered, "\x00") {
		return "", fmt.Errorf("%w: git convention %s rendered unsafe control characters", ErrInvalidInput, name)
	}
	if rendered == "" {
		return "", fmt.Errorf("%w: git convention %s rendered empty output", ErrInvalidInput, name)
	}
	return rendered, nil
}

func expandTemplate(template string, values map[string]string) string {
	var builder strings.Builder
	for offset := 0; offset < len(template); {
		start := strings.Index(template[offset:], "{{")
		if start < 0 {
			builder.WriteString(template[offset:])
			break
		}
		start += offset
		builder.WriteString(template[offset:start])
		end := strings.Index(template[start+2:], "}}")
		if end < 0 {
			builder.WriteString(template[start:])
			break
		}
		end += start + 2
		placeholder := strings.TrimSpace(template[start+2 : end])
		builder.WriteString(values[placeholder])
		offset = end + 2
	}
	return builder.String()
}

func conventionalSubject(conventions Conventions, summary string) string {
	scope := ""
	if conventions.CommitScope != "" {
		scope = "(" + conventions.CommitScope + ")"
	}
	return conventions.CommitType + scope + ": " + summary
}

func commitBody(input PostTaskInput, values map[string]string, whatChanged, howVerified, tests string) string {
	var builder strings.Builder
	if body := strings.TrimSpace(input.CommitBody); body != "" {
		builder.WriteString(body)
		builder.WriteString("\n\n")
	}
	builder.WriteString("What changed:\n")
	builder.WriteString(whatChanged)
	builder.WriteString("\n\nHow verified:\n")
	builder.WriteString(howVerified)
	builder.WriteString("\n\nTests:\n")
	builder.WriteString(tests)
	builder.WriteString("\n\nGitOps metadata:\n")
	for _, key := range []string{"project_id", "work_plan_id", "work_task_id", "branch_name", "ticket_ref", "automation_id", "automation_run_id", "operator_id", "review_refs", "verifier_refs"} {
		builder.WriteString(metadataLabel(key))
		builder.WriteString(": ")
		builder.WriteString(values[key])
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}

func pullRequestBody(whatChanged, howVerified, tests string) string {
	return "## What changed\n\n" + whatChanged + "\n\n## How verified\n\n" + howVerified + "\n\n## Tests\n\n" + tests + "\n"
}

func metadataLabel(key string) string {
	switch key {
	case "project_id":
		return "Project ID"
	case "work_plan_id":
		return "Work Plan ID"
	case "work_task_id":
		return "Work Task ID"
	case "branch_name":
		return "Branch"
	case "ticket_ref":
		return "Ticket"
	case "automation_id":
		return "Automation ID"
	case "automation_run_id":
		return "Automation Run ID"
	case "operator_id":
		return "Operator ID"
	case "review_refs":
		return "Review refs"
	case "verifier_refs":
		return "Verifier refs"
	default:
		return key
	}
}

func safeMetadataRefs(name string, values []string) ([]string, error) {
	if len(values) > 100 {
		return nil, fmt.Errorf("%w: %s has too many values", ErrInvalidInput, name)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if strings.Contains(trimmed, "..") || strings.Contains(trimmed, "\\") || strings.HasPrefix(trimmed, "/") || !metadataRefPattern.MatchString(trimmed) {
			return nil, fmt.Errorf("%w: unsafe %s", ErrInvalidInput, name)
		}
		if len(trimmed) > gitOpsMetadataLineMax {
			return nil, fmt.Errorf("%w: %s is too long", ErrInvalidInput, name)
		}
		if !seen[trimmed] {
			seen[trimmed] = true
			out = append(out, trimmed)
		}
	}
	sort.Strings(out)
	return out, nil
}

func safeMetadataLines(name string, values []string, max int) ([]string, error) {
	if len(values) > 100 {
		return nil, fmt.Errorf("%w: %s has too many values", ErrInvalidInput, name)
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		line, err := safeMetadataLine(name, value, max)
		if err != nil {
			return nil, err
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out, nil
}

func safeMetadataLine(name, value string, max int) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > max {
		return "", fmt.Errorf("%w: %s is too long", ErrInvalidInput, name)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("%w: %s must be a safe single-line value", ErrInvalidInput, name)
	}
	return value, nil
}

func refsValue(values []string) string {
	if len(values) == 0 {
		return "not available"
	}
	return strings.Join(values, ", ")
}

func testsValue(values []string) string {
	if len(values) == 0 {
		return "Not reported by automation runner; orchestrator verification pending."
	}
	return strings.Join(values, "\n")
}

func valueOrUnavailable(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unavailable"
	}
	return value
}
