package store

import (
	"errors"

	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
)

var ErrNotFound = errors.New("project automation resource not found")
var ErrDuplicate = errors.New("project automation resource already exists")

func shouldPreserveExistingRun(existing, next projectautomation.AutomationRun) bool {
	if next.Status != projectautomation.RunStatusRunning || existing.Status == projectautomation.RunStatusFailed {
		return false
	}
	switch next.SafeSummary {
	case "pre_execution_recovery", projectautomation.RunSafeSummaryGitOpsPostTaskRecovery, projectautomation.RunSafeSummaryPostImplementationReviewQueued:
		return true
	default:
		return false
	}
}
