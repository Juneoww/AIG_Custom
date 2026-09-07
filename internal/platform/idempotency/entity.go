// Package idempotency provides the MCP-only request replay boundary.
// It deliberately stores a canonical payload hash and a narrow safe response,
// never a browser request body or connection material.
package idempotency

import (
	"encoding/json"
	"time"
)

// Scope is selected by trusted server-side code from the operation's ownership
// model. It is not a browser-supplied tenant or organization identifier.
type Scope string

const (
	ScopePrivate Scope = "private"
	ScopeGlobal  Scope = "global"
)

// Record is the durable, secret-free replay record for one MCP mutation key.
type Record struct {
	ID             string          `gorm:"primaryKey;column:id" json:"-"`
	PrincipalID    string          `gorm:"not null;column:principal_id" json:"-"`
	ScopeKey       string          `gorm:"not null;column:scope_key" json:"-"`
	Method         string          `gorm:"not null;column:method" json:"-"`
	Path           string          `gorm:"not null;column:path" json:"-"`
	IdempotencyKey string          `gorm:"not null;column:idempotency_key" json:"-"`
	PayloadHash    []byte          `gorm:"not null;column:payload_hash" json:"-"`
	StatusCode     int             `gorm:"not null;column:status_code" json:"-"`
	SafeResponse   json.RawMessage `gorm:"type:jsonb;not null;column:safe_response" json:"-"`
	ExpiresAt      time.Time       `gorm:"not null;column:expires_at" json:"-"`
	CreatedAt      time.Time       `gorm:"not null;column:created_at" json:"-"`
}

func (Record) TableName() string { return "platform_idempotency_records" }

// Operation contains only the identity inputs used to find a replay record.
// Payload is canonicalized and hashed before it reaches persistence.
type Operation struct {
	Scope   Scope
	Method  string
	Path    string
	Key     string
	Payload json.RawMessage
}

// SafeResponse is intentionally not a generic JSON envelope. A replayed MCP
// creation request can expose only its opaque task ID and a safe task status.
type SafeResponse struct {
	TaskID           string `json:"task_id,omitempty"`
	Status           string `json:"status"`
	ID               string `json:"id,omitempty"`
	CurrentVersion   int    `json:"current_version,omitempty"`
	ResourceRevision string `json:"resource_revision,omitempty"`
	AttachmentID     string `json:"attachment_id,omitempty"`
	Size             int64  `json:"size,omitempty"`
}

// Result is returned to the future MCP handler without exposing record or
// request fields. Replay is true only when a previous successful result won.
type Result struct {
	StatusCode int          `json:"-"`
	Response   SafeResponse `json:"response"`
	Replay     bool         `json:"replay"`
}

type recordIdentity struct {
	principalID string
	scopeKey    string
	method      string
	path        string
	key         string
}
