package store

import (
	"context"
	"sort"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/projectevidence"
)

type MemoryStore struct {
	mu      sync.RWMutex
	claims  map[string]projectevidence.Claim
	records map[string]projectevidence.ClaimRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		claims:  make(map[string]projectevidence.Claim),
		records: make(map[string]projectevidence.ClaimRecord),
	}
}

func (store *MemoryStore) CreateClaim(_ context.Context, claim projectevidence.Claim) (projectevidence.Claim, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := claimKey(claim.ProjectID, claim.ID)
	store.claims[key] = claim
	store.records[key] = projectevidence.ClaimRecord{Claim: claim}
	return claim, nil
}

func (store *MemoryStore) GetClaim(_ context.Context, projectID string, claimID string) (projectevidence.ClaimRecord, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	record, ok := store.records[claimKey(projectID, claimID)]
	if !ok {
		return projectevidence.ClaimRecord{}, ErrNotFound
	}
	return cloneRecord(record), nil
}

func (store *MemoryStore) ListClaims(_ context.Context, projectID string, filter projectevidence.ClaimFilter) ([]projectevidence.Claim, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	claims := make([]projectevidence.Claim, 0)
	for _, record := range store.records {
		if record.Claim.ProjectID != projectID || !matchesFilter(record, filter) {
			continue
		}
		claims = append(claims, record.Claim)
	}
	sort.Slice(claims, func(i, j int) bool { return claims[i].CreatedAt.Before(claims[j].CreatedAt) })
	return claims, nil
}

func (store *MemoryStore) AppendEvidence(_ context.Context, evidence projectevidence.Evidence) (projectevidence.Evidence, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := claimKey(evidence.ProjectID, evidence.ClaimID)
	record, ok := store.records[key]
	if !ok {
		return projectevidence.Evidence{}, ErrNotFound
	}
	record.Evidence = append(record.Evidence, evidence)
	store.records[key] = record
	return evidence, nil
}

func (store *MemoryStore) CreateDecision(_ context.Context, decision projectevidence.Decision) (projectevidence.Decision, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := claimKey(decision.ProjectID, decision.ClaimID)
	record, ok := store.records[key]
	if !ok {
		return projectevidence.Decision{}, ErrNotFound
	}
	record.Decisions = append(record.Decisions, decision)
	store.records[key] = record
	return decision, nil
}

func (store *MemoryStore) CreateAction(_ context.Context, action projectevidence.Action) (projectevidence.Action, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := claimKey(action.ProjectID, action.ClaimID)
	record, ok := store.records[key]
	if !ok {
		return projectevidence.Action{}, ErrNotFound
	}
	record.Actions = append(record.Actions, cloneAction(action))
	store.records[key] = record
	return cloneAction(action), nil
}

func (store *MemoryStore) CreateOutcome(_ context.Context, outcome projectevidence.Outcome) (projectevidence.Outcome, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := claimKey(outcome.ProjectID, outcome.ClaimID)
	record, ok := store.records[key]
	if !ok {
		return projectevidence.Outcome{}, ErrNotFound
	}
	record.Outcomes = append(record.Outcomes, outcome)
	store.records[key] = record
	return outcome, nil
}

func (store *MemoryStore) LinkArtifact(_ context.Context, link projectevidence.ArtifactLink) (projectevidence.ArtifactLink, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := claimKey(link.ProjectID, link.ClaimID)
	record, ok := store.records[key]
	if !ok {
		return projectevidence.ArtifactLink{}, ErrNotFound
	}
	record.ArtifactLinks = append(record.ArtifactLinks, link)
	store.records[key] = record
	return link, nil
}

func (store *MemoryStore) LinkPromotion(_ context.Context, link projectevidence.PromotionLink) (projectevidence.PromotionLink, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := claimKey(link.ProjectID, link.ClaimID)
	record, ok := store.records[key]
	if !ok {
		return projectevidence.PromotionLink{}, ErrNotFound
	}
	record.PromotionLinks = append(record.PromotionLinks, link)
	store.records[key] = record
	return link, nil
}

func matchesFilter(record projectevidence.ClaimRecord, filter projectevidence.ClaimFilter) bool {
	if filter.RunID != "" && record.Claim.RunID != filter.RunID {
		return false
	}
	if filter.TraceID != "" && record.Claim.TraceID != filter.TraceID {
		return false
	}
	if filter.ArtifactRef != "" && !hasArtifact(record, filter.ArtifactRef) {
		return false
	}
	if filter.PromotionState != "" && !hasPromotionState(record, filter.PromotionState) {
		return false
	}
	if filter.OutcomeStatus != "" && !hasOutcomeStatus(record, filter.OutcomeStatus) {
		return false
	}
	return true
}

func hasArtifact(record projectevidence.ClaimRecord, artifactRef string) bool {
	for _, link := range record.ArtifactLinks {
		if link.ArtifactRef == artifactRef {
			return true
		}
	}
	for _, link := range record.PromotionLinks {
		if link.ArtifactRef == artifactRef {
			return true
		}
	}
	return false
}

func hasPromotionState(record projectevidence.ClaimRecord, state string) bool {
	for _, link := range record.PromotionLinks {
		if link.PromotionState == state {
			return true
		}
	}
	return false
}

func hasOutcomeStatus(record projectevidence.ClaimRecord, status string) bool {
	for _, outcome := range record.Outcomes {
		if outcome.Status == status {
			return true
		}
	}
	return false
}

func cloneRecord(record projectevidence.ClaimRecord) projectevidence.ClaimRecord {
	record.Evidence = append([]projectevidence.Evidence(nil), record.Evidence...)
	record.Decisions = append([]projectevidence.Decision(nil), record.Decisions...)
	record.Actions = cloneActions(record.Actions)
	record.Outcomes = append([]projectevidence.Outcome(nil), record.Outcomes...)
	record.ArtifactLinks = append([]projectevidence.ArtifactLink(nil), record.ArtifactLinks...)
	record.PromotionLinks = append([]projectevidence.PromotionLink(nil), record.PromotionLinks...)
	return record
}

func cloneActions(actions []projectevidence.Action) []projectevidence.Action {
	out := append([]projectevidence.Action(nil), actions...)
	for i := range out {
		out[i].ChangedFiles = append([]string(nil), out[i].ChangedFiles...)
	}
	return out
}

func cloneAction(action projectevidence.Action) projectevidence.Action {
	action.ChangedFiles = append([]string(nil), action.ChangedFiles...)
	return action
}

func claimKey(projectID string, claimID string) string {
	return projectID + "\x00" + claimID
}
