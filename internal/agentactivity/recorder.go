package agentactivity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultCapacity = 500

type Event struct {
	ID                 int64           `json:"id"`
	Timestamp          time.Time       `json:"timestamp"`
	ProjectID          string          `json:"project_id,omitempty"`
	Method             string          `json:"method"`
	ToolName           string          `json:"tool_name,omitempty"`
	Status             string          `json:"status"`
	DurationMS         int64           `json:"duration_ms"`
	Error              string          `json:"error,omitempty"`
	FailureCategory    string          `json:"failure_category,omitempty"`
	RequestID          string          `json:"request_id,omitempty"`
	RemoteAddr         string          `json:"remote_addr,omitempty"`
	UserAgent          string          `json:"user_agent,omitempty"`
	ClientClass        string          `json:"client_class,omitempty"`
	InputSummaryHash   string          `json:"input_summary_hash,omitempty"`
	InputSummaryClass  string          `json:"input_summary_class,omitempty"`
	OutputSummaryHash  string          `json:"output_summary_hash,omitempty"`
	OutputSummaryClass string          `json:"output_summary_class,omitempty"`
	RawRequest         json.RawMessage `json:"raw_request,omitempty"`
	RawParams          json.RawMessage `json:"raw_params,omitempty"`
	RawArgs            json.RawMessage `json:"raw_arguments,omitempty"`
	RawResult          json.RawMessage `json:"raw_result,omitempty"`
}

type Store interface {
	Record(context.Context, Event) error
	Recent(context.Context, string, int) ([]Event, error)
}

type CursorStore interface {
	Since(context.Context, string, int64, int) ([]Event, error)
}

type IDStore interface {
	MaxID(context.Context) (int64, error)
}

type Recorder struct {
	mu          sync.Mutex
	nextID      int64
	capacity    int
	events      []Event
	subscribers map[chan Event]struct{}
	store       Store
	storeDirty  bool
}

func NewRecorder(capacity int) *Recorder {
	return NewRecorderWithStore(capacity, nil)
}

func NewRecorderWithStore(capacity int, store Store) *Recorder {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	recorder := &Recorder{
		capacity:    capacity,
		subscribers: make(map[chan Event]struct{}),
		store:       store,
	}
	if idStore, ok := store.(IDStore); ok {
		if maxID, err := idStore.MaxID(context.Background()); err == nil {
			recorder.nextID = maxID
		}
	}
	return recorder
}

func (recorder *Recorder) Record(event Event) Event {
	if recorder == nil {
		return event
	}
	event = enrichEvent(event)
	recorder.mu.Lock()
	recorder.nextID++
	event.ID = recorder.nextID
	recorder.events = append(recorder.events, cloneEvent(event))
	if len(recorder.events) > recorder.capacity {
		copy(recorder.events, recorder.events[len(recorder.events)-recorder.capacity:])
		recorder.events = recorder.events[:recorder.capacity]
	}
	subscribers := make([]chan Event, 0, len(recorder.subscribers))
	for ch := range recorder.subscribers {
		subscribers = append(subscribers, ch)
	}
	recorder.mu.Unlock()

	for _, ch := range subscribers {
		select {
		case ch <- cloneEvent(event):
		default:
		}
	}
	if recorder.store != nil {
		if err := recorder.store.Record(context.Background(), event); err != nil {
			recorder.mu.Lock()
			recorder.storeDirty = true
			recorder.mu.Unlock()
		}
	}
	return event
}

func (recorder *Recorder) Recent(projectID string, limit int) []Event {
	if recorder == nil {
		return nil
	}
	recorder.mu.Lock()
	if limit == 0 {
		recorder.mu.Unlock()
		return nil
	}
	if limit < 0 || limit > recorder.capacity {
		limit = recorder.capacity
	}
	memoryEvents := recorder.recentFromMemoryLocked(projectID, limit)
	storeDirty := recorder.storeDirty
	recorder.mu.Unlock()
	if recorder.store != nil {
		if events, err := recorder.store.Recent(context.Background(), projectID, limit); err == nil {
			if !storeDirty {
				return events
			}
			return mergeRecent(events, memoryEvents, projectID, limit)
		}
	}
	return memoryEvents
}

func (recorder *Recorder) Since(projectID string, afterID int64, limit int) []Event {
	if recorder == nil || afterID < 0 {
		return nil
	}
	if limit == 0 {
		return nil
	}
	if limit < 0 || limit > recorder.capacity {
		limit = recorder.capacity
	}
	recorder.mu.Lock()
	memoryEvents := recorder.sinceFromMemoryLocked(projectID, afterID, limit)
	storeDirty := recorder.storeDirty
	recorder.mu.Unlock()
	if cursorStore, ok := recorder.store.(CursorStore); ok {
		if events, err := cursorStore.Since(context.Background(), projectID, afterID, limit); err == nil {
			if !storeDirty {
				return events
			}
			return mergeRecent(events, memoryEvents, projectID, limit)
		}
	}
	return memoryEvents
}

