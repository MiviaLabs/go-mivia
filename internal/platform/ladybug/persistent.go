package ladybug

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
)

type PersistentGraph struct {
	mu      sync.Mutex
	path    string
	graph   *MemoryGraph
	journal bool
}

type persistentSnapshot struct {
	Schema        schema.GraphSchema `json:"schema"`
	Nodes         []Node             `json:"nodes"`
	Relationships []Relationship     `json:"relationships"`
}

type persistentOperation struct {
	Op           string             `json:"op"`
	Schema       schema.GraphSchema `json:"schema,omitempty"`
	Node         *Node              `json:"node,omitempty"`
	Label        string             `json:"label,omitempty"`
	Filter       map[string]string  `json:"filter,omitempty"`
	Relationship *Relationship      `json:"relationship,omitempty"`
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
	return graph.appendOperationsLocked([]persistentOperation{{
		Op:     "bootstrap",
		Schema: graphSchema,
	}})
}

func (graph *PersistentGraph) PutNode(ctx context.Context, node Node) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if err := graph.graph.PutNode(ctx, node); err != nil {
		return err
	}
	copied := copyNode(node)
	return graph.appendOperationsLocked([]persistentOperation{{
		Op:   "put_node",
		Node: &copied,
	}})
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
	return graph.appendOperationsLocked([]persistentOperation{{
		Op:     "delete_nodes",
		Label:  label,
		Filter: copyProperties(filter),
	}})
}

func (graph *PersistentGraph) PutRelationship(ctx context.Context, relationship Relationship) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if err := graph.graph.PutRelationship(ctx, relationship); err != nil {
		return err
	}
	copied := copyRelationship(relationship)
	return graph.appendOperationsLocked([]persistentOperation{{
		Op:           "put_relationship",
		Relationship: &copied,
	}})
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
	recorder := &recordingGraph{graph: graph.graph}
	callbackErr := fn(recorder)
	if callbackErr != nil {
		return callbackErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return graph.appendOperationsLocked(recorder.operations)
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
			graph.journal = true
			return nil
		}
		return fmt.Errorf("stat ladybug graph store: %w", err)
	}
	data, err := os.ReadFile(graph.path)
	if err != nil {
		return fmt.Errorf("read ladybug graph store: %w", err)
	}
	if len(data) == 0 {
		graph.journal = true
		return nil
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		graph.journal = true
		return nil
	}
	firstLine := trimmed
	if index := bytes.IndexByte(firstLine, '\n'); index >= 0 {
		firstLine = firstLine[:index]
	}
	var firstOperation persistentOperation
	if err := json.Unmarshal(firstLine, &firstOperation); err == nil && firstOperation.Op != "" {
		graph.journal = true
		return graph.replayJournal(data)
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
	graph.journal = false
	return nil
}

func (graph *PersistentGraph) replayJournal(data []byte) error {
	reader := bufio.NewReader(bytes.NewReader(data))
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var operation persistentOperation
			if decodeErr := json.Unmarshal(bytes.TrimSpace(line), &operation); decodeErr != nil {
				return fmt.Errorf("decode ladybug graph journal: %w", decodeErr)
			}
			if applyErr := graph.applyOperation(operation); applyErr != nil {
				return applyErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read ladybug graph journal: %w", err)
		}
	}
}

func (graph *PersistentGraph) applyOperation(operation persistentOperation) error {
	switch operation.Op {
	case "bootstrap":
		return graph.graph.Bootstrap(context.Background(), operation.Schema)
	case "put_node":
		if operation.Node == nil {
			return fmt.Errorf("decode ladybug graph journal: put_node missing node")
		}
		return graph.graph.PutNode(context.Background(), *operation.Node)
	case "delete_nodes":
		return graph.graph.DeleteNodes(context.Background(), operation.Label, operation.Filter)
	case "put_relationship":
		if operation.Relationship == nil {
			return fmt.Errorf("decode ladybug graph journal: put_relationship missing relationship")
		}
		return graph.graph.PutRelationship(context.Background(), *operation.Relationship)
	default:
		return fmt.Errorf("decode ladybug graph journal: unknown operation %q", operation.Op)
	}
}

