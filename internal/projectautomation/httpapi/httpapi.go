package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/projectautomation"
	automationstore "github.com/MiviaLabs/go-mivia/internal/projectautomation/store"
)

var errInvalidJSON = errors.New("invalid json")

type automationListResponse struct {
	Automations []projectautomation.Automation `json:"automations"`
}

type runListResponse struct {
	AutomationRuns []projectautomation.AutomationRun `json:"automation_runs"`
}

func RegisterRoutes(mux *http.ServeMux, svc *projectautomation.Service) {
	if svc == nil {
		return
	}
	mux.Handle("POST /api/v1/projects/{id}/automations", createAutomationHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/automations", listAutomationsHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/automations/{automation_id}", getAutomationHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/automations/{automation_id}/status", updateAutomationStatusHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/automations/{automation_id}/runs", submitRunHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/automations/{automation_id}/parallel-batches", computeParallelBatchHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/automation-runs", listRunsHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/automation-runs/claim-next", claimNextRunHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/automation-runs/{run_id}", getRunHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/automation-runs/{run_id}/attempt-result", completeAttemptHandler(svc))
}

func createAutomationHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectautomation.CreateAutomationInput
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		input.ProjectID = projectID(r)
		automation, err := svc.CreateAutomation(r.Context(), input)
		writeResult(w, automation, err, http.StatusCreated)
	})
}

func listAutomationsHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		automations, err := svc.ListAutomations(r.Context(), projectautomation.AutomationFilter{
			ProjectID: projectID(r),
			Status:    strings.TrimSpace(r.URL.Query().Get("status")),
			AgentID:   strings.TrimSpace(r.URL.Query().Get("agent_id")),
		})
		writeResult(w, automationListResponse{Automations: automations}, err, http.StatusOK)
	})
}

func getAutomationHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		automation, err := svc.GetAutomation(r.Context(), projectID(r), r.PathValue("automation_id"))
		writeResult(w, automation, err, http.StatusOK)
	})
}

func updateAutomationStatusHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectautomation.UpdateAutomationStatusInput
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		input.ProjectID = projectID(r)
		input.AutomationID = r.PathValue("automation_id")
		automation, err := svc.UpdateAutomationStatus(r.Context(), input)
		writeResult(w, automation, err, http.StatusOK)
	})
}

func submitRunHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectautomation.SubmitRunInput
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		input.ProjectID = projectID(r)
		input.AutomationID = r.PathValue("automation_id")
		run, err := svc.RunNow(r.Context(), input)
		writeResult(w, run, err, http.StatusCreated)
	})
}

func computeParallelBatchHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectautomation.ComputeParallelBatchInput
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		input.ProjectID = projectID(r)
		batch, err := svc.ComputeParallelBatch(r.Context(), input)
		writeResult(w, batch, err, http.StatusCreated)
	})
}

func listRunsHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runs, err := svc.ListRuns(r.Context(), projectautomation.RunFilter{
			ProjectID:    projectID(r),
			AutomationID: strings.TrimSpace(r.URL.Query().Get("automation_id")),
			Status:       strings.TrimSpace(r.URL.Query().Get("status")),
		})
		writeResult(w, runListResponse{AutomationRuns: runs}, err, http.StatusOK)
	})
}

func claimNextRunHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectautomation.ClaimNextRunInput
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		input.ProjectID = projectID(r)
		claimed, err := svc.ClaimNextRun(r.Context(), input)
		writeResult(w, claimed, err, http.StatusOK)
	})
}

func getRunHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run, err := svc.GetRun(r.Context(), projectID(r), r.PathValue("run_id"))
		writeResult(w, run, err, http.StatusOK)
	})
}

func completeAttemptHandler(svc *projectautomation.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectautomation.CompleteAttemptInput
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		input.ProjectID = projectID(r)
		input.RunID = r.PathValue("run_id")
		run, err := svc.CompleteAttempt(r.Context(), input)
		writeResult(w, run, err, http.StatusOK)
	})
}

func projectID(r *http.Request) string {
	return r.PathValue("id")
}

func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/json") {
		return true
	}
	writeResult(w, nil, errInvalidJSON, http.StatusOK)
	return false
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return err
	}
	return nil
}

func writeInvalidJSON(w http.ResponseWriter) {
	writeResult(w, nil, errInvalidJSON, http.StatusOK)
}

func writeResult(w http.ResponseWriter, value any, err error, successStatus int) {
	if err != nil {
		status := statusCode(err)
		httpserver.WriteJSON(w, status, map[string]any{
			"error": map[string]string{
				"code":    errorCode(err),
				"message": safeErrorMessage(err),
			},
		})
		return
	}
	if successStatus == 0 {
		successStatus = http.StatusOK
	}
	httpserver.WriteJSON(w, successStatus, value)
}

func statusCode(err error) int {
	switch {
	case errors.Is(err, automationstore.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, automationstore.ErrDuplicate):
		return http.StatusConflict
	case errors.Is(err, errInvalidJSON):
		return http.StatusUnsupportedMediaType
	case errors.Is(err, projectautomation.ErrInvalidInput):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, automationstore.ErrNotFound):
		return "not_found"
	case errors.Is(err, automationstore.ErrDuplicate):
		return "conflict"
	case errors.Is(err, errInvalidJSON):
		return "invalid_json"
	case errors.Is(err, projectautomation.ErrInvalidInput):
		return "invalid_input"
	default:
		return "internal_error"
	}
}

func safeErrorMessage(err error) string {
	switch statusCode(err) {
	case http.StatusBadRequest, http.StatusConflict, http.StatusNotFound, http.StatusUnsupportedMediaType:
		return err.Error()
	default:
		return "internal server error"
	}
}
