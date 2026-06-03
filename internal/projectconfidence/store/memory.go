package store

import (
	"context"
	"sort"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/projectconfidence"
)

type MemoryStore struct {
	mu          sync.RWMutex
	assessments map[string]projectconfidence.ConfidenceAssessment
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{assessments: make(map[string]projectconfidence.ConfidenceAssessment)}
}

func (store *MemoryStore) CreateAssessment(_ context.Context, assessment projectconfidence.ConfidenceAssessment) (projectconfidence.ConfidenceAssessment, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	assessment = cloneAssessment(assessment)
	store.assessments[assessmentKey(assessment.ProjectID, assessment.ClaimID)] = assessment
	return cloneAssessment(assessment), nil
}

func (store *MemoryStore) GetAssessment(_ context.Context, projectID string, claimID string) (projectconfidence.ConfidenceAssessment, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	assessment, ok := store.assessments[assessmentKey(projectID, claimID)]
	if !ok {
		return projectconfidence.ConfidenceAssessment{}, ErrNotFound
	}
	return cloneAssessment(assessment), nil
}

func (store *MemoryStore) ListAssessments(_ context.Context, projectID string, filter projectconfidence.AssessmentFilter) ([]projectconfidence.ConfidenceAssessment, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]projectconfidence.ConfidenceAssessment, 0)
	for _, assessment := range store.assessments {
		if assessment.ProjectID != projectID || !matchesFilter(assessment, filter) {
			continue
		}
		out = append(out, cloneAssessment(assessment))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ClaimID < out[j].ClaimID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func matchesFilter(assessment projectconfidence.ConfidenceAssessment, filter projectconfidence.AssessmentFilter) bool {
	if filter.Band != "" && assessment.Band != filter.Band {
		return false
	}
	if filter.MinScore != nil && assessment.Score < *filter.MinScore {
		return false
	}
	if filter.MaxScore != nil && assessment.Score > *filter.MaxScore {
		return false
	}
	if filter.Recommendation != "" && assessment.Recommendation != filter.Recommendation {
		return false
	}
	if filter.RunID != "" && assessment.RunID != filter.RunID {
		return false
	}
	if filter.TraceID != "" && assessment.TraceID != filter.TraceID {
		return false
	}
	return true
}

func cloneAssessment(assessment projectconfidence.ConfidenceAssessment) projectconfidence.ConfidenceAssessment {
	assessment.Factors = append([]projectconfidence.ConfidenceFactor(nil), assessment.Factors...)
	assessment.Inputs.EvidenceKinds = append([]string(nil), assessment.Inputs.EvidenceKinds...)
	return assessment
}

func assessmentKey(projectID string, claimID string) string {
	return projectID + "\x00" + claimID
}
