package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/MiviaLabs/go-mivia/internal/platform/httpserver"
	"github.com/MiviaLabs/go-mivia/internal/projectregistry"
	"github.com/MiviaLabs/go-mivia/internal/projectworkplan"
	workplanstore "github.com/MiviaLabs/go-mivia/internal/projectworkplan/store"
)

const (
	defaultPageSize = 50
	maxPageSize     = 100
)

var errInvalidJSON = errors.New("invalid json")

type planListResponse struct {
	WorkPlans     []projectworkplan.WorkPlan `json:"work_plans"`
	NextPageToken string                     `json:"next_page_token,omitempty"`
}

type taskListResponse struct {
	WorkTasks     []projectworkplan.WorkTask `json:"work_tasks"`
	NextPageToken string                     `json:"next_page_token,omitempty"`
}

// RegisterRoutes exposes project-scoped Work Plan and Work Task metadata routes.
func RegisterRoutes(mux *http.ServeMux, svc *projectworkplan.Service) {
	if svc == nil {
		return
	}
	mux.Handle("POST /api/v1/projects/{id}/work-plans", createWorkPlanHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/work-plans", listWorkPlansHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/work-plans/{plan_id}", getWorkPlanHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-plans/{plan_id}/status", updateWorkPlanStatusHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/work-plans/{plan_id}/resume", resumeWorkPlanHandler(svc))

	mux.Handle("POST /api/v1/projects/{id}/work-plans/{plan_id}/tasks", createWorkTaskHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/work-tasks", listWorkTasksHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/work-tasks/open", listOpenWorkTasksHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/work-tasks/mine", listMineWorkTasksHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/work-tasks/blocked", listBlockedWorkTasksHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/work-tasks/next", getNextWorkTaskHandler(svc))
	mux.Handle("GET /api/v1/projects/{id}/work-tasks/{task_id}", getWorkTaskHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/status", updateWorkTaskStatusHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/claim", claimWorkTaskHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/release", releaseWorkTaskHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/start", startWorkTaskHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/complete", completeWorkTaskHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/fail", failWorkTaskHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/block", blockWorkTaskHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/evidence", attachEvidenceHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/context-packs", attachContextPackHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/claims", attachClaimHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/verifier-results", attachVerifierResultHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/review-results", attachReviewResultHandler(svc))
	mux.Handle("POST /api/v1/projects/{id}/work-tasks/{task_id}/knowledge-candidates", promoteKnowledgeCandidateHandler(svc))
}

func createWorkPlanHandler(svc *projectworkplan.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectworkplan.CreateWorkPlanInput
		if err := decodeJSON(r, &input); err != nil {
			writeDecodeError(w, err)
			return
		}
		input.ProjectID = projectID(r)
		plan, err := svc.CreateWorkPlan(r.Context(), input)
		writeResult(w, plan, err, http.StatusCreated)
	})
}

func listWorkPlansHandler(svc *projectworkplan.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageSize, pageToken, err := pagination(r)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		filter := projectworkplan.WorkPlanFilter{
			ProjectID:  projectID(r),
			Status:     strings.TrimSpace(r.URL.Query().Get("status")),
			OwnerAgent: strings.TrimSpace(r.URL.Query().Get("owner_agent")),
		}
		plans, err := svc.ListWorkPlans(r.Context(), filter)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		writeResult(w, paginatePlans(plans, pageSize, pageToken), nil, http.StatusOK)
	})
}

func getWorkPlanHandler(svc *projectworkplan.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plan, err := svc.GetWorkPlan(r.Context(), projectID(r), planID(r))
		writeResult(w, plan, err, http.StatusOK)
	})
}

func updateWorkPlanStatusHandler(svc *projectworkplan.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectworkplan.UpdateWorkPlanStatusInput
		if err := decodeJSON(r, &input); err != nil {
			writeDecodeError(w, err)
			return
		}
		input.ProjectID = projectID(r)
		input.PlanID = planID(r)
		plan, err := svc.UpdateWorkPlanStatus(r.Context(), input)
		writeResult(w, plan, err, http.StatusOK)
	})
}

func resumeWorkPlanHandler(svc *projectworkplan.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := svc.ResumeWorkPlan(r.Context(), projectworkplan.ResumeWorkPlanInput{
			ProjectID: projectID(r),
			PlanID:    planID(r),
		})
		writeResult(w, result, err, http.StatusOK)
	})
}

func createWorkTaskHandler(svc *projectworkplan.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		var input projectworkplan.CreateWorkTaskInput
		if err := decodeJSON(r, &input); err != nil {
			writeDecodeError(w, err)
			return
		}
		input.ProjectID = projectID(r)
		input.PlanID = planID(r)
		task, err := svc.CreateWorkTask(r.Context(), input)
		writeResult(w, task, err, http.StatusCreated)
	})
}

