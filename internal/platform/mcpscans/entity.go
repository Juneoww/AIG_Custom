package mcpscans

import "github.com/Juneoww/AIG_Custom/internal/platform/tasks"

const CreateOperationPath = "/api/v1/platform/mcp-scans"

type SourceKind string

const (
	SourceKindRepository SourceKind = "repository"
	SourceKindService    SourceKind = "service"
)

// CreateInput is the MCP-only creation contract. It has no task type,
// language, endpoint, credential, header, cookie, certificate, or runtime
// fields: those values are either fixed by the server or retained solely in
// the dedicated MCP connection/task-binding domains.
type CreateInput struct {
	IdempotencyKey string
	SourceKind     SourceKind

	RepositoryURL string
	AttachmentIDs []string

	ConnectionConfigID      string
	ConnectionConfigVersion int
	AuthorizationConfirmed  bool

	ModelID string
	Thread  *int
}

// CreateResult is safe to return to a future MCP HTTP handler. It contains no
// source URL, endpoint, connection metadata, attachment metadata, or engine
// session identifier.
type CreateResult struct {
	TaskID string       `json:"task_id"`
	Status tasks.Status `json:"status"`
	Replay bool         `json:"-"`
}
