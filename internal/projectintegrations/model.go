package projectintegrations

import (
	"errors"
	"time"
)

var ErrInvalidInput = errors.New("invalid integration input")
var ErrNotFound = errors.New("integration resource not found")

type Provider string

const (
	ProviderJira       Provider = "jira"
	ProviderConfluence Provider = "confluence"
)

type SyncKind string

const (
	SyncKindInitialFull SyncKind = "initial_full"
	SyncKindIncremental SyncKind = "incremental"
)

type SyncRunStatus string

const (
	SyncRunStatusPending   SyncRunStatus = "pending"
	SyncRunStatusRunning   SyncRunStatus = "running"
	SyncRunStatusCompleted SyncRunStatus = "completed"
	SyncRunStatusFailed    SyncRunStatus = "failed"
	SyncRunStatusNoOp      SyncRunStatus = "no_op"
)

type SourceMetadataInput struct {
	ProjectID           string
	Provider            Provider
	SiteURL             string
	CloudID             string
	Allowlist           []string
	AuthMode            string
	IngestionEnabled    bool
	InitialFullSync     string
	IncrementalInterval time.Duration
	EmptyPollSleep      time.Duration
	MaxIdleSleep        time.Duration
	OverlapWindow       time.Duration
	InitialPageSize     int
	IncrementalPageSize int
	MaxResults          int
	UpdatedAt           time.Time
}

type SourceMetadata struct {
	ProjectID           string
	Provider            Provider
	SiteURLHash         string
	CloudIDHash         string
	AllowlistHash       string
	AllowlistCount      int
	AuthMode            string
	IngestionEnabled    bool
	InitialFullSync     string
	IncrementalInterval time.Duration
	EmptyPollSleep      time.Duration
	MaxIdleSleep        time.Duration
	OverlapWindow       time.Duration
	InitialPageSize     int
	IncrementalPageSize int
	MaxResults          int
	UpdatedAt           time.Time
}

type SyncRun struct {
	ID                   string
	ProjectID            string
	Provider             Provider
	Kind                 SyncKind
	Status               SyncRunStatus
	ItemsSeen            int
	ItemsUpserted        int
	ItemsChanged         int
	ItemsUnchanged       int
	RichContentChanged   int
	RichContentUnchanged int
	EmptyPoll            bool
	IdleSleep            time.Duration
	ErrorCategory        string
	StartedAt            time.Time
	FinishedAt           time.Time
}

type SyncStateInput struct {
	ProjectID             string
	Provider              Provider
	LastRunID             string
	LastSuccessfulRunID   string
	LastSuccessAt         time.Time
	LastFullSyncAt        time.Time
	LastIncrementalSyncAt time.Time
	LastEmptyPollAt       time.Time
	EmptyPollCount        int
	CurrentIdleSleep      time.Duration
	Cursor                string
	UpdatedAt             time.Time
}

type SyncState struct {
	ProjectID             string
	Provider              Provider
	LastRunID             string
	LastSuccessfulRunID   string
	LastSuccessAt         time.Time
	LastFullSyncAt        time.Time
	LastIncrementalSyncAt time.Time
	LastEmptyPollAt       time.Time
	EmptyPollCount        int
	CurrentIdleSleep      time.Duration
	Cursor                string
	CursorHash            string
	UpdatedAt             time.Time
}

type ItemMetadataInput struct {
	ProjectID       string
	Provider        Provider
	ItemID          string
	ItemKey         string
	ItemType        string
	ItemStatus      string
	ItemUpdatedAt   time.Time
	ProviderVersion string
	ProviderETag    string
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	LastRunID       string
}

type ItemMetadata struct {
	ProjectID       string
	Provider        Provider
	ItemID          string
	ItemKey         string
	ItemIDHash      string
	ItemKeyHash     string
	ItemType        string
	ItemStatus      string
	ItemUpdatedAt   time.Time
	ContentSHA256   string
	ProviderVersion string
	ProviderETag    string
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	LastRunID       string
	Changed         bool
}

type RichContentPayload struct {
	Item   RichContentItem
	Chunks []RichContentChunk
}
