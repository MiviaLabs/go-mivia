package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/research/provider"
)

var ErrNotFound = errors.New("research metadata not found")

type MetadataStore interface {
	SaveSource(context.Context, provider.SourceMetadata) (provider.SourceMetadata, error)
	GetSource(context.Context, string) (provider.SourceMetadata, error)
	FindSourceByHash(context.Context, string) (provider.SourceMetadata, error)
}

type LadybugMetadataStore struct {
	graph  ladybug.Graph
	mu     sync.RWMutex
	hashes map[string]string
}

func NewLadybugMetadataStore(graph ladybug.Graph) *LadybugMetadataStore {
	return &LadybugMetadataStore{
		graph:  graph,
		hashes: make(map[string]string),
	}
}

func (store *LadybugMetadataStore) SaveSource(ctx context.Context, source provider.SourceMetadata) (provider.SourceMetadata, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	policyMetadata, err := json.Marshal(source.PolicyMetadata)
	if err != nil {
		return provider.SourceMetadata{}, err
	}
	if id, ok := store.hashes[source.ContentHash]; ok {
		return store.getSourceLocked(ctx, id)
	}
	if err := store.graph.PutNode(ctx, ladybug.Node{
		Label: "Source",
		ID:    source.ID,
		Properties: map[string]string{
			"id":              source.ID,
			"research_run_id": source.ResearchRunID,
			"artifact_ref":    source.ArtifactRef,
			"source_type":     source.SourceType,
			"summary":         source.Summary,
			"content_hash":    source.ContentHash,
			"retrieved_at":    source.RetrievedAt.Format(time.RFC3339Nano),
			"policy_metadata": string(policyMetadata),
		},
	}); err != nil {
		return provider.SourceMetadata{}, err
	}
	store.hashes[source.ContentHash] = source.ID
	return source, nil
}

func (store *LadybugMetadataStore) GetSource(ctx context.Context, id string) (provider.SourceMetadata, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.getSourceLocked(ctx, id)
}

func (store *LadybugMetadataStore) FindSourceByHash(ctx context.Context, hash string) (provider.SourceMetadata, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	id, ok := store.hashes[hash]
	if !ok {
		return provider.SourceMetadata{}, ErrNotFound
	}
	return store.getSourceLocked(ctx, id)
}

func (store *LadybugMetadataStore) getSourceLocked(ctx context.Context, id string) (provider.SourceMetadata, error) {
	node, err := store.graph.GetNode(ctx, "Source", id)
	if errors.Is(err, ladybug.ErrNodeNotFound) {
		return provider.SourceMetadata{}, ErrNotFound
	}
	if err != nil {
		return provider.SourceMetadata{}, err
	}
	retrievedAt, err := time.Parse(time.RFC3339Nano, node.Properties["retrieved_at"])
	if err != nil {
		return provider.SourceMetadata{}, err
	}
	policyMetadata := map[string]string{}
	if raw := node.Properties["policy_metadata"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &policyMetadata); err != nil {
			return provider.SourceMetadata{}, err
		}
	}
	if len(policyMetadata) == 0 {
		policyMetadata = map[string]string{
			"raw_content":    "not_stored",
			"pii_prohibited": "true",
		}
	}
	return provider.SourceMetadata{
		ID:             node.Properties["id"],
		ResearchRunID:  node.Properties["research_run_id"],
		ArtifactRef:    node.Properties["artifact_ref"],
		SourceType:     node.Properties["source_type"],
		Summary:        node.Properties["summary"],
		ContentHash:    node.Properties["content_hash"],
		RetrievedAt:    retrievedAt,
		PolicyMetadata: policyMetadata,
	}, nil
}
