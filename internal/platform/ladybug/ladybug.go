package ladybug

import (
	"context"
	"errors"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
)

type NativeRuntimeStatus struct {
	Available bool
	Reason    string
}

var (
	ErrNodeNotFound         = errors.New("ladybug node not found")
	ErrRelationshipNotFound = errors.New("ladybug relationship not found")
)

type Node struct {
	Label      string
	ID         string
	Properties map[string]string
}

type NodeRef struct {
	Label string
	ID    string
}

type Relationship struct {
	Type       string
	From       NodeRef
	To         NodeRef
	Properties map[string]string
}

type RelationshipFilter struct {
	From       *NodeRef
	To         *NodeRef
	Properties map[string]string
}

type Graph interface {
	Bootstrap(context.Context, schema.GraphSchema) error
	PutNode(context.Context, Node) error
	GetNode(context.Context, string, string) (Node, error)
	ListNodes(context.Context, string, map[string]string) ([]Node, error)
	DeleteNodes(context.Context, string, map[string]string) error
	PutRelationship(context.Context, Relationship) error
	ListRelationships(context.Context, string, RelationshipFilter) ([]Relationship, error)
}

type BatchGraph interface {
	Batch(context.Context, func(Graph) error) error
}

type MemoryGraph struct {
	mu                  sync.RWMutex
	nodeLabels          map[string]struct{}
	relationshipSchemas map[string]schema.Relationship
	nodes               map[string]Node
	relationships       map[string]Relationship
}

func NewMemoryGraph() *MemoryGraph {
	return &MemoryGraph{
		nodeLabels:          make(map[string]struct{}),
		relationshipSchemas: make(map[string]schema.Relationship),
		nodes:               make(map[string]Node),
		relationships:       make(map[string]Relationship),
	}
}

func (graph *MemoryGraph) Bootstrap(_ context.Context, graphSchema schema.GraphSchema) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	for _, label := range graphSchema.NodeLabels {
		graph.nodeLabels[label] = struct{}{}
	}
	for _, rel := range graphSchema.Relationships {
		graph.relationshipSchemas[rel.Type] = rel
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

func (graph *MemoryGraph) ListNodes(_ context.Context, label string, filter map[string]string) ([]Node, error) {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	nodes := make([]Node, 0)
	for _, node := range graph.nodes {
		if node.Label != label {
			continue
		}
		if !matchesProperties(node.Properties, filter) {
			continue
		}
		nodes = append(nodes, Node{
			Label:      node.Label,
			ID:         node.ID,
			Properties: copyProperties(node.Properties),
		})
	}
	return nodes, nil
}

func (graph *MemoryGraph) DeleteNodes(_ context.Context, label string, filter map[string]string) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	deleted := make(map[string]struct{})
	for key, node := range graph.nodes {
		if node.Label != label {
			continue
		}
		if !matchesProperties(node.Properties, filter) {
			continue
		}
		delete(graph.nodes, key)
		deleted[key] = struct{}{}
	}
	for key, relationship := range graph.relationships {
		if _, ok := deleted[nodeKey(relationship.From.Label, relationship.From.ID)]; ok {
			delete(graph.relationships, key)
			continue
		}
		if _, ok := deleted[nodeKey(relationship.To.Label, relationship.To.ID)]; ok {
			delete(graph.relationships, key)
		}
	}
	return nil
}

func (graph *MemoryGraph) PutRelationship(_ context.Context, relationship Relationship) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	copied := Relationship{
		Type:       relationship.Type,
		From:       relationship.From,
		To:         relationship.To,
		Properties: copyProperties(relationship.Properties),
	}
	graph.relationships[relationshipKey(relationship.Type, relationship.From, relationship.To)] = copied
	return nil
}

func (graph *MemoryGraph) ListRelationships(_ context.Context, relationshipType string, filter RelationshipFilter) ([]Relationship, error) {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	out := make([]Relationship, 0)
	for _, relationship := range graph.relationships {
		if relationship.Type != relationshipType {
			continue
		}
		if filter.From != nil && relationship.From != *filter.From {
			continue
		}
		if filter.To != nil && relationship.To != *filter.To {
			continue
		}
		if !matchesProperties(relationship.Properties, filter.Properties) {
			continue
		}
		out = append(out, Relationship{
			Type:       relationship.Type,
			From:       relationship.From,
			To:         relationship.To,
			Properties: copyProperties(relationship.Properties),
		})
	}
	return out, nil
}

func (graph *MemoryGraph) GetRelationship(_ context.Context, relationshipType string, from NodeRef, to NodeRef) (Relationship, error) {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	relationship, ok := graph.relationships[relationshipKey(relationshipType, from, to)]
	if !ok {
		return Relationship{}, ErrRelationshipNotFound
	}
	return Relationship{
		Type:       relationship.Type,
		From:       relationship.From,
		To:         relationship.To,
		Properties: copyProperties(relationship.Properties),
	}, nil
}

func (graph *MemoryGraph) SchemaSnapshot() schema.GraphSchema {
	graph.mu.RLock()
	defer graph.mu.RUnlock()
	out := schema.GraphSchema{
		NodeLabels:    make([]string, 0, len(graph.nodeLabels)),
		Relationships: make([]schema.Relationship, 0, len(graph.relationshipSchemas)),
	}
	for label := range graph.nodeLabels {
		out.NodeLabels = append(out.NodeLabels, label)
	}
	for _, rel := range graph.relationshipSchemas {
		out.Relationships = append(out.Relationships, rel)
	}
	return out
}

func nodeKey(label string, id string) string {
	return label + ":" + id
}

func relationshipKey(relationshipType string, from NodeRef, to NodeRef) string {
	return relationshipType + ":" + nodeKey(from.Label, from.ID) + "->" + nodeKey(to.Label, to.ID)
}

func copyProperties(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func matchesProperties(properties map[string]string, filter map[string]string) bool {
	for key, value := range filter {
		if properties[key] != value {
			return false
		}
	}
	return true
}
