package projectregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
)

type ProjectGraphRouter struct {
	registry            *Registry
	memory              ladybug.Graph
	persistent          ladybug.Graph
	persistentByProject map[string]ladybug.Graph
	storageKeyByProject map[string]string
	allBackends         []ladybug.Graph
}

type ProjectGraphBackend struct {
	ProjectID  string
	Graph      ladybug.Graph
	StorageKey string
}

type GraphStorageDiagnostic struct {
	ProjectID    string `json:"project_id"`
	Backend      string `json:"backend"`
	StorageKey   string `json:"storage_key,omitempty"`
	Open         bool   `json:"open,omitempty"`
	Leases       int    `json:"leases,omitempty"`
	OpenTotal    int    `json:"open_total,omitempty"`
	CloseTotal   int    `json:"close_total,omitempty"`
	BlockedClose int    `json:"blocked_close,omitempty"`
}

type graphLifecycleDiagnostics interface {
	Diagnostics() ladybug.LazyPebbleGraphDiagnostics
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

func NewProjectScopedGraphRouter(registry *Registry, memory ladybug.Graph, persistentBackends []ProjectGraphBackend) *ProjectGraphRouter {
	backends := make([]ladybug.Graph, 0, 1+len(persistentBackends))
	if memory != nil {
		backends = append(backends, memory)
	}
	router := &ProjectGraphRouter{
		registry:            registry,
		memory:              memory,
		persistentByProject: make(map[string]ladybug.Graph, len(persistentBackends)),
		storageKeyByProject: make(map[string]string, len(persistentBackends)),
		allBackends:         backends,
	}
	seen := make(map[ladybug.Graph]struct{}, len(persistentBackends))
	if memory != nil {
		seen[memory] = struct{}{}
	}
	for _, backend := range persistentBackends {
		projectID := strings.TrimSpace(backend.ProjectID)
		if projectID == "" || backend.Graph == nil {
			continue
		}
		router.persistentByProject[projectID] = backend.Graph
		router.storageKeyByProject[projectID] = strings.TrimSpace(backend.StorageKey)
		if _, ok := seen[backend.Graph]; ok {
			continue
		}
		seen[backend.Graph] = struct{}{}
		router.allBackends = append(router.allBackends, backend.Graph)
	}
	return router
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

func (router *ProjectGraphRouter) DeleteDerivedFileNodes(ctx context.Context, projectID string, repoFileID string) error {
	backend, err := router.backendForProjectID(projectID)
	if err != nil {
		return err
	}
	return backend.DeleteDerivedFileNodes(ctx, projectID, repoFileID)
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
	if len(router.persistentByProject) > 0 {
		return fn(router)
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

func (router *ProjectGraphRouter) BatchProject(ctx context.Context, projectID string, fn func(ladybug.Graph) error) error {
	if fn == nil {
		return nil
	}
	backend, err := router.backendForProjectID(projectID)
	if err != nil {
		return err
	}
	batcher, ok := backend.(ladybug.BatchGraph)
	if !ok {
		return fn(backend)
	}
	return batcher.Batch(ctx, fn)
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
		if !project.Enabled || project.DigestMode != DigestModeContentGraph {
			if router.memory == nil {
				return nil, fmt.Errorf("project graph memory backend is required")
			}
			return router.memory, nil
		}
		if backend := router.persistentByProject[project.ID]; backend != nil {
			return backend, nil
		}
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

func (router *ProjectGraphRouter) GraphStorageDiagnostics() []GraphStorageDiagnostic {
	if router == nil || router.registry == nil {
		return nil
	}
	diagnostics := make([]GraphStorageDiagnostic, 0, len(router.registry.projects))
	for _, project := range router.registry.projects {
		diagnostic := GraphStorageDiagnostic{
			ProjectID: project.ID,
			Backend:   "in_memory_shared",
		}
		if project.Enabled && project.DigestMode == DigestModeContentGraph && project.GraphStorage == GraphStoragePersistent {
			if storageKey := strings.TrimSpace(router.storageKeyByProject[project.ID]); storageKey != "" {
				diagnostic.Backend = "persistent_pebble_project"
				diagnostic.StorageKey = storageKey
				if backend := router.persistentByProject[project.ID]; backend != nil {
					diagnostic.applyLifecycleDiagnostics(backend)
				}
			} else if router.persistent != nil {
				diagnostic.Backend = "persistent_shared"
				diagnostic.applyLifecycleDiagnostics(router.persistent)
			}
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics
}

func (diagnostic *GraphStorageDiagnostic) applyLifecycleDiagnostics(graph ladybug.Graph) {
	lifecycle, ok := graph.(graphLifecycleDiagnostics)
	if !ok {
		return
	}
	state := lifecycle.Diagnostics()
	diagnostic.Open = state.Open
	diagnostic.Leases = state.Leases
	diagnostic.OpenTotal = state.OpenTotal
	diagnostic.CloseTotal = state.CloseTotal
	diagnostic.BlockedClose = state.BlockedClose
}
