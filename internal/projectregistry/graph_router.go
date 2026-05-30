package projectregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug/schema"
)

type ProjectGraphRouter struct {
	registry    *Registry
	memory      ladybug.Graph
	persistent  ladybug.Graph
	allBackends []ladybug.Graph
}

func NewProjectGraphRouter(registry *Registry, memory ladybug.Graph, persistent ladybug.Graph) *ProjectGraphRouter {
	backends := make([]ladybug.Graph, 0, 2)
	if memory != nil {
		backends = append(backends, memory)
	}
	if persistent != nil && persistent != memory {
		backends = append(backends, persistent)
	}
	return &ProjectGraphRouter{
		registry:    registry,
		memory:      memory,
		persistent:  persistent,
		allBackends: backends,
	}
}

func (router *ProjectGraphRouter) Bootstrap(ctx context.Context, graphSchema schema.GraphSchema) error {
	for _, backend := range router.allBackends {
		if err := backend.Bootstrap(ctx, graphSchema); err != nil {
			return err
		}
	}
	return nil
}

func (router *ProjectGraphRouter) PutNode(ctx context.Context, node ladybug.Node) error {
	backend, err := router.backendForProjectID(projectIDForNode(node))
	if err != nil {
		return err
	}
	return backend.PutNode(ctx, node)
}

func (router *ProjectGraphRouter) GetNode(ctx context.Context, label string, id string) (ladybug.Node, error) {
	if backend, err := router.backendForNodeRef(label, id); err == nil {
		return backend.GetNode(ctx, label, id)
	}
	for _, backend := range router.allBackends {
		node, err := backend.GetNode(ctx, label, id)
		if err == nil {
			return node, nil
		}
		if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
			return ladybug.Node{}, err
		}
	}
	return ladybug.Node{}, ladybug.ErrNodeNotFound
}

func (router *ProjectGraphRouter) ListNodes(ctx context.Context, label string, filter map[string]string) ([]ladybug.Node, error) {
	if projectID := strings.TrimSpace(filter["project_id"]); projectID != "" {
		backend, err := router.backendForProjectID(projectID)
		if err != nil {
			return nil, err
		}
		return backend.ListNodes(ctx, label, filter)
	}
	nodes := make([]ladybug.Node, 0)
	for _, backend := range router.allBackends {
		listed, err := backend.ListNodes(ctx, label, filter)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, listed...)
	}
	return nodes, nil
}

func (router *ProjectGraphRouter) DeleteNodes(ctx context.Context, label string, filter map[string]string) error {
	if projectID := strings.TrimSpace(filter["project_id"]); projectID != "" {
		backend, err := router.backendForProjectID(projectID)
		if err != nil {
			return err
		}
		return backend.DeleteNodes(ctx, label, filter)
	}
	for _, backend := range router.allBackends {
		if err := backend.DeleteNodes(ctx, label, filter); err != nil {
			return err
		}
	}
	return nil
}

func (router *ProjectGraphRouter) PutRelationship(ctx context.Context, relationship ladybug.Relationship) error {
	projectID := strings.TrimSpace(relationship.Properties["project_id"])
	if projectID == "" {
		projectID = router.projectIDForRef(relationship.From.Label, relationship.From.ID)
	}
	if projectID == "" {
		projectID = router.projectIDForRef(relationship.To.Label, relationship.To.ID)
	}
	backend, err := router.backendForProjectID(projectID)
	if err != nil {
		return err
	}
	return backend.PutRelationship(ctx, relationship)
}

func (router *ProjectGraphRouter) ListRelationships(ctx context.Context, relationshipType string, filter ladybug.RelationshipFilter) ([]ladybug.Relationship, error) {
	if projectID := strings.TrimSpace(filter.Properties["project_id"]); projectID != "" {
		backend, err := router.backendForProjectID(projectID)
		if err != nil {
			return nil, err
		}
		return backend.ListRelationships(ctx, relationshipType, filter)
	}
	relationships := make([]ladybug.Relationship, 0)
	for _, backend := range router.allBackends {
		listed, err := backend.ListRelationships(ctx, relationshipType, filter)
		if err != nil {
			return nil, err
		}
		relationships = append(relationships, listed...)
	}
	return relationships, nil
}

func (router *ProjectGraphRouter) Batch(ctx context.Context, fn func(ladybug.Graph) error) error {
	if fn == nil {
		return nil
	}
	batcher, ok := router.persistent.(ladybug.BatchGraph)
	if !ok {
		return fn(router)
	}
	return batcher.Batch(ctx, func(persistent ladybug.Graph) error {
		batched := NewProjectGraphRouter(router.registry, router.memory, persistent)
		return fn(batched)
	})
}

func (router *ProjectGraphRouter) backendForNodeRef(label string, id string) (ladybug.Graph, error) {
	projectID := router.projectIDForRef(label, id)
	if projectID == "" {
		return nil, fmt.Errorf("project graph backend cannot infer project for %s node", label)
	}
	return router.backendForProjectID(projectID)
}

func (router *ProjectGraphRouter) backendForProjectID(projectID string) (ladybug.Graph, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		if router.memory == nil {
			return nil, fmt.Errorf("project graph memory backend is required")
		}
		return router.memory, nil
	}
	project, ok := router.registry.Get(projectID)
	if !ok {
		return nil, ErrProjectNotFound
	}
	switch project.GraphStorage {
	case GraphStorageInMemory:
		if router.memory == nil {
			return nil, fmt.Errorf("project graph memory backend is required")
		}
		return router.memory, nil
	case GraphStoragePersistent:
		if router.persistent == nil {
			return nil, fmt.Errorf("project graph persistent backend is required")
		}
		return router.persistent, nil
	default:
		return nil, fmt.Errorf("project %q graph_storage must be %q or %q", project.ID, GraphStoragePersistent, GraphStorageInMemory)
	}
}

func (router *ProjectGraphRouter) projectIDForRef(label string, id string) string {
	if label == "Project" {
		if _, ok := router.registry.Get(id); ok {
			return id
		}
	}
	namespace := id
	if colon := strings.Index(namespace, ":"); colon >= 0 {
		namespace = namespace[:colon]
	}
	for _, project := range router.registry.projects {
		if project.GraphNamespace == namespace {
			return project.ID
		}
	}
	return ""
}

func projectIDForNode(node ladybug.Node) string {
	if node.Label == "Project" {
		return node.ID
	}
	return strings.TrimSpace(node.Properties["project_id"])
}
