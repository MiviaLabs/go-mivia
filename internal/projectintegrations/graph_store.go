package projectintegrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MiviaLabs/go-mivia/internal/platform/ladybug"
)

type integrationGraphBackend interface {
	PutNode(context.Context, ladybug.Node) error
	GetNode(context.Context, string, string) (ladybug.Node, error)
	ListNodes(context.Context, string, map[string]string) ([]ladybug.Node, error)
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
	Changed       bool
}

type RichContentReadOptions struct {
	MaxChunkBytes int
	MaxChunks     int
	ChunkOffset   int
}

type RichContentSearchOptions struct {
	Provider        Provider
	Query           string
	MaxResults      int
	MaxSnippetBytes int
	CaseSensitive   bool
}

type RichContentArtifact struct {
	ID            string
	ProjectID     string
	Provider      Provider
	ItemID        string
	ItemKey       string
	ItemType      string
	FieldCount    int
	ChunkCount    int
	ContentSHA256 string
	UpdatedAt     time.Time
}

type RichContentChunkView struct {
	ID            string
	ArtifactID    string
	ProjectID     string
	Provider      Provider
	ItemID        string
	ItemKey       string
	ItemType      string
	FieldName     string
	Label         string
	Index         int
	ByteStart     int
	ByteEnd       int
	Text          string
	TextTruncated bool
	UpdatedAt     time.Time
}

type RichContentReadResult struct {
	Artifact        RichContentArtifact    `json:"artifact"`
	Chunks          []RichContentChunkView `json:"chunks"`
	ChunksTruncated bool                   `json:"chunks_truncated"`
	NextChunkOffset int                    `json:"next_chunk_offset,omitempty"`
}

type RichContentSearchResult struct {
	Artifact         RichContentArtifact
	Chunk            RichContentChunkView
	Snippet          string
	SnippetTruncated bool
	ByteStart        int
	ByteEnd          int
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
		Changed:       true,
	}
	existing, err := store.graph.GetNode(ctx, "IntegrationArtifact", artifactID)
	if err == nil && existing.Properties["content_sha256"] == contentSHA {
		if err := store.withBatch(ctx, func(store *RichContentGraphStore) error {
			return store.putRichContentArtifactMetadata(ctx, artifactID, contentSHA, item, len(graphChunks))
		}); err != nil {
			return RichContentGraphResult{}, err
		}
		result.ChunksWritten = 0
		result.Changed = false
		return result, nil
	}
	if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
		return RichContentGraphResult{}, err
	}
	if err := store.withBatch(ctx, func(store *RichContentGraphStore) error {
		return store.putRichContentItem(ctx, artifactID, contentSHA, item, graphChunks)
	}); err != nil {
		return RichContentGraphResult{}, err
	}
	return result, nil
}

func (store *RichContentGraphStore) GetRichContentItem(ctx context.Context, projectID string, provider Provider, itemID string, options RichContentReadOptions) (RichContentReadResult, error) {
	if store == nil || store.graph == nil {
		return RichContentReadResult{}, fmt.Errorf("%w: integration graph store unavailable", ErrInvalidInput)
	}
	projectID = strings.TrimSpace(projectID)
	itemID = strings.TrimSpace(itemID)
	if projectID == "" || provider == "" || itemID == "" {
		return RichContentReadResult{}, ErrInvalidInput
	}
	artifactNode, err := store.findArtifactNode(ctx, projectID, provider, itemID)
	if err != nil {
		return RichContentReadResult{}, err
	}
	artifact, err := artifactFromNode(artifactNode)
	if err != nil {
		return RichContentReadResult{}, err
	}
	if artifact.ProjectID != projectID || artifact.Provider != provider {
		return RichContentReadResult{}, ErrNotFound
	}
	chunkNodes, err := store.graph.ListNodes(ctx, "IntegrationContentChunk", map[string]string{
		"project_id":  projectID,
		"provider":    string(provider),
		"artifact_id": artifact.ID,
	})
	if err != nil {
		return RichContentReadResult{}, err
	}
	chunks, truncated, nextOffset, err := chunksFromNodes(chunkNodes, options.MaxChunkBytes, options.MaxChunks, options.ChunkOffset)
	if err != nil {
		return RichContentReadResult{}, err
	}
	return RichContentReadResult{Artifact: artifact, Chunks: chunks, ChunksTruncated: truncated, NextChunkOffset: nextOffset}, nil
}

func (store *RichContentGraphStore) findArtifactNode(ctx context.Context, projectID string, provider Provider, itemIDOrKey string) (ladybug.Node, error) {
	artifactID := integrationArtifactID(projectID, provider, itemIDOrKey)
	node, err := store.graph.GetNode(ctx, "IntegrationArtifact", artifactID)
	if err == nil {
		return node, nil
	}
	if err != nil && !errors.Is(err, ladybug.ErrNodeNotFound) {
		return ladybug.Node{}, err
	}
	nodes, err := store.graph.ListNodes(ctx, "IntegrationArtifact", map[string]string{
		"project_id": projectID,
		"provider":   string(provider),
	})
	if err != nil {
		return ladybug.Node{}, err
	}
	for _, node := range nodes {
		if node.Properties["item_id"] == itemIDOrKey || node.Properties["item_key"] == itemIDOrKey {
			return node, nil
		}
	}
	return ladybug.Node{}, ErrNotFound
}

