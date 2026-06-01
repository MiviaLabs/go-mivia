package agentactivity

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const defaultCapacity = 500

type Event struct {
	ID         int64           `json:"id"`
	Timestamp  time.Time       `json:"timestamp"`
	ProjectID  string          `json:"project_id,omitempty"`
	Method     string          `json:"method"`
	ToolName   string          `json:"tool_name,omitempty"`
	Status     string          `json:"status"`
	DurationMS int64           `json:"duration_ms"`
	Error      string          `json:"error,omitempty"`
	RequestID  string          `json:"request_id,omitempty"`
	RemoteAddr string          `json:"remote_addr,omitempty"`
	UserAgent  string          `json:"user_agent,omitempty"`
	RawRequest json.RawMessage `json:"raw_request,omitempty"`
	RawParams  json.RawMessage `json:"raw_params,omitempty"`
	RawArgs    json.RawMessage `json:"raw_arguments,omitempty"`
	RawResult  json.RawMessage `json:"raw_result,omitempty"`
}

type Recorder struct {
	mu          sync.Mutex
	nextID      int64
	capacity    int
	events      []Event
	subscribers map[chan Event]struct{}
}

func NewRecorder(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	return &Recorder{
		capacity:    capacity,
		subscribers: make(map[chan Event]struct{}),
	}
}

func (recorder *Recorder) Record(event Event) Event {
	if recorder == nil {
		return event
	}
	recorder.mu.Lock()
	recorder.nextID++
	event.ID = recorder.nextID
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
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
	return event
}

func (recorder *Recorder) Recent(projectID string, limit int) []Event {
	if recorder == nil {
		return nil
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if limit == 0 {
		return nil
	}
	if limit < 0 || limit > recorder.capacity {
		limit = recorder.capacity
	}
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
