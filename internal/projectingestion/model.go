package projectingestion

import "time"

type Trigger string

const (
	TriggerManual Trigger = "manual"
	TriggerLive   Trigger = "live"
)

type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusCompleted RunStatus = "completed"
	RunStatusFailed    RunStatus = "failed"
)

type Run struct {
	ID            string
	ProjectID     string
	Trigger       Trigger
	Mode          string
	Status        RunStatus
	FilesSeen     int
	FilesIngested int
	FilesSkipped  int
	ChunksStored  int
	SymbolsStored int
	ErrorCategory string
	ReasonCounts  map[string]int
	StartedAt     time.Time
	FinishedAt    time.Time
}

type FileStatus string

const (
	FileStatusEligible FileStatus = "eligible"
	FileStatusSkipped  FileStatus = "skipped"
	FileStatusAbsent   FileStatus = "absent"
)

type SkipReason string

const (
	SkipReasonNone              SkipReason = ""
	SkipReasonUnsafePath        SkipReason = "unsafe_path"
	SkipReasonDeniedPath        SkipReason = "denied_path"
	SkipReasonFileTooLarge      SkipReason = "file_too_large"
	SkipReasonBinaryContent     SkipReason = "binary_content"
	SkipReasonNULByte           SkipReason = "nul_byte"
	SkipReasonInvalidUTF8       SkipReason = "invalid_utf8"
	SkipReasonSensitiveContent  SkipReason = "sensitive_content"
	SkipReasonUnsupportedPolicy SkipReason = "unsupported_policy"
	SkipReasonStatError         SkipReason = "stat_error"
	SkipReasonReadError         SkipReason = "read_error"
	SkipReasonChunkError        SkipReason = "chunk_error"
	SkipReasonParseError        SkipReason = "parse_error"
)

type FileState struct {
	ProjectID        string
	RelativePathHash string
	RelativePath     string
	RelativePathSafe bool
	Status           FileStatus
	Present          bool
	ContentSHA256    string
	SizeBytes        int64
	ModifiedAt       time.Time
	LastEventAt      time.Time
	LastIngestedAt   time.Time
	SkippedReason    SkipReason
}

type Chunk struct {
	ID            string
	FileID        string
	Index         int
	RelativePath  string
	StartLine     int
	EndLine       int
	ByteStart     int
	ByteEnd       int
	Text          string
	ContentSHA256 string
}

type SymbolKind string

const (
	SymbolKindPackage   SymbolKind = "package"
	SymbolKindImport    SymbolKind = "import"
	SymbolKindFunction  SymbolKind = "function"
	SymbolKindMethod    SymbolKind = "method"
	SymbolKindType      SymbolKind = "type"
	SymbolKindClass     SymbolKind = "class"
	SymbolKindExport    SymbolKind = "export"
	SymbolKindStage     SymbolKind = "stage"
	SymbolKindTarget    SymbolKind = "target"
	SymbolKindPath      SymbolKind = "path"
	SymbolKindKey       SymbolKind = "key"
	SymbolKindMigration SymbolKind = "migration"
)

type Symbol struct {
	Kind        SymbolKind
	Name        string
	PackageName string
	ImportPath  string
	Receiver    string
	StartLine   int
	EndLine     int
}

type Heading struct {
	Level       int
	Text        string
	ParentIndex int
	StartLine   int
	EndLine     int
}

type Finding struct {
	Code     SkipReason
	Category string
}
