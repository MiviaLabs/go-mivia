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
	DeleteDerivedFileNodes(context.Context, string, string) error
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
	nodesByLabelFileID  map[string]map[string]map[string]struct{}
	relationshipsByNode map[string]map[string]struct{}
}

func NewMemoryGraph() *MemoryGraph {
	return &MemoryGraph{
		nodeLabels:          make(map[string]struct{}),
		relationshipSchemas: make(map[string]schema.Relationship),
		nodes:               make(map[string]Node),
		relationships:       make(map[string]Relationship),
		nodesByLabelFileID:  make(map[string]map[string]map[string]struct{}),
		relationshipsByNode: make(map[string]map[string]struct{}),
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
	key := nodeKey(node.Label, node.ID)
	if existing, ok := graph.nodes[key]; ok {
		graph.unindexNodeLocked(key, existing)
	}
	graph.nodes[key] = copied
	graph.indexNodeLocked(key, copied)
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
		graph.unindexNodeLocked(key, node)
		deleted[key] = struct{}{}
	}
	graph.deleteAttachedRelationshipsLocked(deleted)
	return nil
}

func (graph *MemoryGraph) DeleteDerivedFileNodes(_ context.Context, projectID string, repoFileID string) error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	deleted := make(map[string]struct{})
	for _, label := range derivedFileNodeLabels() {
		for key := range graph.fileNodeCandidatesLocked(label, repoFileID) {
			node, ok := graph.nodes[key]
			if !ok || node.Label != label || node.Properties["project_id"] != projectID || node.Properties["repo_file_id"] != repoFileID {
				continue
			}
			delete(graph.nodes, key)
			graph.unindexNodeLocked(key, node)
			deleted[key] = struct{}{}
		}
	}
	graph.deleteAttachedRelationshipsLocked(deleted)
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
	key := relationshipKey(relationship.Type, relationship.From, relationship.To)
	if existing, ok := graph.relationships[key]; ok {
		graph.unindexRelationshipLocked(key, existing)
	}
	graph.relationships[key] = copied
	graph.indexRelationshipLocked(key, copied)
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

func derivedFileNodeLabels() []string {
	return []string{"CodeReference", "CodeCall", "CodeSymbol", "DocumentHeading", "ContentChunk", "FileVersion"}
}

func (graph *MemoryGraph) fileNodeCandidatesLocked(label string, repoFileID string) map[string]struct{} {
	if repoFileID == "" {
		return map[string]struct{}{}
	}
	byFileID := graph.nodesByLabelFileID[label]
	if byFileID == nil {
		return map[string]struct{}{}
	}
	return copySet(byFileID[repoFileID])
}

func (graph *MemoryGraph) deleteAttachedRelationshipsLocked(deleted map[string]struct{}) {
	for nodeKey := range deleted {
		for relKey := range graph.relationshipsByNode[nodeKey] {
			relationship, ok := graph.relationships[relKey]
			if ok {
				graph.unindexRelationshipLocked(relKey, relationship)
			}
			delete(graph.relationships, relKey)
		}
		delete(graph.relationshipsByNode, nodeKey)
	}
}

func (graph *MemoryGraph) indexNodeLocked(key string, node Node) {
	repoFileID := node.Properties["repo_file_id"]
	if repoFileID == "" {
		return
	}
	byFileID := graph.nodesByLabelFileID[node.Label]
	if byFileID == nil {
		byFileID = make(map[string]map[string]struct{})
		graph.nodesByLabelFileID[node.Label] = byFileID
	}
	keys := byFileID[repoFileID]
	if keys == nil {
		keys = make(map[string]struct{})
		byFileID[repoFileID] = keys
	}
	keys[key] = struct{}{}
}

func (graph *MemoryGraph) unindexNodeLocked(key string, node Node) {
	repoFileID := node.Properties["repo_file_id"]
	if repoFileID == "" {
		return
	}
	byFileID := graph.nodesByLabelFileID[node.Label]
	if byFileID == nil {
		return
	}
	keys := byFileID[repoFileID]
	delete(keys, key)
	if len(keys) == 0 {
		delete(byFileID, repoFileID)
	}
	if len(byFileID) == 0 {
		delete(graph.nodesByLabelFileID, node.Label)
	}
}

func (graph *MemoryGraph) indexRelationshipLocked(key string, relationship Relationship) {
	graph.addRelationshipNodeIndexLocked(nodeKey(relationship.From.Label, relationship.From.ID), key)
	graph.addRelationshipNodeIndexLocked(nodeKey(relationship.To.Label, relationship.To.ID), key)
}

func (graph *MemoryGraph) addRelationshipNodeIndexLocked(node string, relationship string) {
	relationships := graph.relationshipsByNode[node]
	if relationships == nil {
		relationships = make(map[string]struct{})
		graph.relationshipsByNode[node] = relationships
	}
	relationships[relationship] = struct{}{}
}

func (graph *MemoryGraph) unindexRelationshipLocked(key string, relationship Relationship) {
	graph.removeRelationshipNodeIndexLocked(nodeKey(relationship.From.Label, relationship.From.ID), key)
	graph.removeRelationshipNodeIndexLocked(nodeKey(relationship.To.Label, relationship.To.ID), key)
}

func (graph *MemoryGraph) removeRelationshipNodeIndexLocked(node string, relationship string) {
	relationships := graph.relationshipsByNode[node]
	delete(relationships, relationship)
	if len(relationships) == 0 {
		delete(graph.relationshipsByNode, node)
	}
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
