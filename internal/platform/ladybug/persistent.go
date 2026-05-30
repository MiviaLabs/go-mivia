package ladybug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
)

type PersistentGraph struct {
	mu    sync.Mutex
	path  string
	graph *MemoryGraph
}

type persistentSnapshot struct {
	Schema        schema.GraphSchema `json:"schema"`
	Nodes         []Node             `json:"nodes"`
	Relationships []Relationship     `json:"relationships"`
}

func OpenPersistentGraph(path string) (*PersistentGraph, error) {
	if path == "" {
		return nil, fmt.Errorf("ladybug path must not be empty")
	}
	graph := &PersistentGraph{
		path:  path,
		graph: NewMemoryGraph(),
	}
	if err := graph.load(); err != nil {
		return nil, err
	}
	return graph, nil
}

func (graph *PersistentGraph) Bootstrap(ctx context.Context, graphSchema schema.GraphSchema) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if err := graph.graph.Bootstrap(ctx, graphSchema); err != nil {
		return err
	}
	return graph.persist()
}

func (graph *PersistentGraph) PutNode(ctx context.Context, node Node) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if err := graph.graph.PutNode(ctx, node); err != nil {
		return err
	}
	return graph.persist()
}

func (graph *PersistentGraph) GetNode(ctx context.Context, label string, id string) (Node, error) {
	return graph.graph.GetNode(ctx, label, id)
}

func (graph *PersistentGraph) ListNodes(ctx context.Context, label string, filter map[string]string) ([]Node, error) {
	return graph.graph.ListNodes(ctx, label, filter)
}

func (graph *PersistentGraph) DeleteNodes(ctx context.Context, label string, filter map[string]string) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if err := graph.graph.DeleteNodes(ctx, label, filter); err != nil {
		return err
	}
	return graph.persist()
}

func (graph *PersistentGraph) PutRelationship(ctx context.Context, relationship Relationship) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if err := graph.graph.PutRelationship(ctx, relationship); err != nil {
		return err
	}
	return graph.persist()
}

func (graph *PersistentGraph) Batch(ctx context.Context, fn func(Graph) error) error {
	if fn == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	callbackErr := fn(graph.graph)
	if err := ctx.Err(); err != nil {
		return errors.Join(callbackErr, err)
	}
	persistErr := graph.persist()
	return errors.Join(callbackErr, persistErr)
}

func (graph *PersistentGraph) GetRelationship(ctx context.Context, relationshipType string, from NodeRef, to NodeRef) (Relationship, error) {
	return graph.graph.GetRelationship(ctx, relationshipType, from, to)
}

func (graph *PersistentGraph) SchemaSnapshot() schema.GraphSchema {
	return graph.graph.SchemaSnapshot()
}

func (graph *PersistentGraph) load() error {
	if _, err := os.Stat(graph.path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat ladybug graph store: %w", err)
	}
	data, err := os.ReadFile(graph.path)
	if err != nil {
		return fmt.Errorf("read ladybug graph store: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var snapshot persistentSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("decode ladybug graph store: %w", err)
	}
	if err := graph.graph.Bootstrap(context.Background(), snapshot.Schema); err != nil {
		return err
	}
	for _, node := range snapshot.Nodes {
		if err := graph.graph.PutNode(context.Background(), node); err != nil {
			return err
		}
	}
	for _, relationship := range snapshot.Relationships {
		if err := graph.graph.PutRelationship(context.Background(), relationship); err != nil {
			return err
		}
	}
	return nil
}

func (graph *PersistentGraph) persist() error {
	if err := os.MkdirAll(filepath.Dir(graph.path), 0o700); err != nil {
		return fmt.Errorf("create ladybug graph store directory: %w", err)
	}
	data, err := json.MarshalIndent(graph.snapshot(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode ladybug graph store: %w", err)
	}
	tmpPath := graph.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write ladybug graph store: %w", err)
	}
	if err := os.Rename(tmpPath, graph.path); err != nil {
		return fmt.Errorf("replace ladybug graph store: %w", err)
	}
	return nil
}

func (graph *PersistentGraph) snapshot() persistentSnapshot {
	graph.graph.mu.RLock()
	defer graph.graph.mu.RUnlock()

	nodes := make([]Node, 0, len(graph.graph.nodes))
	for _, node := range graph.graph.nodes {
		nodes = append(nodes, Node{
			Label:      node.Label,
			ID:         node.ID,
			Properties: copyProperties(node.Properties),
		})
	}
	relationships := make([]Relationship, 0, len(graph.graph.relationships))
	for _, relationship := range graph.graph.relationships {
		relationships = append(relationships, Relationship{
			Type:       relationship.Type,
			From:       relationship.From,
			To:         relationship.To,
			Properties: copyProperties(relationship.Properties),
		})
	}
	return persistentSnapshot{
		Schema:        graph.graph.SchemaSnapshot(),
		Nodes:         nodes,
		Relationships: relationships,
	}
}