func (recorder *Recorder) recentFromMemoryLocked(projectID string, limit int) []Event {
	selected := make([]Event, 0, limit)
	for index := len(recorder.events) - 1; index >= 0 && len(selected) < limit; index-- {
		event := recorder.events[index]
		if projectID != "" && event.ProjectID != projectID {
			continue
		}
		selected = append(selected, cloneEvent(event))
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

func (recorder *Recorder) sinceFromMemoryLocked(projectID string, afterID int64, limit int) []Event {
	selected := make([]Event, 0, limit)
	for _, event := range recorder.events {
		if event.ID <= afterID {
			continue
		}
		if projectID != "" && event.ProjectID != projectID {
			continue
		}
		selected = append(selected, cloneEvent(event))
		if len(selected) == limit {
			break
		}
	}
	return selected
}

func mergeRecent(persisted []Event, memory []Event, projectID string, limit int) []Event {
	if limit == 0 {
		return nil
	}
	byID := make(map[int64]Event, len(persisted)+len(memory))
	for _, event := range persisted {
		if projectID != "" && event.ProjectID != projectID {
			continue
		}
		byID[event.ID] = cloneEvent(event)
	}
	for _, event := range memory {
		if projectID != "" && event.ProjectID != projectID {
			continue
		}
		byID[event.ID] = cloneEvent(event)
	}
	merged := make([]Event, 0, len(byID))
	for _, event := range byID {
		merged = append(merged, event)
	}
	sort.Slice(merged, func(left, right int) bool {
		return merged[left].ID < merged[right].ID
	})
	if len(merged) > limit {
		merged = merged[len(merged)-limit:]
	}
	return merged
}

func enrichEvent(event Event) Event {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.ClientClass == "" {
		event.ClientClass = classifyClient(event.UserAgent)
	}
	if event.FailureCategory == "" {
		event.FailureCategory = classifyFailure(event.Status, event.Error)
	}
	input := firstRaw(event.RawArgs, event.RawParams, event.RawRequest)
	event.InputSummaryHash = hashRaw(input)
	event.InputSummaryClass = classifyRaw(input)
	event.OutputSummaryHash = hashRaw(event.RawResult)
	event.OutputSummaryClass = classifyRaw(event.RawResult)
	if event.OutputSummaryClass == "empty" && event.Error != "" {
		event.OutputSummaryHash = hashString(event.Error)
		event.OutputSummaryClass = "error"
	}
	return event
}

func firstRaw(values ...json.RawMessage) json.RawMessage {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func hashRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return hashString(string(raw))
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func classifyRaw(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "empty"
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return "invalid_json"
	}
	switch decoded.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

func classifyClient(userAgent string) string {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	switch {
	case ua == "":
		return "unknown"
	case strings.Contains(ua, "codex"):
		return "codex"
	case strings.Contains(ua, "claude"):
		return "claude"
	case strings.Contains(ua, "mozilla"):
		return "browser"
	case strings.Contains(ua, "curl"):
		return "curl"
	default:
		return "other"
	}
}

func classifyFailure(status string, message string) string {
	if strings.EqualFold(status, "ok") || strings.TrimSpace(message) == "" {
		return ""
	}
	normalized := strings.ToLower(message)
	switch {
	case strings.Contains(normalized, "invalid"):
		return "invalid_request"
	case strings.Contains(normalized, "not found") || strings.Contains(normalized, "not_indexed"):
		return "not_found"
	case strings.Contains(normalized, "unauthorized") || strings.Contains(normalized, "forbidden"):
		return "authorization"
	case strings.Contains(normalized, "timeout") || strings.Contains(normalized, "deadline"):
		return "timeout"
	case strings.Contains(normalized, "unavailable"):
		return "unavailable"
	default:
		return "handler_error"
	}
}

func (recorder *Recorder) Subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, 64)
	if recorder == nil {
		close(ch)
		return ch
	}
	recorder.mu.Lock()
	recorder.subscribers[ch] = struct{}{}
	recorder.mu.Unlock()

	go func() {
		<-ctx.Done()
		recorder.mu.Lock()
		delete(recorder.subscribers, ch)
		recorder.mu.Unlock()
		close(ch)
	}()
	return ch
}

func cloneEvent(event Event) Event {
	event.RawRequest = cloneRaw(event.RawRequest)
	event.RawParams = cloneRaw(event.RawParams)
	event.RawArgs = cloneRaw(event.RawArgs)
	event.RawResult = cloneRaw(event.RawResult)
	return event
}

func cloneRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	cloned := make([]byte, len(raw))
	copy(cloned, raw)
	return cloned
}