func listWorkTasksHandler(svc *projectworkplan.Service) http.Handler {
	return listTasksHandler(func(r *http.Request, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
		if lister, ok := any(svc).(interface {
			ListWorkTasks(context.Context, projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error)
		}); ok {
			return lister.ListWorkTasks(r.Context(), filter)
		}
		return svc.ListOpenWorkTasks(r.Context(), filter)
	})
}

func listOpenWorkTasksHandler(svc *projectworkplan.Service) http.Handler {
	return listTasksHandler(func(r *http.Request, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
		return svc.ListOpenWorkTasks(r.Context(), filter)
	})
}

func listMineWorkTasksHandler(svc *projectworkplan.Service) http.Handler {
	return listTasksHandler(func(r *http.Request, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
		return svc.ListMineWorkTasks(r.Context(), filter)
	})
}

func listBlockedWorkTasksHandler(svc *projectworkplan.Service) http.Handler {
	return listTasksHandler(func(r *http.Request, filter projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error) {
		return svc.ListBlockedWorkTasks(r.Context(), filter)
	})
}

func listTasksHandler(list func(*http.Request, projectworkplan.WorkTaskFilter) ([]projectworkplan.WorkTask, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageSize, pageToken, err := pagination(r)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		filter := projectworkplan.WorkTaskFilter{
			ProjectID:      projectID(r),
			PlanID:         strings.TrimSpace(r.URL.Query().Get("plan_id")),
			Status:         strings.TrimSpace(r.URL.Query().Get("status")),
			OwnerAgent:     strings.TrimSpace(r.URL.Query().Get("owner_agent")),
			ClaimedByRunID: firstQuery(r, "claimed_by_run_id", "run_id"),
		}
		tasks, err := list(r, filter)
		if err != nil {
			writeResult(w, nil, err, http.StatusOK)
			return
		}
		writeResult(w, paginateTasks(tasks, pageSize, pageToken), nil, http.StatusOK)
	})
}

func getNextWorkTaskHandler(svc *projectworkplan.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := svc.GetNextWorkTask(r.Context(), projectworkplan.GetNextWorkTaskInput{
			ProjectID:          projectID(r),
			PlanID:             strings.TrimSpace(r.URL.Query().Get("plan_id")),
			OwnerAgent:         strings.TrimSpace(r.URL.Query().Get("owner_agent")),
			RunID:              strings.TrimSpace(r.URL.Query().Get("run_id")),
			TraceID:            strings.TrimSpace(r.URL.Query().Get("trace_id")),
			IncludeClaimedByMe: boolQuery(r, "include_claimed_by_me"),
		})
		writeResult(w, result, err, http.StatusOK)
	})
}

func getWorkTaskHandler(svc *projectworkplan.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task, err := svc.GetWorkTask(r.Context(), projectID(r), taskID(r))
		writeResult(w, task, err, http.StatusOK)
	})
}

func updateWorkTaskStatusHandler(svc *projectworkplan.Service) http.Handler {
	return actionHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.UpdateWorkTaskStatusInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.UpdateWorkTaskStatus(r.Context(), input)
	})
}

func claimWorkTaskHandler(svc *projectworkplan.Service) http.Handler {
	return actionHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.WorkTaskActionInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.ClaimWorkTask(r.Context(), input)
	})
}

func releaseWorkTaskHandler(svc *projectworkplan.Service) http.Handler {
	return actionHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.WorkTaskActionInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.ReleaseWorkTask(r.Context(), input)
	})
}

func startWorkTaskHandler(svc *projectworkplan.Service) http.Handler {
	return actionHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.WorkTaskActionInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.StartWorkTask(r.Context(), input)
	})
}

func completeWorkTaskHandler(svc *projectworkplan.Service) http.Handler {
	return actionHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.WorkTaskActionInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.CompleteWorkTask(r.Context(), input)
	})
}

func failWorkTaskHandler(svc *projectworkplan.Service) http.Handler {
	return actionHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.WorkTaskActionInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.FailWorkTask(r.Context(), input)
	})
}

func blockWorkTaskHandler(svc *projectworkplan.Service) http.Handler {
	return actionHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.WorkTaskActionInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.BlockWorkTask(r.Context(), input)
	})
}

func attachEvidenceHandler(svc *projectworkplan.Service) http.Handler {
	return attachHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.AttachInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.AttachEvidence(r.Context(), input)
	})
}

func attachContextPackHandler(svc *projectworkplan.Service) http.Handler {
	return attachHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.AttachInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.AttachContextPack(r.Context(), input)
	})
}

func attachClaimHandler(svc *projectworkplan.Service) http.Handler {
	return attachHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.AttachInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.AttachClaim(r.Context(), input)
	})
}

func attachVerifierResultHandler(svc *projectworkplan.Service) http.Handler {
	return attachHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.AttachInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.AttachVerifierResult(r.Context(), input)
	})
}

func attachReviewResultHandler(svc *projectworkplan.Service) http.Handler {
	return attachHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.AttachInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.AttachReviewResult(r.Context(), input)
	})
}

