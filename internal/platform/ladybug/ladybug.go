package ladybug

import (
	"context"
	"errors"
	"sync"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
)

type NativeRuntimeStatus struct {
	Available bool
	Reason    string
}

var ErrNodeNotFound = errors.New("ladybug node not found")

type Node struct {
	Label      string
	ID         string
	Properties map[string]string
}

type Graph interface {
	Bootstrap(context.Context, schema.GraphSchema) error
	PutNode(context.Context, Node) error
	GetNode(context.Context, string, string) (Node, error)
}

type MemoryGraph struct {
	mu            sync.RWMutex
	nodeLabels    map[string]struct{}
	relationships map[string]schema.Relationship
	nodes         map[string]Node
}

func NewMemoryGraph() *MemoryGraph {
	return &MemoryGraph{
		nodeLabels:    make(map[string]struct{}),
		relationships: make(map[string]schema.Relationship),
		nodes:         make(map[string]Node),
	}
}

func (graph *MemoryGraph) Bootstrap(_ context.Context, graphSchema schema.GraphSchema) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	for _, label := range graphSchema.NodeLabels {
		graph.nodeLabels[label] = struct{}{}
	}
	for _, rel := range graphSchema.Relationships {
		graph.relationships[rel.Type] = rel
	}
	return nil
}

func (graph *MemoryGraph) PutNode(_ context.Context, node Node) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	copied := Node{
		Label:      node.Label,
		ID:         node.ID,
		Properties: copyProperties(node.Properties),
	}
	graph.nodes[nodeKey(node.Label, node.ID)] = copied
	return nil
}

func (graph *MemoryGraph) GetNode(_ context.Context, label string, id string) (Node, error) {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	node, ok := graph.nodes[nodeKey(label, id)]
	if !ok {
		return Node{}, ErrNodeNotFound
	}
	return Node{
		Label:      node.Label,
		ID:         node.ID,
		Properties: copyProperties(node.Properties),
	}, nil
}

func (graph *MemoryGraph) SchemaSnapshot() schema.GraphSchema {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	out := schema.GraphSchema{
		NodeLabels:    make([]string, 0, len(graph.nodeLabels)),
		Relationships: make([]schema.Relationship, 0, len(graph.relationships)),
	}
	for label := range graph.nodeLabels {
		out.NodeLabels = append(out.NodeLabels, label)
	}
	for _, rel := range graph.relationships {
		out.Relationships = append(out.Relationships, rel)
	}
	return out
}

func nodeKey(label string, id string) string {
	return label + ":" + id
}

func copyProperties(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