func (store *RichContentGraphStore) SearchRichContent(ctx context.Context, projectID string, options RichContentSearchOptions) ([]RichContentSearchResult, error) {
	if store == nil || store.graph == nil {
		return nil, fmt.Errorf("%w: integration graph store unavailable", ErrInvalidInput)
	}
	projectID = strings.TrimSpace(projectID)
	query := strings.TrimSpace(options.Query)
	if projectID == "" || query == "" {
		return nil, ErrInvalidInput
	}
	filter := map[string]string{"project_id": projectID}
	if options.Provider != "" {
		filter["provider"] = string(options.Provider)
	}
	nodes, err := store.graph.ListNodes(ctx, "IntegrationContentChunk", filter)
	if err != nil {
		return nil, err
	}
	sort.Slice(nodes, func(i, j int) bool {
		leftIndex := atoiDefault(nodes[i].Properties["chunk_index"])
		rightIndex := atoiDefault(nodes[j].Properties["chunk_index"])
		if nodes[i].Properties["artifact_id"] != nodes[j].Properties["artifact_id"] {
			return nodes[i].Properties["artifact_id"] < nodes[j].Properties["artifact_id"]
		}
		if leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		return nodes[i].ID < nodes[j].ID
	})
	limit := boundedSearchLimit(options.MaxResults)
	snippetBytes := boundedSnippetBytes(options.MaxSnippetBytes)
	results := make([]RichContentSearchResult, 0)
	artifactCache := make(map[string]RichContentArtifact)
	for _, node := range nodes {
		text := sanitizeRichText(node.Properties["text"])
		index := richMatchIndex(text, query, options.CaseSensitive)
		if index < 0 {
			continue
		}
		chunk, err := chunkFromNode(node, snippetBytes)
		if err != nil {
			return nil, err
		}
		artifact, ok := artifactCache[chunk.ArtifactID]
		if !ok {
			artifactNode, err := store.graph.GetNode(ctx, "IntegrationArtifact", chunk.ArtifactID)
			if errors.Is(err, ladybug.ErrNodeNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			artifact, err = artifactFromNode(artifactNode)
			if err != nil {
				return nil, err
			}
			artifactCache[chunk.ArtifactID] = artifact
		}
		snippet, truncated := richSnippet(text, index, index+len(query), snippetBytes)
		results = append(results, RichContentSearchResult{
			Artifact:         artifact,
			Chunk:            chunk,
			Snippet:          snippet,
			SnippetTruncated: truncated,
			ByteStart:        chunk.ByteStart + index,
			ByteEnd:          chunk.ByteStart + index + len(query),
		})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func (store *RichContentGraphStore) putRichContentItem(ctx context.Context, artifactID string, contentSHA string, item RichContentItem, chunks []RichContentChunk) error {
	if err := store.graph.DeleteNodes(ctx, "IntegrationContentChunk", map[string]string{
		"project_id":  item.ProjectID,
		"artifact_id": artifactID,
	}); err != nil {
		return err
	}
	if err := store.putRichContentArtifactMetadata(ctx, artifactID, contentSHA, item, len(chunks)); err != nil {
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

func (store *RichContentGraphStore) putRichContentArtifactMetadata(ctx context.Context, artifactID string, contentSHA string, item RichContentItem, chunkCount int) error {
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
			"chunk_count":    strconv.Itoa(chunkCount),
			"content_sha256": contentSHA,
			"updated_at":     formatIntegrationGraphTime(item.UpdatedAt),
		},
	}); err != nil {
		return err
	}
	return store.putRelationship(ctx, "PROJECT_HAS_INTEGRATION_ARTIFACT", "Project", item.ProjectID, "IntegrationArtifact", artifactID, item.ProjectID)
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

func artifactFromNode(node ladybug.Node) (RichContentArtifact, error) {
	fieldCount, err := strconv.Atoi(node.Properties["field_count"])
	if err != nil && node.Properties["field_count"] != "" {
		return RichContentArtifact{}, err
	}
	chunkCount, err := strconv.Atoi(node.Properties["chunk_count"])
	if err != nil && node.Properties["chunk_count"] != "" {
		return RichContentArtifact{}, err
	}
	updatedAt, err := parseIntegrationGraphTime(node.Properties["updated_at"])
	if err != nil {
		return RichContentArtifact{}, err
	}
	return RichContentArtifact{
		ID:            node.ID,
		ProjectID:     node.Properties["project_id"],
		Provider:      Provider(node.Properties["provider"]),
		ItemID:        node.Properties["item_id"],
		ItemKey:       node.Properties["item_key"],
		ItemType:      node.Properties["item_type"],
		FieldCount:    fieldCount,
		ChunkCount:    chunkCount,
		ContentSHA256: node.Properties["content_sha256"],
		UpdatedAt:     updatedAt,
	}, nil
}

func chunksFromNodes(nodes []ladybug.Node, maxChunkBytes int, maxChunks int, chunkOffset int) ([]RichContentChunkView, bool, int, error) {
	limit, err := boundedReadChunkLimit(maxChunks)
	if err != nil {
		return nil, false, 0, err
	}
	if chunkOffset < 0 {
		return nil, false, 0, ErrInvalidInput
	}
	sort.Slice(nodes, func(i, j int) bool {
		leftIndex := atoiDefault(nodes[i].Properties["chunk_index"])
		rightIndex := atoiDefault(nodes[j].Properties["chunk_index"])
		if leftIndex != rightIndex {
			return leftIndex < rightIndex
		}
		return nodes[i].ID < nodes[j].ID
	})
	if chunkOffset > len(nodes) {
		chunkOffset = len(nodes)
	}
	nodes = nodes[chunkOffset:]
	truncated := len(nodes) > limit
	nextOffset := 0
	if truncated {
		nextOffset = chunkOffset + limit
		nodes = nodes[:limit]
	}
	chunks := make([]RichContentChunkView, 0, len(nodes))
	for _, node := range nodes {
		chunk, err := chunkFromNode(node, maxChunkBytes)
		if err != nil {
			return nil, false, 0, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, truncated, nextOffset, nil
}

func chunkFromNode(node ladybug.Node, maxChunkBytes int) (RichContentChunkView, error) {
	index, err := strconv.Atoi(node.Properties["chunk_index"])
	if err != nil {
		return RichContentChunkView{}, err
	}
	byteStart, err := strconv.Atoi(node.Properties["byte_start"])
	if err != nil {
		return RichContentChunkView{}, err
	}
	byteEnd, err := strconv.Atoi(node.Properties["byte_end"])
	if err != nil {
		return RichContentChunkView{}, err
	}
	updatedAt, err := parseIntegrationGraphTime(node.Properties["updated_at"])
	if err != nil {
		return RichContentChunkView{}, err
	}
	text, truncated := truncateRichTextForResponse(sanitizeRichText(node.Properties["text"]), maxChunkBytes)
	return RichContentChunkView{
		ID:            node.ID,
		ArtifactID:    node.Properties["artifact_id"],
		ProjectID:     node.Properties["project_id"],
		Provider:      Provider(node.Properties["provider"]),
		ItemID:        node.Properties["item_id"],
		ItemKey:       node.Properties["item_key"],
		ItemType:      node.Properties["item_type"],
		FieldName:     node.Properties["field_name"],
		Label:         node.Properties["label"],
		Index:         index,
		ByteStart:     byteStart,
		ByteEnd:       byteEnd,
		Text:          text,
		TextTruncated: truncated,
		UpdatedAt:     updatedAt,
	}, nil
}

func boundedSearchLimit(value int) int {
	if value <= 0 {
		return 10
	}
	if value > 50 {
		return 50
	}
	return value
}

func boundedReadChunkLimit(value int) (int, error) {
	if value < 0 {
		return 0, ErrInvalidInput
	}
	if value == 0 {
		return 3, nil
	}
	if value > 200 {
		return 200, nil
	}
	return value, nil
}

func boundedSnippetBytes(value int) int {
	if value <= 0 {
		return 512
	}
	if value > 4096 {
		return 4096
	}
	return value
}

func richMatchIndex(text string, query string, caseSensitive bool) int {
	if !caseSensitive {
		text = strings.ToLower(text)
		query = strings.ToLower(query)
	}
	return strings.Index(text, query)
}

func richSnippet(text string, start int, end int, maxBytes int) (string, bool) {
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	if end < start {
		end = start
	}
	prefixStart := start - maxBytes/2
	if prefixStart < 0 {
		prefixStart = 0
	}
	suffixEnd := prefixStart + maxBytes
	if suffixEnd > len(text) {
		suffixEnd = len(text)
	}
	snippet := text[prefixStart:suffixEnd]
	truncated := prefixStart > 0 || suffixEnd < len(text)
	snippet, utf8Truncated := truncateRichTextForResponse(snippet, maxBytes)
	return snippet, truncated || utf8Truncated
}

func truncateRichTextForResponse(text string, maxBytes int) (string, bool) {
	if maxBytes <= 0 {
		maxBytes = 2048
	}
	if len([]byte(text)) <= maxBytes {
		return text, false
	}
	return truncateUTF8Bytes(text, maxBytes), true
}

func parseIntegrationGraphTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func atoiDefault(value string) int {
	parsed, _ := strconv.Atoi(value)
	return parsed
}