func promoteKnowledgeCandidateHandler(svc *projectworkplan.Service) http.Handler {
	return attachHandler(func(ctxInput actionContext, r *http.Request) (any, error) {
		var input projectworkplan.AttachInput
		if err := decodeJSON(r, &input); err != nil {
			return nil, err
		}
		input.ProjectID = ctxInput.projectID
		input.TaskID = ctxInput.taskID
		return svc.AttachKnowledgeCandidate(r.Context(), input)
	})
}

type actionContext struct {
	projectID string
	taskID    string
}

func actionHandler(call func(actionContext, *http.Request) (any, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		body, err := call(actionContext{projectID: projectID(r), taskID: taskID(r)}, r)
		if err != nil && isJSONDecodeError(err) {
			writeDecodeError(w, err)
			return
		}
		writeResult(w, body, err, http.StatusOK)
	})
}

func attachHandler(call func(actionContext, *http.Request) (any, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !requireJSON(w, r) {
			return
		}
		body, err := call(actionContext{projectID: projectID(r), taskID: taskID(r)}, r)
		if err != nil && isJSONDecodeError(err) {
			writeDecodeError(w, err)
			return
		}
		writeResult(w, body, err, http.StatusCreated)
	})
}

func writeResult(w http.ResponseWriter, body any, err error, successStatus int) {
	w.Header().Set("Cache-Control", "no-store")
	if err == nil {
		httpserver.WriteJSON(w, successStatus, body)
		return
	}
	if errors.Is(err, workplanstore.ErrNotFound) || errors.Is(err, projectregistry.ErrProjectNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "not_found", "project work plan resource not found")
		return
	}
	if errors.Is(err, workplanstore.ErrDuplicate) {
		httpserver.WriteError(w, http.StatusConflict, "conflict", "project work plan resource already exists")
		return
	}
	if errors.Is(err, projectworkplan.ErrInvalidInput) {
		httpserver.WriteError(w, http.StatusBadRequest, "invalid_project_workplan_request", "project work plan request is invalid")
		return
	}
	httpserver.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if httpserver.RequireJSON(r) {
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "content type must be application/json")
	return false
}

func writeInvalidJSON(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	httpserver.WriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
}

func writeDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, projectworkplan.ErrInvalidInput) {
		writeResult(w, nil, err, http.StatusOK)
		return
	}
	writeInvalidJSON(w)
}

func decodeJSON(r *http.Request, dst any) error {
	if err := httpserver.DecodeJSON(r, dst); err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			return fmt.Errorf("%w: request body contains unsupported field", projectworkplan.ErrInvalidInput)
		}
		return errInvalidJSON
	}
	return nil
}

func isJSONDecodeError(err error) bool {
	return errors.Is(err, errInvalidJSON)
}

func projectID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("id"))
}

func planID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("plan_id"))
}

func taskID(r *http.Request) string {
	return strings.TrimSpace(r.PathValue("task_id"))
}

func pagination(r *http.Request) (int, int, error) {
	pageSize := defaultPageSize
	rawPageSize := strings.TrimSpace(r.URL.Query().Get("page_size"))
	if rawPageSize != "" {
		parsed, err := strconv.Atoi(rawPageSize)
		if err != nil || parsed < 1 || parsed > maxPageSize {
			return 0, 0, fmt.Errorf("%w: page_size is invalid", projectworkplan.ErrInvalidInput)
		}
		pageSize = parsed
	}
	pageToken := 0
	rawPageToken := strings.TrimSpace(r.URL.Query().Get("page_token"))
	if rawPageToken != "" {
		parsed, err := strconv.Atoi(rawPageToken)
		if err != nil || parsed < 0 {
			return 0, 0, fmt.Errorf("%w: page_token is invalid", projectworkplan.ErrInvalidInput)
		}
		pageToken = parsed
	}
	return pageSize, pageToken, nil
}

func paginatePlans(plans []projectworkplan.WorkPlan, pageSize int, pageToken int) planListResponse {
	if pageToken >= len(plans) {
		return planListResponse{WorkPlans: []projectworkplan.WorkPlan{}}
	}
	end := pageToken + pageSize
	if end > len(plans) {
		end = len(plans)
	}
	out := planListResponse{WorkPlans: plans[pageToken:end]}
	if end < len(plans) {
		out.NextPageToken = strconv.Itoa(end)
	}
	return out
}

func paginateTasks(tasks []projectworkplan.WorkTask, pageSize int, pageToken int) taskListResponse {
	if pageToken >= len(tasks) {
		return taskListResponse{WorkTasks: []projectworkplan.WorkTask{}}
	}
	end := pageToken + pageSize
	if end > len(tasks) {
		end = len(tasks)
	}
	out := taskListResponse{WorkTasks: tasks[pageToken:end]}
	if end < len(tasks) {
		out.NextPageToken = strconv.Itoa(end)
	}
	return out
}

func firstQuery(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(r.URL.Query().Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func boolQuery(r *http.Request, name string) bool {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	return raw == "1" || strings.EqualFold(raw, "true") || strings.EqualFold(raw, "yes")
}
