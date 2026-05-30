package agentcontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	TaskStatusPending = "pending"
)

var ErrTaskNotFound = errors.New("task not found")

type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateTaskRequest struct {
	Title string `json:"title"`
}

type TaskStore interface {
	CreateTask(context.Context, Task) (Task, error)
	GetTask(context.Context, string) (Task, error)
}

type Service struct {
	store TaskStore
	now   func() time.Time
}

func NewService(store TaskStore) *Service {
	return &Service{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
}

func (svc *Service) CreateTask(ctx context.Context, req CreateTaskRequest) (Task, error) {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return Task{}, errors.New("title is required")
	}
	if len(title) > 200 {
		return Task{}, errors.New("title exceeds 200 characters")
	}
	task := Task{
		ID:        newTaskID(),
		Title:     title,
		Status:    TaskStatusPending,
		CreatedAt: svc.now(),
	}
	return svc.store.CreateTask(ctx, task)
}

func (svc *Service) GetTask(ctx context.Context, id string) (Task, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Task{}, errors.New("id is required")
	}
	return svc.store.GetTask(ctx, id)
}

type MemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]Task
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tasks: make(map[string]Task)}
}

func (store *MemoryStore) CreateTask(_ context.Context, task Task) (Task, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.tasks[task.ID] = task
	return task, nil
}

func (store *MemoryStore) GetTask(_ context.Context, id string) (Task, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	task, ok := store.tasks[id]
	if !ok {
		return Task{}, ErrTaskNotFound
	}
	return task, nil
}

func newTaskID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generate task id: %w", err))
	}
	return "task_" + hex.EncodeToString(b[:])
}
