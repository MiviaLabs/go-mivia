package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/projectworkflow"
	workflowstore "github.com/MiviaLabs/go-mivia/internal/projectworkflow/store"
)

var errInvalidJSON = errors.New("invalid json")
var errUnsupportedMediaType = errors.New("unsupported media type")

type workflowListResponse struct {
	Workflows []projectworkflow.WorkflowDefinition `json:"workflows"`
}

type agentDefinitionListResponse struct {
	AgentDefinitions []projectworkflow.WorkflowAgentDefinition `json:"agent_definitions"`
}

type permissionSnapshotListResponse struct {
	PermissionSnapshots []projectworkflow.WorkflowPermissionSnapshot `json:"permission_snapshots"`
}

type validateTOMLRequest struct {
	TOML string `json:"toml"`
}

type importTOMLRequest struct {
	TOML           string `json:"toml"`
	CreatedByRunID string `json:"created_by_run_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
}

type updateStatusRequest struct {
	Status string `json:"status"`
}

type compileRequest struct {
	UserRequestRef string `json:"user_request_ref,omitempty"`
	CreatedByRunID string `json:"created_by_run_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	TitleOverride  string `json:"title_override,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

// RegisterRoutes exposes project-scoped workflow metadata routes.
func RegisterRoutes(mux *http.ServeMux, svc *projectworkflow.Service) {
	if svc == nil {
		return
	}
	mux.Handle("POST /api/v1/projects/{id}/workflows/validate-toml", validateTOMLHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/workflows/import-toml", importTOMLHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/workflows", listWorkflowsHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/workflows/{workflow_id}", getWorkflowHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/workflows/{workflow_id}/status", updateStatusHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/workflows/{workflow_id}/compile", compileWorkflowHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/workflows/{workflow_id}/agent-definitions", listAgentDefinitionsHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/workflows/{workflow_id}/agent-definitions/{agent_id}", getAgentDefinitionHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/permission-snapshots", listPermissionSnapshotsHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/permission-snapshots/{snapshot_id}", getPermissionSnapshotHandler(svc))
}

func validateTOMLHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input validateTOMLRequest
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		result, err := svc.ValidateWorkflowTOML(r.Context(), projectworkflow.ValidateWorkflowTOMLInput{Data: []byte(input.TOML)})
		if err != nil {
			err = invalidWorkflowRequest("workflow TOML is invalid")
		}
		writeResult(w, result, err, http.StatusOK)
	})
}

func importTOMLHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input importTOMLRequest
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		result, err := svc.ImportWorkflowTOML(r.Context(), projectworkflow.ImportWorkflowTOMLInput{
			ProjectID:      projectID(r),
			Data:           []byte(input.TOML),
			CreatedByRunID: input.CreatedByRunID,
			TraceID:        input.TraceID,
		})
		if err != nil && !errors.Is(err, projectworkflow.ErrInvalidInput) {
			err = invalidWorkflowRequest("workflow TOML is invalid")
		}
		writeResult(w, result, err, http.StatusCreated)
	})
}

func listWorkflowsHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflows, err := svc.ListWorkflows(r.Context(), projectworkflow.WorkflowFilter{
			ProjectID:   projectID(r),
			Status:      strings.TrimSpace(r.URL.Query().Get("status")),
			WorkflowRef: strings.TrimSpace(r.URL.Query().Get("workflow_ref")),
		})
		writeResult(w, workflowListResponse{Workflows: workflows}, err, http.StatusOK)
	})
}

func getWorkflowHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflow, err := svc.GetWorkflow(r.Context(), projectID(r), workflowID(r))
		writeResult(w, workflow, err, http.StatusOK)
	})
}

func updateStatusHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input updateStatusRequest
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		workflow, err := svc.UpdateWorkflowStatus(r.Context(), projectworkflow.UpdateWorkflowStatusInput{
			ProjectID:  projectID(r),
			WorkflowID: workflowID(r),
			Status:     input.Status,
		})
		writeResult(w, workflow, err, http.StatusOK)
	})
}

func compileWorkflowHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input compileRequest
		if err := decodeJSON(r, &input); err != nil {
			writeInvalidJSON(w)
			return
		}
		result, err := svc.CompileWorkflow(r.Context(), projectworkflow.WorkflowCompileInput{
			ProjectID:      projectID(r),
			WorkflowID:     workflowID(r),
			UserRequestRef: input.UserRequestRef,
			CreatedByRunID: input.CreatedByRunID,
			TraceID:        input.TraceID,
			TitleOverride:  input.TitleOverride,
			DryRun:         input.DryRun,
		})
		writeResult(w, result, err, http.StatusOK)
	})
}

func listAgentDefinitionsHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflow, err := svc.GetWorkflow(r.Context(), projectID(r), workflowID(r))
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		writeResult(w, agentDefinitionListResponse{AgentDefinitions: workflow.Agents}, nil, http.StatusOK)
	})
}

func getAgentDefinitionHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workflow, err := svc.GetWorkflow(r.Context(), projectID(r), workflowID(r))
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		agentID := strings.TrimSpace(r.PathValue("agent_id"))
		for _, agent := range workflow.Agents {
			if agent.ID == agentID {
				writeResult(w, agent, nil, http.StatusOK)
				return
			}
		}
		writeResult(w, nil, workflowstore.ErrNotFound, http.StatusOK)
	})
}

func listPermissionSnapshotsHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, err := callWorkflowTool(r, svc, "projects.permission_snapshots.list", map[string]any{
			"id":          projectID(r),
			"workflow_id": strings.TrimSpace(r.URL.Query().Get("workflow_id")),
			"agent_id":    strings.TrimSpace(r.URL.Query().Get("agent_id")),
		})
		snapshots, _ := value.([]projectworkflow.WorkflowPermissionSnapshot)
		writeResult(w, permissionSnapshotListResponse{PermissionSnapshots: snapshots}, err, http.StatusOK)
	})
}

func getPermissionSnapshotHandler(svc *projectworkflow.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := callWorkflowTool(r, svc, "projects.permission_snapshots.get", map[string]any{
			"id":          projectID(r),
			"snapshot_id": strings.TrimSpace(r.PathValue("snapshot_id")),
		})
		writeResult(w, snapshot, err, http.StatusOK)
	})
}

func callWorkflowTool(r *http.Request, svc *projectworkflow.Service, name string, args map[string]any) (any, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return svc.CallWorkflowTool(r.Context(), name, raw)
}

func projectID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("id"))
}

func workflowID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("workflow_id"))
}

func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "application/json") {
		return true
	}
	writeResult(w, nil, errUnsupportedMediaType, http.StatusOK)
	return false
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("%w: trailing json", errInvalidJSON)
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
	case errors.Is(err, workflowstore.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, workflowstore.ErrDuplicate):
		return http.StatusConflict
	case errors.Is(err, errInvalidJSON):
		return http.StatusBadRequest
	case errors.Is(err, errUnsupportedMediaType):
		return http.StatusUnsupportedMediaType
	case errors.Is(err, projectworkflow.ErrInvalidInput):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, workflowstore.ErrNotFound):
		return "not_found"
	case errors.Is(err, workflowstore.ErrDuplicate):
		return "conflict"
	case errors.Is(err, errInvalidJSON):
		return "invalid_json"
	case errors.Is(err, errUnsupportedMediaType):
		return "unsupported_media_type"
	case errors.Is(err, projectworkflow.ErrInvalidInput):
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

func invalidWorkflowRequest(message string) error {
	return fmt.Errorf("%w: %s", projectworkflow.ErrInvalidInput, message)
}
