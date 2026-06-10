package projectdurable

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// objectiveFixture is a distinctive neutral sentence used to prove the raw
// objective never leaks into serialized output or error messages.
const objectiveFixture = "create one markdown file describing the pilot fixture scenario"

var objectiveRefPattern = regexp.MustCompile(`^objective:[0-9a-f]{12}$`)

func TestNormalizeIntakeJiraAccepted(t *testing.T) {
	for _, key := range []string{"PROJ-1044", "jira:PROJ-1044", "  jira:PROJ-1044  "} {
		result, err := NormalizeIntake(DurableIntakeRequest{
			ProjectID: "project-1",
			Kind:      IntakeKindJiraIssueKey,
			TicketKey: key,
		})
		if err != nil {
			t.Fatalf("expected ticket key %q accepted, got %v", key, err)
		}
		if result.InputRef != "jira:PROJ-1044" {
			t.Fatalf("expected jira:PROJ-1044, got %q", result.InputRef)
		}
		if result.Kind != IntakeKindJiraIssueKey || result.ProjectID != "project-1" {
			t.Fatalf("unexpected result metadata: %#v", result)
		}
		if result.SafeSummary == "" {
			t.Fatalf("expected non-empty safe summary, got %#v", result)
		}
	}
}

func TestNormalizeIntakeJiraRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"fake ticket prefix", "ticket:PROJ-1044"},
		{"lowercase key", "proj-1044"},
		{"lowercase prefixed key", "jira:proj-1044"},
		{"empty prefixed key", "jira:"},
		{"trailing segment", "PROJ-1044-extra"},
		{"missing number", "PROJ-"},
		{"empty", ""},
		{"digits first", "1PROJ-1044"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeIntake(DurableIntakeRequest{
				ProjectID: "project-1",
				Kind:      IntakeKindJiraIssueKey,
				TicketKey: tc.key,
			})
			if !errors.Is(err, ErrInvalidIntake) {
				t.Fatalf("expected ErrInvalidIntake for key %q, got %v", tc.key, err)
			}
		})
	}
}

// Jira mode fails closed: objective fields must be empty (documented choice
// over silently ignoring them).
func TestNormalizeIntakeJiraRejectsObjectiveFields(t *testing.T) {
	_, err := NormalizeIntake(DurableIntakeRequest{
		ProjectID:     "project-1",
		Kind:          IntakeKindJiraIssueKey,
		TicketKey:     "PROJ-1044",
		ObjectiveText: objectiveFixture,
	})
	if !errors.Is(err, ErrInvalidIntake) {
		t.Fatalf("expected objective text rejected in jira mode, got %v", err)
	}
	_, err = NormalizeIntake(DurableIntakeRequest{
		ProjectID:          "project-1",
		Kind:               IntakeKindJiraIssueKey,
		TicketKey:          "PROJ-1044",
		ObjectiveTitleHint: "pilot title",
	})
	if !errors.Is(err, ErrInvalidIntake) {
		t.Fatalf("expected objective title hint rejected in jira mode, got %v", err)
	}
}

func TestNormalizeIntakeObjectiveAccepted(t *testing.T) {
	result, err := NormalizeIntake(DurableIntakeRequest{
		ProjectID:     "project-1",
		Kind:          IntakeKindObjectiveText,
		ObjectiveText: objectiveFixture,
	})
	if err != nil {
		t.Fatalf("expected objective accepted, got %v", err)
	}
	if !objectiveRefPattern.MatchString(result.InputRef) {
		t.Fatalf("expected objective:<12hex> ref, got %q", result.InputRef)
	}
	if result.SafeSummary != "objective intake" {
		t.Fatalf("expected default safe summary, got %q", result.SafeSummary)
	}
	if len(result.ContextRefs) != 0 {
		t.Fatalf("expected empty context refs at this layer, got %#v", result.ContextRefs)
	}

	hinted, err := NormalizeIntake(DurableIntakeRequest{
		ProjectID:          "project-1",
		Kind:               IntakeKindObjectiveText,
		ObjectiveText:      objectiveFixture,
		ObjectiveTitleHint: "pilot fixture objective",
	})
	if err != nil {
		t.Fatalf("expected hinted objective accepted, got %v", err)
	}
	if hinted.SafeSummary != "pilot fixture objective" {
		t.Fatalf("expected hint as safe summary, got %q", hinted.SafeSummary)
	}
}

