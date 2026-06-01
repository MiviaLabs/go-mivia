package agentactivity

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
)

func ProjectStreamHandler(recorder *Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if recorder == nil {
			httpserver.WriteError(w, http.StatusServiceUnavailable, "activity_unavailable", "agent activity recorder is unavailable")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			httpserver.WriteError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming is unsupported")
			return
		}
		projectID := strings.TrimSpace(r.PathValue("id"))
		if projectID == "" {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_project_request", "project id is required")
			return
		}
		limit := 50
		if raw := strings.TrimSpace(r.URL.Query().Get("recent")); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 || value > 500 {
				httpserver.WriteError(w, http.StatusBadRequest, "invalid_activity_request", "recent must be between 0 and 500")
				return
			}
			limit = value
		}
		afterID, hasCursor, err := replayCursor(r)
		if err != nil {
			httpserver.WriteError(w, http.StatusBadRequest, "invalid_activity_request", "Last-Event-ID and after_id must be non-negative integers")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		replay := recorder.Recent(projectID, limit)
		if hasCursor {
			replay = recorder.Since(projectID, afterID, limit)
		}
		for _, event := range replay {
			writeSSE(w, event)
		}
		flusher.Flush()

		events := recorder.Subscribe(r.Context())
		for {
			select {
			case <-r.Context().Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				if event.ProjectID != projectID {
					continue
				}
				writeSSE(w, event)
				flusher.Flush()
			}
		}
	})
}

func replayCursor(r *http.Request) (int64, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("after_id"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, false, err
	}
	return value, true, nil
}

func writeSSE(w http.ResponseWriter, event Event) {
	encoded, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\n", event.ID)
	fmt.Fprint(w, "event: mcp_activity\n")
	fmt.Fprintf(w, "data: %s\n\n", encoded)
}
