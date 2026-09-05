package mcpegress

import "time"

// RuntimeCapability persists only a one-way digest of the short-lived token
// issued to an Agent. The raw token is deliberately never a database value or
// a JSON value; it exists only in the in-memory task-assignment payload.
type RuntimeCapability struct {
	ID             string    `gorm:"primaryKey;column:id" json:"-"`
	TaskID         string    `gorm:"not null;column:task_id" json:"-"`
	CapabilityHash []byte    `gorm:"not null;column:capability_hash" json:"-"`
	IssuedAt       time.Time `gorm:"not null;column:issued_at" json:"-"`
	ExpiresAt      time.Time `gorm:"not null;column:expires_at" json:"-"`
	Rotation       int       `gorm:"not null;column:rotation" json:"-"`
	Version        int       `gorm:"not null;column:version" json:"-"`
	CreatedAt      time.Time `gorm:"not null;column:created_at" json:"-"`
}

func (RuntimeCapability) TableName() string { return "platform_mcp_runtime_capabilities" }

// Runtime is the only MCP-specific material allowed to cross from the
// platform scheduler into the controlled Agent. It intentionally has no JSON
// representation so it cannot be accidentally persisted in a Session, task,
// audit record, log, or browser DTO.
type Runtime struct {
	MCPProxyURL        string `json:"-"`
	TaskCapability     string `json:"-"`
	EffectiveTransport string `json:"-"`
	ArchiveRef         string `json:"-"`
}

// String prevents a future structured or printf-style log statement from
// expanding the proxy capability or archive reference by accident.
func (Runtime) String() string { return "MCP 运行时 [REDACTED]" }

// GoString covers the %#v format, which deliberately bypasses Stringer.
func (Runtime) GoString() string { return "mcpegress.Runtime{[REDACTED]}" }

// TaskRuntimeParams creates the ephemeral assignment-only map consumed by the
// existing websocket adapter. Callers must not marshal or persist this map.
func (runtime Runtime) TaskRuntimeParams() map[string]any {
	params := make(map[string]any, 3)
	if runtime.MCPProxyURL != "" {
		params["mcp_proxy_url"] = runtime.MCPProxyURL
	}
	if runtime.TaskCapability != "" {
		params["task_capability"] = runtime.TaskCapability
	}
	if runtime.EffectiveTransport != "" {
		params["effective_transport"] = runtime.EffectiveTransport
	}
	if runtime.ArchiveRef != "" {
		params["archive_ref"] = runtime.ArchiveRef
	}
	return params
}

// RepositoryFetchRequest is an internal-only input for the controlled Git
// fetcher. RepositoryURL must never leave this package through JSON, errors,
// task params, or browser response objects.
type RepositoryFetchRequest struct {
	TaskID        string `json:"-"`
	OwnerUserID   string `json:"-"`
	RepositoryURL string `json:"-"`
}

// String keeps the decrypted repository URL and owner identity out of
// accidental printf-style logs in a Fetcher implementation.
func (RepositoryFetchRequest) String() string { return "MCP 仓库获取请求 [REDACTED]" }

// GoString covers the %#v format, which deliberately bypasses Stringer.
func (RepositoryFetchRequest) GoString() string {
	return "mcpegress.RepositoryFetchRequest{[REDACTED]}"
}
