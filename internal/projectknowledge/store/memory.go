package store

import (
	"context"
	"sort"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/projectknowledge"
)

type MemoryStore struct {
	mu        sync.RWMutex
	knowledge map[string]projectknowledge.KnowledgeRecord
	decisions map[string][]projectknowledge.PromotionDecision
	reuse     map[string][]projectknowledge.KnowledgeReuseEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		knowledge: make(map[string]projectknowledge.KnowledgeRecord),
		decisions: make(map[string][]projectknowledge.PromotionDecision),
		reuse:     make(map[string][]projectknowledge.KnowledgeReuseEvent),
	}
}

func (store *MemoryStore) CreateKnowledge(_ context.Context, record projectknowledge.KnowledgeRecord) (projectknowledge.KnowledgeRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record = cloneKnowledge(record)
	store.knowledge[knowledgeKey(record.ProjectID, record.ID)] = record
	return cloneKnowledge(record), nil
}

func (store *MemoryStore) GetKnowledge(_ context.Context, projectID string, knowledgeID string) (projectknowledge.KnowledgeRecord, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	record, ok := store.knowledge[knowledgeKey(projectID, knowledgeID)]
	if !ok {
		return projectknowledge.KnowledgeRecord{}, ErrNotFound
	}
	return cloneKnowledge(record), nil
}

func (store *MemoryStore) ListKnowledge(_ context.Context, projectID string, filter projectknowledge.KnowledgeFilter) ([]projectknowledge.KnowledgeRecord, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]projectknowledge.KnowledgeRecord, 0)
	for _, record := range store.knowledge {
		if record.ProjectID != projectID || !matchesKnowledgeFilter(record, filter) {
			continue
		}
		out = append(out, cloneKnowledge(record))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *MemoryStore) ListOrgKnowledge(_ context.Context, orgRef string, filter projectknowledge.KnowledgeFilter) ([]projectknowledge.KnowledgeRecord, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	out := make([]projectknowledge.KnowledgeRecord, 0)
	for _, record := range store.knowledge {
		if record.OrgRef != orgRef || !matchesKnowledgeFilter(record, filter) {
			continue
		}
		out = append(out, cloneKnowledge(record))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (store *MemoryStore) UpdateKnowledge(_ context.Context, record projectknowledge.KnowledgeRecord) (projectknowledge.KnowledgeRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := knowledgeKey(record.ProjectID, record.ID)
	if _, ok := store.knowledge[key]; !ok {
		return projectknowledge.KnowledgeRecord{}, ErrNotFound
	}
	record = cloneKnowledge(record)
	store.knowledge[key] = record
	return cloneKnowledge(record), nil
}

func (store *MemoryStore) CreateDecision(_ context.Context, decision projectknowledge.PromotionDecision) (projectknowledge.PromotionDecision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := knowledgeKey(decision.ProjectID, decision.KnowledgeID)
	if _, ok := store.knowledge[key]; !ok {
		return projectknowledge.PromotionDecision{}, ErrNotFound
	}
	store.decisions[key] = append(store.decisions[key], decision)
	return decision, nil
}

func (store *MemoryStore) ListDecisions(_ context.Context, projectID string, knowledgeID string) ([]projectknowledge.PromotionDecision, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	key := knowledgeKey(projectID, knowledgeID)
	if _, ok := store.knowledge[key]; !ok {
		return nil, ErrNotFound
	}
	out := append([]projectknowledge.PromotionDecision(nil), store.decisions[key]...)
	sort.Slice(out, func(i, j int) bool { return out[i].DecidedAt.Before(out[j].DecidedAt) })
	return out, nil
}

func (store *MemoryStore) CreateReuseEvent(_ context.Context, event projectknowledge.KnowledgeReuseEvent) (projectknowledge.KnowledgeReuseEvent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := knowledgeKey(event.ProjectID, event.KnowledgeID)
	if _, ok := store.knowledge[key]; !ok {
		return projectknowledge.KnowledgeReuseEvent{}, ErrNotFound
	}
	store.reuse[key] = append(store.reuse[key], event)
	return event, nil
}

func (store *MemoryStore) ListReuseEvents(_ context.Context, projectID string, knowledgeID string) ([]projectknowledge.KnowledgeReuseEvent, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	key := knowledgeKey(projectID, knowledgeID)
	if _, ok := store.knowledge[key]; !ok {
		return nil, ErrNotFound
	}
	out := append([]projectknowledge.KnowledgeReuseEvent(nil), store.reuse[key]...)
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func matchesKnowledgeFilter(record projectknowledge.KnowledgeRecord, filter projectknowledge.KnowledgeFilter) bool {
	if filter.Scope != "" && record.Scope != filter.Scope {
		return false
	}
	if filter.OrgRef != "" && record.OrgRef != filter.OrgRef {
		return false
	}
	if filter.State != "" && record.State != filter.State {
		return false
	}
	if filter.ClaimID != "" && record.ClaimID != filter.ClaimID {
		return false
	}
	if filter.KnowledgeRef != "" && record.KnowledgeRef != filter.KnowledgeRef {
		return false
	}
	if filter.ConfidenceBand != "" && record.ConfidenceBand != filter.ConfidenceBand {
		return false
	}
	if filter.MinConfidence != nil && record.ConfidenceScore < *filter.MinConfidence {
		return false
	}
	if filter.MaxConfidence != nil && record.ConfidenceScore > *filter.MaxConfidence {
		return false
	}
	return true
}

func cloneKnowledge(record projectknowledge.KnowledgeRecord) projectknowledge.KnowledgeRecord {
	record.EvidenceRefs = append([]string(nil), record.EvidenceRefs...)
	record.VerifierRefs = append([]string(nil), record.VerifierRefs...)
	record.OutcomeRefs = append([]string(nil), record.OutcomeRefs...)
	record.PromotionRefs = append([]string(nil), record.PromotionRefs...)
	return record
}

func knowledgeKey(projectID string, knowledgeID string) string {
	return projectID + "\x00" + knowledgeID
}
