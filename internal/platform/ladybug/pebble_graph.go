package ladybug

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug/schema"
	"github.com/cockroachdb/pebble/v2"
)

const pebbleGraphSchemaVersion = "1"

var pebbleSync = &pebble.WriteOptions{Sync: true}

type PebbleGraph struct {
	mu sync.Mutex
	db *pebble.DB
}

type pebbleNodeRecord struct {
	Node Node `json:"node"`
}

type pebbleRelationshipRecord struct {
	Relationship Relationship `json:"relationship"`
}

func OpenPebbleGraph(path string) (*PebbleGraph, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("ladybug pebble path must not be empty")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, fmt.Errorf("create ladybug pebble graph directory: %w", err)
	}
	db, err := pebble.Open(path, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("open ladybug pebble graph: %w", err)
	}
	graph := &PebbleGraph{db: db}
	if err := graph.ensureMetadata(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return graph, nil
}

func (graph *PebbleGraph) Close() error {
	if graph == nil || graph.db == nil {
		return nil
	}
	return graph.db.Close()
}

func (graph *PebbleGraph) Bootstrap(ctx context.Context, graphSchema schema.GraphSchema) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	batch := graph.db.NewBatch()
	defer batch.Close()
	for _, label := range graphSchema.NodeLabels {
		if err := batch.Set([]byte("schema:node:"+escapeKey(label)), []byte{1}, nil); err != nil {
			return fmt.Errorf("write ladybug pebble schema: %w", err)
		}
	}
	for _, rel := range graphSchema.Relationships {
		data, err := json.Marshal(rel)
		if err != nil {
			return err
		}
		if err := batch.Set([]byte("schema:rel:"+escapeKey(rel.Type)), data, nil); err != nil {
			return fmt.Errorf("write ladybug pebble schema: %w", err)
		}
	}
	return batch.Commit(pebbleSync)
}

func (graph *PebbleGraph) PutNode(ctx context.Context, node Node) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	batch := graph.db.NewBatch()
	defer batch.Close()
	if err := graph.putNodeBatch(batch, node); err != nil {
		return err
	}
	return batch.Commit(pebbleSync)
}

func (graph *PebbleGraph) GetNode(ctx context.Context, label string, id string) (Node, error) {
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	return graph.getNodeLocked(label, id)
}

func (graph *PebbleGraph) ListNodes(ctx context.Context, label string, filter map[string]string) ([]Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	keys, err := graph.nodeCandidateKeysLocked(label, filter)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(keys))
	for _, key := range keys {
		node, err := graph.getNodeByStorageKeyLocked(key)
		if errors.Is(err, ErrNodeNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if node.Label != label || !matchesProperties(node.Properties, filter) {
			continue
		}
		out = append(out, copyNode(node))
	}
	return out, nil
}

