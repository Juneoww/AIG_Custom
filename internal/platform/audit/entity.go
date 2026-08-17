package audit

import (
	"encoding/json"
	"time"
)

type Action string

const (
	ActionLoginSuccess               Action = "authentication.login_succeeded"
	ActionLoginFailure               Action = "authentication.login_failed"
	ActionAccountCreated             Action = "identity.account_created"
	ActionAccountEnabled             Action = "identity.account_enabled"
	ActionAccountDisabled            Action = "identity.account_disabled"
	ActionRoleAssigned               Action = "identity.role_assigned"
	ActionPasswordResetRequested     Action = "identity.password_reset_requested"
	ActionTaskChanged                Action = "task.changed"
	ActionTaskCreated                Action = "task.created"
	ActionTaskCancelRequested        Action = "task.cancel_requested"
	ActionTaskCancelled              Action = "task.cancelled"
	ActionTaskDispatchFailed         Action = "task.dispatch_failed"
	ActionAttachmentCreated          Action = "attachment.created"
	ActionAttachmentChunkUploaded    Action = "attachment.chunk_uploaded"
	ActionAttachmentMerged           Action = "attachment.merged"
	ActionAttachmentDownloaded       Action = "attachment.downloaded"
	ActionReportExported             Action = "report.exported"
	ActionReportBackfilled           Action = "report.backfilled"
	ActionBrandUpdated               Action = "brand.updated"
	ActionSystemConfigurationChanged Action = "system.configuration_changed"
	ActionModelCreated               Action = "model.created"
	ActionModelUpdated               Action = "model.updated"
	ActionModelDeleted               Action = "model.deleted"
	ActionModelEncryptionRotated     Action = "model.encryption_rotated"
	ActionKnowledgeChangeRequested   Action = "knowledge.change_requested"
	ActionKnowledgeChanged           Action = "knowledge.changed"
)

type Outcome string

const (
	OutcomePending Outcome = "pending"
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

type CompletionState string

const (
	CompletionStatePrepared CompletionState = "prepared"
	CompletionStateReady    CompletionState = "ready"
)

// Event is append-only governance evidence. It deliberately has no secret
// fields and exposes no update/delete API through its repository.
type Event struct {
	ID            string          `gorm:"primaryKey;column:id" json:"id"`
	OccurredAt    time.Time       `gorm:"index;not null" json:"occurred_at"`
	ActorUserID   string          `gorm:"index" json:"actor_user_id,omitempty"`
	ActorUsername string          `gorm:"index" json:"actor_username,omitempty"`
	ActorRole     string          `gorm:"index" json:"actor_role,omitempty"`
	Action        Action          `gorm:"index;not null" json:"action"`
	ResourceType  string          `gorm:"index" json:"resource_type,omitempty"`
	ResourceID    string          `gorm:"index" json:"resource_id,omitempty"`
	Outcome       Outcome         `gorm:"index;not null" json:"outcome"`
	ClientIP      string          `json:"client_ip,omitempty"`
	RequestID     string          `gorm:"index" json:"request_id,omitempty"`
	Metadata      json.RawMessage `gorm:"type:jsonb;not null" json:"metadata"`
}

func (Event) TableName() string { return "audit_events" }

type EventInput struct {
	Action       Action
	ResourceType string
	ResourceID   string
	Outcome      Outcome
	ClientIP     string
	RequestID    string
	Metadata     map[string]any
}

type Filter struct {
	Action       Action
	ActorUserID  string
	ResourceType string
	ResourceID   string
	Limit        int
}

// CompletionOutbox durably stores a mutation result before the final
// append-only audit event is delivered. It contains only sanitized metadata.
type CompletionOutbox struct {
	ID            string          `gorm:"primaryKey;column:id" json:"id"`
	EventID       string          `gorm:"uniqueIndex;not null" json:"event_id"`
	RequestID     string          `gorm:"uniqueIndex;not null" json:"request_id"`
	ActorUserID   string          `gorm:"index" json:"actor_user_id,omitempty"`
	ActorUsername string          `gorm:"index" json:"actor_username,omitempty"`
	ActorRole     string          `gorm:"index" json:"actor_role,omitempty"`
	Action        Action          `gorm:"index;not null" json:"action"`
	ResourceType  string          `gorm:"index" json:"resource_type,omitempty"`
	ResourceID    string          `gorm:"index" json:"resource_id,omitempty"`
	Outcome       Outcome         `gorm:"index;not null" json:"outcome"`
	ClientIP      string          `json:"client_ip,omitempty"`
	Metadata      json.RawMessage `gorm:"type:jsonb;not null" json:"metadata"`
	State         CompletionState `gorm:"index;not null;default:ready" json:"state"`
	CreatedAt     time.Time       `gorm:"index;not null" json:"created_at"`
	ReadyAt       *time.Time      `gorm:"index" json:"ready_at,omitempty"`
	Attempts      int             `gorm:"not null;default:0" json:"attempts"`
	DeliveredAt   *time.Time      `gorm:"index" json:"delivered_at,omitempty"`
}

func (CompletionOutbox) TableName() string { return "audit_completion_outbox" }
