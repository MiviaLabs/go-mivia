package ladybug

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
)

const DefaultPebbleGraphMaxOpen = 16

var ErrLazyPebbleGraphClosing = errors.New("lazy pebble graph is closing")

type PebbleGraphLRU struct {
	mu      sync.Mutex
	maxOpen int
	graphs  map[*LazyPebbleGraph]struct{}
}

type PebbleGraphLRUDiagnostics struct {
	MaxOpen      int `json:"max_open"`
	Tracked      int `json:"tracked"`
	Open         int `json:"open"`
	Leased       int `json:"leased"`
	IdleOpen     int `json:"idle_open"`
	OpenTotal    int `json:"open_total"`
	CloseTotal   int `json:"close_total"`
	BlockedClose int `json:"blocked_close"`
}

type LazyPebbleGraphDiagnostics struct {
	Open         bool `json:"open"`
	Leases       int  `json:"leases"`
	OpenTotal    int  `json:"open_total"`
	CloseTotal   int  `json:"close_total"`
	BlockedClose int  `json:"blocked_close"`
}

func NewPebbleGraphLRU(maxOpen int) *PebbleGraphLRU {
	if maxOpen <= 0 {
		maxOpen = DefaultPebbleGraphMaxOpen
	}
	return &PebbleGraphLRU{
		maxOpen: maxOpen,
		graphs:  make(map[*LazyPebbleGraph]struct{}),
	}
}

func (lru *PebbleGraphLRU) register(graph *LazyPebbleGraph) {
	if lru == nil || graph == nil {
		return
	}
	lru.mu.Lock()
	defer lru.mu.Unlock()
	lru.graphs[graph] = struct{}{}
}

func (lru *PebbleGraphLRU) enforce() error {
	if lru == nil {
		return nil
	}
	lru.mu.Lock()
	defer lru.mu.Unlock()
	for lru.openCountLocked() > lru.maxOpen {
		victim := lru.oldestIdleLocked()
		if victim == nil {
			return nil
		}
		if err := victim.closeIfIdleLocked(); err != nil {
			return err
		}
	}
	return nil
}