func TestNormalizeIntakeObjectiveDeterminism(t *testing.T) {
	base := DurableIntakeRequest{
		ProjectID:     "project-1",
		Kind:          IntakeKindObjectiveText,
		ObjectiveText: objectiveFixture,
	}
	first, err := NormalizeIntake(base)
	if err != nil {
		t.Fatalf("first normalize: %v", err)
	}
	second, err := NormalizeIntake(base)
	if err != nil {
		t.Fatalf("second normalize: %v", err)
	}
	if first.InputRef != second.InputRef {
		t.Fatalf("same input produced different refs: %q vs %q", first.InputRef, second.InputRef)
	}

	whitespaceVariant := base
	whitespaceVariant.ObjectiveText = "  Create  one\nmarkdown   file describing the pilot fixture scenario "
	normalized, err := NormalizeIntake(whitespaceVariant)
	if err != nil {
		t.Fatalf("whitespace variant normalize: %v", err)
	}
	if normalized.InputRef != first.InputRef {
		t.Fatalf("whitespace-normalized variant produced different ref: %q vs %q", normalized.InputRef, first.InputRef)
	}

	differentText := base
	differentText.ObjectiveText = objectiveFixture + " with one extra requirement"
	other, err := NormalizeIntake(differentText)
	if err != nil {
		t.Fatalf("different text normalize: %v", err)
	}
	if other.InputRef == first.InputRef {
		t.Fatalf("different text produced same ref %q", other.InputRef)
	}

	differentProject := base
	differentProject.ProjectID = "project-2"
	otherProject, err := NormalizeIntake(differentProject)
	if err != nil {
		t.Fatalf("different project normalize: %v", err)
	}
	if otherProject.InputRef == first.InputRef {
		t.Fatalf("different project produced same ref %q", otherProject.InputRef)
	}
}

func TestNormalizeIntakeObjectiveRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"whitespace only", "   \n  "},
		{"oversized", strings.Repeat("a", 8193)},
		{"sensitive marker", "objective mentions the api_key value"},
		{"raw prompt marker", "store the raw_prompt for later"},
		{"root path", "write to /home/mac/notes.md"},
		{"url", "fetch https://example.test first"},
		{"email", "send results to user@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeIntake(DurableIntakeRequest{
				ProjectID:     "project-1",
				Kind:          IntakeKindObjectiveText,
				ObjectiveText: tc.text,
			})
			if !errors.Is(err, ErrInvalidIntake) {
				t.Fatalf("expected ErrInvalidIntake, got %v", err)
			}
		})
	}
}

// Objective mode fails closed: a ticket key must not be set.
func TestNormalizeIntakeObjectiveRejectsTicketKey(t *testing.T) {
	_, err := NormalizeIntake(DurableIntakeRequest{
		ProjectID:     "project-1",
		Kind:          IntakeKindObjectiveText,
		ObjectiveText: objectiveFixture,
		TicketKey:     "PROJ-1044",
	})
	if !errors.Is(err, ErrInvalidIntake) {
		t.Fatalf("expected ticket key rejected in objective mode, got %v", err)
	}
}

func TestNormalizeIntakeUnsupportedKind(t *testing.T) {
	_, err := NormalizeIntake(DurableIntakeRequest{
		ProjectID: "project-1",
		Kind:      IntakeKind("free_text"),
	})
	if !errors.Is(err, ErrInvalidIntake) {
		t.Fatalf("expected unsupported kind rejected, got %v", err)
	}
}

func TestObjectiveTextNeverPersisted(t *testing.T) {
	req := DurableIntakeRequest{
		ProjectID:          "project-1",
		Kind:               IntakeKindObjectiveText,
		ObjectiveText:      objectiveFixture,
		ObjectiveTitleHint: "pilot fixture objective",
	}
	encodedReq, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if strings.Contains(string(encodedReq), objectiveFixture) {
		t.Fatalf("serialized request leaked objective text: %s", encodedReq)
	}
	if strings.Contains(string(encodedReq), "pilot fixture scenario") {
		t.Fatalf("serialized request leaked objective substring: %s", encodedReq)
	}

	result, err := NormalizeIntake(req)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(encodedResult), objectiveFixture) {
		t.Fatalf("serialized result leaked objective text: %s", encodedResult)
	}
}

func TestIntakeErrorsNeverContainObjectiveText(t *testing.T) {
	oversized := objectiveFixture + " " + strings.Repeat("b", 8193)
	_, err := NormalizeIntake(DurableIntakeRequest{
		ProjectID:     "project-1",
		Kind:          IntakeKindObjectiveText,
		ObjectiveText: oversized,
	})
	if !errors.Is(err, ErrInvalidIntake) {
		t.Fatalf("expected oversized objective rejected, got %v", err)
	}
	if strings.Contains(err.Error(), objectiveFixture) {
		t.Fatalf("oversized error leaked objective text: %v", err)
	}

	sensitive := objectiveFixture + " and rotate the api_key"
	_, err = NormalizeIntake(DurableIntakeRequest{
		ProjectID:     "project-1",
		Kind:          IntakeKindObjectiveText,
		ObjectiveText: sensitive,
	})
	if !errors.Is(err, ErrInvalidIntake) {
		t.Fatalf("expected sensitive objective rejected, got %v", err)
	}
	if strings.Contains(err.Error(), objectiveFixture) {
		t.Fatalf("sensitive error leaked objective text: %v", err)
	}
}
