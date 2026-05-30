package projectintegrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/mivialabs-agents-monorepo/internal/platform/ladybug"
)

type integrationGraphBackend interface {
	PutNode(context.Context, ladybug.Node) error
	DeleteNodes(context.Context, string, map[string]string) error
	PutRelationship(context.Context, ladybug.Relationship) error
}

type RichContentGraphStore struct {
	graph integrationGraphBackend
}

type RichContentGraphResult struct {
	ArtifactID    string
	ChunksWritten int
	ContentSHA256 string
}

func NewRichContentGraphStore(graph integrationGraphBackend) *RichContentGraphStore {
	return &RichContentGraphStore{graph: graph}
}

func (store *RichContentGraphStore) PutRichContentItem(ctx context.Context, item RichContentItem, chunks []RichContentChunk) (RichContentGraphResult, error) {
	if store == nil || store.graph == nil {
		return RichContentGraphResult{}, fmt.Errorf("%w: integration graph store unavailable", ErrInvalidInput)
	}
	if strings.TrimSpace(item.ProjectID) == "" || item.Provider == "" || strings.TrimSpace(item.ItemID) == "" {
		return RichContentGraphResult{}, ErrInvalidInput
	}
	artifactID := integrationArtifactID(item.ProjectID, item.Provider, item.ItemID)
	graphChunks := normalizeGraphChunks(item, chunks)
	contentSHA := integrationContentSHA256(item, graphChunks)
	result := RichContentGraphResult{
		ArtifactID:    artifactID,
		ChunksWritten: len(graphChunks),
		ContentSHA256: contentSHA,
	}
	if err := store.withBatch(ctx, func(store *RichContentGraphStore) error {
		return store.putRichContentItem(ctx, artifactID, contentSHA, item, graphChunks)
	}); err != nil {
		return RichContentGraphResult{}, err
	}
	return result, nil
}

func (store *RichContentGraphStore) putRichContentItem(ctx context.Context, artifactID string, contentSHA string, item RichContentItem, chunks []RichContentChunk) error {
	if err := store.graph.DeleteNodes(ctx, "IntegrationContentChunk", map[string]string{
		"project_id":  item.ProjectID,
		"artifact_id": artifactID,
	}); err != nil {
		return err
	}
	if err := store.graph.PutNode(ctx, ladybug.Node{
		Label: "IntegrationArtifact",
		ID:    artifactID,
		Properties: map[string]string{
			"id":             artifactID,
			"project_id":     item.ProjectID,
			"provider":       string(item.Provider),
			"item_id":        item.ItemID,
			"item_key":       item.ItemKey,
			"item_type":      item.ItemType,
			"field_count":    strconv.Itoa(len(item.Fields)),
			"chunk_count":    strconv.Itoa(len(chunks)),
			"content_sha256": contentSHA,
			"updated_at":     formatIntegrationGraphTime(item.UpdatedAt),
		},
	}); err != nil {
		return err
	}
	if err := store.putRelationship(ctx, "PROJECT_HAS_INTEGRATION_ARTIFACT", "Project", item.ProjectID, "IntegrationArtifact", artifactID, item.ProjectID); err != nil {
		return err
	}
	for index, chunk := range chunks {
		chunkID := chunk.ID
		if strings.TrimSpace(chunkID) == "" {
			chunkID = integrationContentChunkID(artifactID, chunk.FieldName, index)
		}
		if err := store.graph.PutNode(ctx, ladybug.Node{
			Label: "IntegrationContentChunk",
			ID:    chunkID,
			Properties: map[string]string{
				"id":             chunkID,
				"project_id":     item.ProjectID,
				"provider":       string(item.Provider),
				"artifact_id":    artifactID,
				"item_id":        item.ItemID,
				"item_key":       item.ItemKey,
				"item_type":      item.ItemType,
				"field_name":     chunk.FieldName,
				"label":          chunk.Label,
				"chunk_index":    strconv.Itoa(index),
				"byte_start":     strconv.Itoa(chunk.ByteStart),
				"byte_end":       strconv.Itoa(chunk.ByteEnd),
				"text":           chunk.Text,
				"content_sha256": contentSHA,
				"updated_at":     formatIntegrationGraphTime(firstGraphTime(chunk.UpdatedAt, item.UpdatedAt)),
			},
		}); err != nil {
			return err
		}
		if err := store.putRelationship(ctx, "INTEGRATION_ARTIFACT_HAS_CHUNK", "IntegrationArtifact", artifactID, "IntegrationContentChunk", chunkID, item.ProjectID); err != nil {
			return err
		}
	}
	return nil
}

func (store *RichContentGraphStore) putRelationship(ctx context.Context, relType string, fromLabel string, fromID string, toLabel string, toID string, projectID string) error {
	return store.graph.PutRelationship(ctx, ladybug.Relationship{
		Type: relType,
		From: ladybug.NodeRef{Label: fromLabel, ID: fromID},
		To:   ladybug.NodeRef{Label: toLabel, ID: toID},
		Properties: map[string]string{
			"project_id": projectID,
		},
	})
}

func (store *RichContentGraphStore) withBatch(ctx context.Context, fn func(*RichContentGraphStore) error) error {
	if fn == nil {
		return nil
	}
	if batcher, ok := store.graph.(ladybug.BatchGraph); ok {
		return batcher.Batch(ctx, func(graph ladybug.Graph) error {
			return fn(&RichContentGraphStore{graph: graph})
		})
	}
	return fn(store)
}

func normalizeGraphChunks(item RichContentItem, chunks []RichContentChunk) []RichContentChunk {
	normalized := make([]RichContentChunk, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.ProjectID != "" && chunk.ProjectID != item.ProjectID {
			continue
		}
		if chunk.Provider != "" && chunk.Provider != item.Provider {
			continue
		}
		if chunk.ItemID != "" && chunk.ItemID != item.ItemID {
			continue
		}
		field := strings.TrimSpace(chunk.FieldName)
		if field == "" || isSensitiveFieldName(field) {
			continue
		}
		text := sanitizeRichText(chunk.Text)
		if text == "" {
			continue
		}
		chunk.ProjectID = item.ProjectID
		chunk.Provider = item.Provider
		chunk.ItemID = item.ItemID
		chunk.ItemKey = item.ItemKey
		chunk.ItemType = item.ItemType
		chunk.FieldName = field
		chunk.Label = firstNonEmptyText(chunk.Label, field)
		chunk.Text = text
		normalized = append(normalized, chunk)
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].Index == normalized[j].Index {
			return normalized[i].ID < normalized[j].ID
		}
		return normalized[i].Index < normalized[j].Index
	})
	return normalized
}

func integrationArtifactID(projectID string, provider Provider, itemID string) string {
	return strings.TrimSpace(projectID) + ":integration:" + string(provider) + ":" + integrationGraphShortHash(itemID)
}

func integrationContentChunkID(artifactID string, field string, index int) string {
	return artifactID + ":chunk:" + integrationGraphShortHash(field+"\x00"+strconv.Itoa(index))
}

func integrationContentSHA256(item RichContentItem, chunks []RichContentChunk) string {
	var builder strings.Builder
	builder.WriteString(item.ProjectID)
	builder.WriteByte(0)
	builder.WriteString(string(item.Provider))
	builder.WriteByte(0)
	builder.WriteString(item.ItemID)
	builder.WriteByte(0)
	for _, chunk := range chunks {
		builder.WriteString(chunk.FieldName)
		builder.WriteByte(0)
		builder.WriteString(chunk.Text)
		builder.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func integrationGraphShortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func formatIntegrationGraphTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func firstGraphTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
