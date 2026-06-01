package agentactivity

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRecorderRecentFiltersByProjectAndKeepsCapacity(t *testing.T) {
	recorder := NewRecorder(2)
	recorder.Record(Event{ProjectID: "alpha", Method: "tools/call", RawArgs: json.RawMessage(`{"id":"alpha"}`)})
	recorder.Record(Event{ProjectID: "beta", Method: "tools/call"})
	recorder.Record(Event{ProjectID: "alpha", Method: "resources/read"})

	all := recorder.Recent("", 10)
	if len(all) != 2 {
		t.Fatalf("expected capacity-limited recent events, got %d", len(all))
	}
	if replay := recorder.Recent("", 0); len(replay) != 0 {
		t.Fatalf("expected zero replay events, got %d", len(replay))
	}
	alpha := recorder.Recent("alpha", 10)
	if len(alpha) != 1 || alpha[0].Method != "resources/read" {
		t.Fatalf("expected only latest alpha event, got %#v", alpha)
	}
}

func TestRecorderSubscribeReceivesNewEvents(t *testing.T) {
	recorder := NewRecorder(10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := recorder.Subscribe(ctx)

	recorded := recorder.Record(Event{ProjectID: "alpha", Method: "tools/call"})
	select {
	case event := <-events:
		if event.ID != recorded.ID || event.ProjectID != "alpha" {
			t.Fatalf("unexpected event %#v", event)
		}
	default:
		t.Fatal("expected subscriber event")
	}
}