func (graph *PebbleGraph) DeleteNodes(ctx context.Context, label string, filter map[string]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	keys, err := graph.nodeCandidateKeysLocked(label, filter)
	if err != nil {
		return err
	}
	batch := graph.db.NewBatch()
	defer batch.Close()
	for _, key := range keys {
		node, err := graph.getNodeByStorageKeyLocked(key)
		if errors.Is(err, ErrNodeNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if node.Label != label || !matchesProperties(node.Properties, filter) {
			continue
		}
		if err := graph.deleteNodeBatch(batch, node); err != nil {
			return err
		}
	}
	return batch.Commit(pebbleSync)
}

func (graph *PebbleGraph) DeleteDerivedFileNodes(ctx context.Context, projectID string, repoFileID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(repoFileID) == "" {
		return nil
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	prefix := []byte("nf:" + escapeKey(projectID) + ":" + escapeKey(repoFileID) + ":")
	keys, err := graph.indexValuesLocked(prefix)
	if err != nil {
		return err
	}
	batch := graph.db.NewBatch()
	defer batch.Close()
	for _, key := range keys {
		node, err := graph.getNodeByStorageKeyLocked(key)
		if errors.Is(err, ErrNodeNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if node.Properties["project_id"] != projectID || node.Properties["repo_file_id"] != repoFileID || !isDerivedFileNodeLabel(node.Label) {
			continue
		}
		if err := graph.deleteNodeBatch(batch, node); err != nil {
			return err
		}
	}
	return batch.Commit(pebbleSync)
}

func (graph *PebbleGraph) PutRelationship(ctx context.Context, relationship Relationship) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	batch := graph.db.NewBatch()
	defer batch.Close()
	if err := graph.putRelationshipBatch(batch, relationship); err != nil {
		return err
	}
	return batch.Commit(pebbleSync)
}

func (graph *PebbleGraph) ListRelationships(ctx context.Context, relationshipType string, filter RelationshipFilter) ([]Relationship, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	keys, err := graph.relationshipCandidateKeysLocked(filter)
	if err != nil {
		return nil, err
	}
	out := make([]Relationship, 0, len(keys))
	for _, key := range keys {
		relationship, err := graph.getRelationshipByStorageKeyLocked(key)
		if errors.Is(err, ErrRelationshipNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
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
		out = append(out, copyRelationship(relationship))
	}
	return out, nil
}

func (graph *PebbleGraph) Batch(ctx context.Context, fn func(Graph) error) error {
	if fn == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	graph.mu.Lock()
	defer graph.mu.Unlock()
	batch := graph.db.NewBatch()
	defer batch.Close()
	batched := &pebbleBatchGraph{graph: graph, batch: batch}
	if err := fn(batched); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return batch.Commit(pebbleSync)
}

func (graph *PebbleGraph) ensureMetadata() error {
	if _, closer, err := graph.db.Get([]byte("meta:schema_version")); err == nil {
		_ = closer.Close()
		return nil
	} else if !errors.Is(err, pebble.ErrNotFound) {
		return fmt.Errorf("read ladybug pebble metadata: %w", err)
	}
	return graph.db.Set([]byte("meta:schema_version"), []byte(pebbleGraphSchemaVersion), pebbleSync)
}

func (graph *PebbleGraph) putNodeBatch(batch *pebble.Batch, node Node) error {
	if existing, err := graph.getNodeLocked(node.Label, node.ID); err == nil {
		if err := graph.deleteNodeIndexesBatch(batch, existing); err != nil {
			return err
		}
	} else if !errors.Is(err, ErrNodeNotFound) {
		return err
	}
	copied := copyNode(node)
	record, err := json.Marshal(pebbleNodeRecord{Node: copied})
	if err != nil {
		return err
	}
	key := nodeStorageKey(copied.Label, copied.ID)
	if err := batch.Set([]byte(key), record, nil); err != nil {
		return fmt.Errorf("write ladybug pebble node: %w", err)
	}
	return graph.putNodeIndexesBatch(batch, copied)
}

func (graph *PebbleGraph) deleteNodeBatch(batch *pebble.Batch, node Node) error {
	key := nodeStorageKey(node.Label, node.ID)
	if err := graph.deleteAttachedRelationshipsBatch(batch, node); err != nil {
		return err
	}
	if err := graph.deleteNodeIndexesBatch(batch, node); err != nil {
		return err
	}
	if err := batch.Delete([]byte(key), nil); err != nil {
		return fmt.Errorf("delete ladybug pebble node: %w", err)
	}
	return nil
}

func (graph *PebbleGraph) putNodeIndexesBatch(batch *pebble.Batch, node Node) error {
	key := nodeStorageKey(node.Label, node.ID)
	if projectID := strings.TrimSpace(node.Properties["project_id"]); projectID != "" {
		if err := batch.Set([]byte(projectNodeIndexKey(projectID, node.Label, node.ID)), []byte(key), nil); err != nil {
			return err
		}
		if repoFileID := strings.TrimSpace(node.Properties["repo_file_id"]); repoFileID != "" {
			if err := batch.Set([]byte(fileNodeIndexKey(projectID, repoFileID, node.Label, node.ID)), []byte(key), nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (graph *PebbleGraph) deleteNodeIndexesBatch(batch *pebble.Batch, node Node) error {
	if projectID := strings.TrimSpace(node.Properties["project_id"]); projectID != "" {
		if err := batch.Delete([]byte(projectNodeIndexKey(projectID, node.Label, node.ID)), nil); err != nil {
			return err
		}
		if repoFileID := strings.TrimSpace(node.Properties["repo_file_id"]); repoFileID != "" {
			if err := batch.Delete([]byte(fileNodeIndexKey(projectID, repoFileID, node.Label, node.ID)), nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (graph *PebbleGraph) putRelationshipBatch(batch *pebble.Batch, relationship Relationship) error {
	if existing, err := graph.getRelationshipByStorageKeyLocked(relationshipStorageKey(relationship)); err == nil {
		if err := graph.deleteRelationshipIndexesBatch(batch, existing); err != nil {
			return err
		}
	} else if !errors.Is(err, ErrRelationshipNotFound) {
		return err
	}
	copied := copyRelationship(relationship)
	record, err := json.Marshal(pebbleRelationshipRecord{Relationship: copied})
	if err != nil {
		return err
	}
	key := relationshipStorageKey(copied)
	if err := batch.Set([]byte(key), record, nil); err != nil {
		return fmt.Errorf("write ladybug pebble relationship: %w", err)
	}
	return graph.putRelationshipIndexesBatch(batch, copied)
}

func (graph *PebbleGraph) deleteRelationshipBatch(batch *pebble.Batch, relationship Relationship) error {
	if err := graph.deleteRelationshipIndexesBatch(batch, relationship); err != nil {
		return err
	}
	if err := batch.Delete([]byte(relationshipStorageKey(relationship)), nil); err != nil {
		return fmt.Errorf("delete ladybug pebble relationship: %w", err)
	}
	return nil
}

func (graph *PebbleGraph) putRelationshipIndexesBatch(batch *pebble.Batch, relationship Relationship) error {
	projectID := strings.TrimSpace(relationship.Properties["project_id"])
	if projectID == "" {
		return nil
	}
	key := relationshipStorageKey(relationship)
	for _, indexKey := range relationshipIndexKeys(projectID, relationship) {
		if err := batch.Set([]byte(indexKey), []byte(key), nil); err != nil {
			return err
		}
	}
	return nil
}

func (graph *PebbleGraph) deleteRelationshipIndexesBatch(batch *pebble.Batch, relationship Relationship) error {
	projectID := strings.TrimSpace(relationship.Properties["project_id"])
	if projectID == "" {
		return nil
	}
	for _, indexKey := range relationshipIndexKeys(projectID, relationship) {
		if err := batch.Delete([]byte(indexKey), nil); err != nil {
			return err
		}
	}
	return nil
}

func (graph *PebbleGraph) deleteAttachedRelationshipsBatch(batch *pebble.Batch, node Node) error {
	seen := make(map[string]struct{})
	for _, prefix := range [][]byte{
		[]byte("eo:" + escapeKey(node.Properties["project_id"]) + ":" + escapeKey(node.Label) + ":" + escapeKey(node.ID) + ":"),
		[]byte("ei:" + escapeKey(node.Properties["project_id"]) + ":" + escapeKey(node.Label) + ":" + escapeKey(node.ID) + ":"),
	} {
		keys, err := graph.indexValuesLocked(prefix)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			relationship, err := graph.getRelationshipByStorageKeyLocked(key)
			if errors.Is(err, ErrRelationshipNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if err := graph.deleteRelationshipBatch(batch, relationship); err != nil {
				return err
			}
		}
	}
	return nil
}

func (graph *PebbleGraph) getNodeLocked(label string, id string) (Node, error) {
	return graph.getNodeByStorageKeyLocked(nodeStorageKey(label, id))
}

func (graph *PebbleGraph) getNodeByStorageKeyLocked(key string) (Node, error) {
	value, closer, err := graph.db.Get([]byte(key))
	if errors.Is(err, pebble.ErrNotFound) {
		return Node{}, ErrNodeNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("read ladybug pebble node: %w", err)
	}
	defer closer.Close()
	var record pebbleNodeRecord
	if err := json.Unmarshal(value, &record); err != nil {
		return Node{}, fmt.Errorf("decode ladybug pebble node: %w", err)
	}
	return copyNode(record.Node), nil
}

func (graph *PebbleGraph) getRelationshipByStorageKeyLocked(key string) (Relationship, error) {
	value, closer, err := graph.db.Get([]byte(key))
	if errors.Is(err, pebble.ErrNotFound) {
		return Relationship{}, ErrRelationshipNotFound
	}
	if err != nil {
		return Relationship{}, fmt.Errorf("read ladybug pebble relationship: %w", err)
	}
	defer closer.Close()
	var record pebbleRelationshipRecord
	if err := json.Unmarshal(value, &record); err != nil {
		return Relationship{}, fmt.Errorf("decode ladybug pebble relationship: %w", err)
	}
	return copyRelationship(record.Relationship), nil
}

func (graph *PebbleGraph) nodeCandidateKeysLocked(label string, filter map[string]string) ([]string, error) {
	if projectID := strings.TrimSpace(filter["project_id"]); projectID != "" {
		if repoFileID := strings.TrimSpace(filter["repo_file_id"]); repoFileID != "" {
			return graph.indexValuesLocked([]byte("nf:" + escapeKey(projectID) + ":" + escapeKey(repoFileID) + ":" + escapeKey(label) + ":"))
		}
		return graph.indexValuesLocked([]byte("np:" + escapeKey(projectID) + ":" + escapeKey(label) + ":"))
	}
	return graph.indexKeysLocked([]byte("n:" + escapeKey(label) + ":"))
}

func (graph *PebbleGraph) relationshipCandidateKeysLocked(filter RelationshipFilter) ([]string, error) {
	projectID := ""
	if filter.Properties != nil {
		projectID = strings.TrimSpace(filter.Properties["project_id"])
	}
	if projectID != "" && filter.From != nil {
		return graph.indexValuesLocked([]byte("eo:" + escapeKey(projectID) + ":" + escapeKey(filter.From.Label) + ":" + escapeKey(filter.From.ID) + ":"))
	}
	if projectID != "" && filter.To != nil {
		return graph.indexValuesLocked([]byte("ei:" + escapeKey(projectID) + ":" + escapeKey(filter.To.Label) + ":" + escapeKey(filter.To.ID) + ":"))
	}
	if projectID != "" {
		return graph.indexValuesLocked([]byte("et:" + escapeKey(projectID) + ":"))
	}
	return graph.indexKeysLocked([]byte("e:"))
}

func (graph *PebbleGraph) indexValuesLocked(prefix []byte) ([]string, error) {
	iter, err := graph.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixUpperBound(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []string
	for valid := iter.First(); valid; valid = iter.Next() {
		out = append(out, string(bytes.Clone(iter.Value())))
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return out, nil
}

func (graph *PebbleGraph) indexKeysLocked(prefix []byte) ([]string, error) {
	iter, err := graph.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: prefixUpperBound(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []string
	for valid := iter.First(); valid; valid = iter.Next() {
		out = append(out, string(bytes.Clone(iter.Key())))
	}
	if err := iter.Error(); err != nil {
		return nil, err
	}
	return out, nil
}

type pebbleBatchGraph struct {
	graph *PebbleGraph
	batch *pebble.Batch
}

func (graph *pebbleBatchGraph) Bootstrap(_ context.Context, graphSchema schema.GraphSchema) error {
	for _, label := range graphSchema.NodeLabels {
		if err := graph.batch.Set([]byte("schema:node:"+escapeKey(label)), []byte{1}, nil); err != nil {
			return err
		}
	}
	for _, rel := range graphSchema.Relationships {
		data, err := json.Marshal(rel)
		if err != nil {
			return err
		}
		if err := graph.batch.Set([]byte("schema:rel:"+escapeKey(rel.Type)), data, nil); err != nil {
			return err
		}
	}
	return nil
}

func (graph *pebbleBatchGraph) PutNode(_ context.Context, node Node) error {
	return graph.graph.putNodeBatch(graph.batch, node)
}

func (graph *pebbleBatchGraph) GetNode(_ context.Context, label string, id string) (Node, error) {
	return graph.graph.getNodeLocked(label, id)
}

func (graph *pebbleBatchGraph) ListNodes(_ context.Context, label string, filter map[string]string) ([]Node, error) {
	keys, err := graph.graph.nodeCandidateKeysLocked(label, filter)
	if err != nil {
		return nil, err
	}
	out := make([]Node, 0, len(keys))
	for _, key := range keys {
		node, err := graph.graph.getNodeByStorageKeyLocked(key)
		if errors.Is(err, ErrNodeNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if node.Label == label && matchesProperties(node.Properties, filter) {
			out = append(out, copyNode(node))
		}
	}
	return out, nil
}

func (graph *pebbleBatchGraph) DeleteNodes(_ context.Context, label string, filter map[string]string) error {
	keys, err := graph.graph.nodeCandidateKeysLocked(label, filter)
	if err != nil {
		return err
	}
	for _, key := range keys {
		node, err := graph.graph.getNodeByStorageKeyLocked(key)
		if errors.Is(err, ErrNodeNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if node.Label == label && matchesProperties(node.Properties, filter) {
			if err := graph.graph.deleteNodeBatch(graph.batch, node); err != nil {
				return err
			}
		}
	}
	return nil
}

func (graph *pebbleBatchGraph) DeleteDerivedFileNodes(_ context.Context, projectID string, repoFileID string) error {
	keys, err := graph.graph.indexValuesLocked([]byte("nf:" + escapeKey(projectID) + ":" + escapeKey(repoFileID) + ":"))
	if err != nil {
		return err
	}
	for _, key := range keys {
		node, err := graph.graph.getNodeByStorageKeyLocked(key)
		if errors.Is(err, ErrNodeNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if node.Properties["project_id"] == projectID && node.Properties["repo_file_id"] == repoFileID && isDerivedFileNodeLabel(node.Label) {
			if err := graph.graph.deleteNodeBatch(graph.batch, node); err != nil {
				return err
			}
		}
	}
	return nil
}

func (graph *pebbleBatchGraph) PutRelationship(_ context.Context, relationship Relationship) error {
	return graph.graph.putRelationshipBatch(graph.batch, relationship)
}

func (graph *pebbleBatchGraph) ListRelationships(_ context.Context, relationshipType string, filter RelationshipFilter) ([]Relationship, error) {
	keys, err := graph.graph.relationshipCandidateKeysLocked(filter)
	if err != nil {
		return nil, err
	}
	out := make([]Relationship, 0, len(keys))
	for _, key := range keys {
		relationship, err := graph.graph.getRelationshipByStorageKeyLocked(key)
		if errors.Is(err, ErrRelationshipNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if relationship.Type == relationshipType && matchesProperties(relationship.Properties, filter.Properties) {
			out = append(out, copyRelationship(relationship))
		}
	}
	return out, nil
}

func nodeStorageKey(label string, id string) string {
	return "n:" + escapeKey(label) + ":" + escapeKey(id)
}

func projectNodeIndexKey(projectID string, label string, id string) string {
	return "np:" + escapeKey(projectID) + ":" + escapeKey(label) + ":" + escapeKey(id)
}

func fileNodeIndexKey(projectID string, repoFileID string, label string, id string) string {
	return "nf:" + escapeKey(projectID) + ":" + escapeKey(repoFileID) + ":" + escapeKey(label) + ":" + escapeKey(id)
}

func relationshipStorageKey(relationship Relationship) string {
	return "e:" + escapeKey(relationship.Type) + ":" + escapeKey(relationship.From.Label) + ":" + escapeKey(relationship.From.ID) + ":" + escapeKey(relationship.To.Label) + ":" + escapeKey(relationship.To.ID)
}

func relationshipIndexKeys(projectID string, relationship Relationship) []string {
	escapedProjectID := escapeKey(projectID)
	return []string{
		"eo:" + escapedProjectID + ":" + escapeKey(relationship.From.Label) + ":" + escapeKey(relationship.From.ID) + ":" + escapeKey(relationship.Type) + ":" + escapeKey(relationship.To.Label) + ":" + escapeKey(relationship.To.ID),
		"ei:" + escapedProjectID + ":" + escapeKey(relationship.To.Label) + ":" + escapeKey(relationship.To.ID) + ":" + escapeKey(relationship.Type) + ":" + escapeKey(relationship.From.Label) + ":" + escapeKey(relationship.From.ID),
		"et:" + escapedProjectID + ":" + escapeKey(relationship.Type) + ":" + escapeKey(relationship.From.Label) + ":" + escapeKey(relationship.From.ID) + ":" + escapeKey(relationship.To.Label) + ":" + escapeKey(relationship.To.ID),
	}
}

func isDerivedFileNodeLabel(label string) bool {
	for _, candidate := range derivedFileNodeLabels() {
		if candidate == label {
			return true
		}
	}
	return false
}

func escapeKey(value string) string {
	if value == "" {
		return "-"
	}
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func prefixUpperBound(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	out := bytes.Clone(prefix)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] != 0xff {
			out[i]++
			return out[:i+1]
		}
	}
	return nil
}

func PebbleGraphPath(path string) string {
	return filepath.Clean(path) + ".pebble"
}