func (graph *PersistentGraph) appendOperationsLocked(operations []persistentOperation) error {
	if len(operations) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(graph.path), 0o700); err != nil {
		return fmt.Errorf("create ladybug graph store directory: %w", err)
	}
	if !graph.journal {
		if err := graph.rewriteJournalLocked(); err != nil {
			return err
		}
		graph.journal = true
		return nil
	}
	file, err := os.OpenFile(graph.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("write ladybug graph store: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, operation := range operations {
		if err := encoder.Encode(operation); err != nil {
			return fmt.Errorf("write ladybug graph store: %w", err)
		}
	}
	return nil
}

func (graph *PersistentGraph) rewriteJournalLocked() error {
	file, err := os.OpenFile(graph.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write ladybug graph store: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, operation := range graph.snapshotOperationsLocked() {
		if err := encoder.Encode(operation); err != nil {
			return fmt.Errorf("write ladybug graph store: %w", err)
		}
	}
	return nil
}

func (graph *PersistentGraph) snapshotOperationsLocked() []persistentOperation {
	graph.graph.mu.RLock()
	defer graph.graph.mu.RUnlock()

	operations := []persistentOperation{{
		Op:     "bootstrap",
		Schema: graph.graph.SchemaSnapshot(),
	}}
	for _, node := range graph.graph.nodes {
		copied := copyNode(node)
		operations = append(operations, persistentOperation{
			Op:   "put_node",
			Node: &copied,
		})
	}
	for _, relationship := range graph.graph.relationships {
		copied := copyRelationship(relationship)
		operations = append(operations, persistentOperation{
			Op:           "put_relationship",
			Relationship: &copied,
		})
	}
	return operations
}

type recordingGraph struct {
	graph      Graph
	operations []persistentOperation
}

func (graph *recordingGraph) Bootstrap(ctx context.Context, graphSchema schema.GraphSchema) error {
	if err := graph.graph.Bootstrap(ctx, graphSchema); err != nil {
		return err
	}
	graph.operations = append(graph.operations, persistentOperation{
		Op:     "bootstrap",
		Schema: graphSchema,
	})
	return nil
}

func (graph *recordingGraph) PutNode(ctx context.Context, node Node) error {
	if err := graph.graph.PutNode(ctx, node); err != nil {
		return err
	}
	copied := copyNode(node)
	graph.operations = append(graph.operations, persistentOperation{
		Op:   "put_node",
		Node: &copied,
	})
	return nil
}

func (graph *recordingGraph) GetNode(ctx context.Context, label string, id string) (Node, error) {
	return graph.graph.GetNode(ctx, label, id)
}

func (graph *recordingGraph) ListNodes(ctx context.Context, label string, filter map[string]string) ([]Node, error) {
	return graph.graph.ListNodes(ctx, label, filter)
}

func (graph *recordingGraph) DeleteNodes(ctx context.Context, label string, filter map[string]string) error {
	if err := graph.graph.DeleteNodes(ctx, label, filter); err != nil {
		return err
	}
	graph.operations = append(graph.operations, persistentOperation{
		Op:     "delete_nodes",
		Label:  label,
		Filter: copyProperties(filter),
	})
	return nil
}

func (graph *recordingGraph) PutRelationship(ctx context.Context, relationship Relationship) error {
	if err := graph.graph.PutRelationship(ctx, relationship); err != nil {
		return err
	}
	copied := copyRelationship(relationship)
	graph.operations = append(graph.operations, persistentOperation{
		Op:           "put_relationship",
		Relationship: &copied,
	})
	return nil
}

func copyNode(node Node) Node {
	return Node{
		Label:      node.Label,
		ID:         node.ID,
		Properties: copyProperties(node.Properties),
	}
}

func copyRelationship(relationship Relationship) Relationship {
	return Relationship{
		Type:       relationship.Type,
		From:       relationship.From,
		To:         relationship.To,
		Properties: copyProperties(relationship.Properties),
	}
}
