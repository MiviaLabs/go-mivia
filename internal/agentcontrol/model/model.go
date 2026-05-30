package model

import "time"

const (
	TaskStatusPending = "pending"

	ResearchRunStatusPending = "pending"
)

type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateTaskInput struct {
	Title string `json:"title"`
}

type ResearchRun struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	GoalSummary string    `json:"goal_summary"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateResearchRunInput struct {
	TaskID      string `json:"task_id"`
	GoalSummary string `json:"goal_summary"`
}