func (lru *PebbleGraphLRU) CloseAll() error {
	if lru == nil {
		return nil
	}
	lru.mu.Lock()
	graphs := make([]*LazyPebbleGraph, 0, len(lru.graphs))
	for graph := range lru.graphs {
		graphs = append(graphs, graph)
	}
	lru.mu.Unlock()
	var firstErr error
	for _, graph := range graphs {
		if err := graph.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (lru *PebbleGraphLRU) Diagnostics() PebbleGraphLRUDiagnostics {
	if lru == nil {
		return PebbleGraphLRUDiagnostics{}
	}
	lru.mu.Lock()
	defer lru.mu.Unlock()
	diagnostics := PebbleGraphLRUDiagnostics{
		MaxOpen: lru.maxOpen,
		Tracked: len(lru.graphs),
	}
	for graph := range lru.graphs {
		state := graph.Diagnostics()
		if state.Open {
			diagnostics.Open++
			if state.Leases == 0 {
				diagnostics.IdleOpen++
			}
		}
		if state.Leases > 0 {
			diagnostics.Leased++
		}
		diagnostics.OpenTotal += state.OpenTotal
		diagnostics.CloseTotal += state.CloseTotal
		diagnostics.BlockedClose += state.BlockedClose
	}
	return diagnostics
}

func (lru *PebbleGraphLRU) openCountLocked() int {
	count := 0
	for graph := range lru.graphs {
		graph.mu.Lock()
		if graph.graph != nil {
			count++
		}
		graph.mu.Unlock()
	}
	return count
}

func (lru *PebbleGraphLRU) oldestIdleLocked() *LazyPebbleGraph {
	var oldest *LazyPebbleGraph
	for graph := range lru.graphs {
		graph.mu.Lock()
		open := graph.graph != nil
		idle := graph.leases == 0
		lastUsed := graph.lastUsed
		graph.mu.Unlock()
		if !open || !idle {
			continue
		}
		if oldest == nil {
			oldest = graph
			continue
		}
		oldest.mu.Lock()
		oldestLastUsed := oldest.lastUsed
		oldest.mu.Unlock()
		if lastUsed.Before(oldestLastUsed) {
			oldest = graph
		}
	}
	return oldest
}

type LazyPebbleGraph struct {
	mu       sync.Mutex
	cond     *sync.Cond
	path     string
	manager  *PebbleGraphLRU
	graph    *PebbleGraph
	leases   int
	closing  bool
	schema   schema.GraphSchema
	lastUsed time.Time
	opens    int
	closes   int
	blocked  int
}

func NewLazyPebbleGraph(path string, manager *PebbleGraphLRU) *LazyPebbleGraph {
	graph := &LazyPebbleGraph{path: path, manager: manager}
	graph.cond = sync.NewCond(&graph.mu)
	if manager != nil {
		manager.register(graph)
	}
	return graph
}

func (graph *LazyPebbleGraph) IsOpenForTest() bool {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	return graph.graph != nil
}

func (graph *LazyPebbleGraph) Diagnostics() LazyPebbleGraphDiagnostics {
	if graph == nil {
		return LazyPebbleGraphDiagnostics{}
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	return LazyPebbleGraphDiagnostics{
		Open:         graph.graph != nil,
		Leases:       graph.leases,
		OpenTotal:    graph.opens,
		CloseTotal:   graph.closes,
		BlockedClose: graph.blocked,
	}
}

func (graph *LazyPebbleGraph) Bootstrap(ctx context.Context, graphSchema schema.GraphSchema) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	graph.schema = graphSchema
	if graph.graph != nil {
		return graph.graph.Bootstrap(ctx, graphSchema)
	}
	return nil
}

func (graph *LazyPebbleGraph) PutNode(ctx context.Context, node Node) error {
	return graph.withGraph(func(open *PebbleGraph) error {
		return open.PutNode(ctx, node)
	})
}

func (graph *LazyPebbleGraph) GetNode(ctx context.Context, label string, id string) (Node, error) {
	var out Node
	err := graph.withGraph(func(open *PebbleGraph) error {
		node, err := open.GetNode(ctx, label, id)
		out = node
		return err
	})
	return out, err
}

func (graph *LazyPebbleGraph) ListNodes(ctx context.Context, label string, filter map[string]string) ([]Node, error) {
	var out []Node
	err := graph.withGraph(func(open *PebbleGraph) error {
		nodes, err := open.ListNodes(ctx, label, filter)
		out = nodes
		return err
	})
	return out, err
}

func (graph *LazyPebbleGraph) DeleteNodes(ctx context.Context, label string, filter map[string]string) error {
	return graph.withGraph(func(open *PebbleGraph) error {
		return open.DeleteNodes(ctx, label, filter)
	})
}

func (graph *LazyPebbleGraph) DeleteDerivedFileNodes(ctx context.Context, projectID string, repoFileID string) error {
	return graph.withGraph(func(open *PebbleGraph) error {
		return open.DeleteDerivedFileNodes(ctx, projectID, repoFileID)
	})
}

func (graph *LazyPebbleGraph) PutRelationship(ctx context.Context, relationship Relationship) error {
	return graph.withGraph(func(open *PebbleGraph) error {
		return open.PutRelationship(ctx, relationship)
	})
}

func (graph *LazyPebbleGraph) ListRelationships(ctx context.Context, relationshipType string, filter RelationshipFilter) ([]Relationship, error) {
	var out []Relationship
	err := graph.withGraph(func(open *PebbleGraph) error {
		relationships, err := open.ListRelationships(ctx, relationshipType, filter)
		out = relationships
		return err
	})
	return out, err
}

func (graph *LazyPebbleGraph) Batch(ctx context.Context, fn func(Graph) error) error {
	return graph.withGraph(func(open *PebbleGraph) error {
		return open.Batch(ctx, fn)
	})
}

func (graph *LazyPebbleGraph) Close() error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	graph.closing = true
	for graph.leases > 0 {
		graph.blocked++
		graph.cond.Wait()
	}
	return graph.closeLocked()
}

func (graph *LazyPebbleGraph) withGraph(fn func(*PebbleGraph) error) (operationErr error) {
	open, err := graph.acquire()
	if err != nil {
		return err
	}
	released := false
	defer func() {
		if !released {
			graph.release()
		}
	}()
	operationErr = fn(open)
	graph.release()
	released = true
	if graph.manager != nil {
		if err := graph.manager.enforce(); err != nil && operationErr == nil {
			return err
		}
	}
	return operationErr
}

func (graph *LazyPebbleGraph) acquire() (*PebbleGraph, error) {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if graph.closing {
		return nil, ErrLazyPebbleGraphClosing
	}
	if graph.graph == nil {
		open, err := OpenPebbleGraph(graph.path)
		if err != nil {
			return nil, err
		}
		if len(graph.schema.NodeLabels) > 0 || len(graph.schema.Relationships) > 0 {
			if err := open.Bootstrap(context.Background(), graph.schema); err != nil {
				_ = open.Close()
				return nil, err
			}
		}
		graph.graph = open
		graph.opens++
	}
	graph.leases++
	graph.lastUsed = time.Now().UTC()
	return graph.graph, nil
}

func (graph *LazyPebbleGraph) release() {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if graph.leases > 0 {
		graph.leases--
	}
	graph.lastUsed = time.Now().UTC()
	if graph.leases == 0 {
		graph.cond.Broadcast()
	}
}

func (graph *LazyPebbleGraph) closeIfIdleLocked() error {
	graph.mu.Lock()
	defer graph.mu.Unlock()
	if graph.leases > 0 {
		graph.blocked++
		return nil
	}
	return graph.closeLocked()
}

func (graph *LazyPebbleGraph) closeLocked() error {
	if graph.graph == nil {
		return nil
	}
	open := graph.graph
	graph.graph = nil
	graph.closes++
	return open.Close()
}
