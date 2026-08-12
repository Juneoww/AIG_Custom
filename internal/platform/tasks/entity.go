package tasks

import (
	"encoding/json"
	"time"
)

type Status string

const (
	StatusPending         Status = "pending"
	StatusDispatching     Status = "dispatching"
	StatusRunning         Status = "running"
	StatusSucceeded       Status = "succeeded"
	StatusEngineFailed    Status = "failed"
	StatusDispatchFailed  Status = "dispatch_failed"
	StatusDispatchUnknown Status = "dispatch_unknown"
	StatusCancelled       Status = "cancelled"
)

type Task struct {
	ID                 string          `gorm:"primaryKey;column:id" json:"id"`
	OwnerUserID        string          `gorm:"not null;column:owner_user_id" json:"owner_user_id"`
	OwnerUsername      string          `gorm:"not null;column:owner_username" json:"owner_username"`
	IdempotencyKey     string          `gorm:"not null;column:idempotency_key" json:"-"`
	EngineSessionID    string          `gorm:"not null;column:engine_session_id" json:"engine_session_id,omitempty"`
	TaskType           string          `gorm:"not null;column:task_type" json:"task_type"`
	Content            string          `gorm:"not null;column:content" json:"content"`
	Params             json.RawMessage `gorm:"type:jsonb;not null;column:params" json:"params,omitempty"`
	AttachmentRefs     json.RawMessage `gorm:"type:jsonb;not null;column:attachment_refs" json:"attachment_ids,omitempty"`
	CountryIsoCode     string          `gorm:"column:country_iso_code" json:"country_iso_code,omitempty"`
	Status             Status          `gorm:"not null;column:status" json:"status"`
	DispatchError      string          `gorm:"not null;column:dispatch_error" json:"dispatch_error,omitempty"`
	DispatchAttempts   int             `gorm:"not null;column:dispatch_attempts" json:"dispatch_attempts"`
	DispatchClaimToken string          `gorm:"not null;column:dispatch_claim_token" json:"-"`
	DispatchLeaseUntil *time.Time      `gorm:"column:dispatch_lease_until" json:"-"`
	CreatedAt          time.Time       `gorm:"not null;column:created_at" json:"created_at"`
	UpdatedAt          time.Time       `gorm:"not null;column:updated_at" json:"updated_at"`
}

func (Task) TableName() string { return "platform_tasks" }

type AttachmentState string

const (
	AttachmentStateUploading AttachmentState = "uploading"
	AttachmentStateReady     AttachmentState = "ready"
)

type Attachment struct {
	ID           string          `gorm:"primaryKey;column:id" json:"id"`
	OwnerUserID  string          `gorm:"not null;column:owner_user_id" json:"-"`
	OriginalName string          `gorm:"not null;column:original_name" json:"filename"`
	StorageName  string          `gorm:"not null;column:storage_name" json:"-"`
	Size         int64           `gorm:"not null;column:size" json:"size"`
	ChunkBytes   int64           `gorm:"not null;column:chunk_bytes" json:"-"`
	State        AttachmentState `gorm:"not null;column:state" json:"state"`
	CreatedAt    time.Time       `gorm:"not null;column:created_at" json:"created_at"`
	UpdatedAt    time.Time       `gorm:"not null;column:updated_at" json:"updated_at"`
}

func (Attachment) TableName() string { return "platform_attachments" }

type CreateInput struct {
	IdempotencyKey string          `json:"-"`
	TaskType       string          `json:"task_type"`
	Content        string          `json:"content"`
	Params         json.RawMessage `json:"params,omitempty"`
	AttachmentIDs  []string        `json:"attachment_ids,omitempty"`
	CountryIsoCode string          `json:"country_iso_code,omitempty"`
}

type View struct {
	ID               string          `json:"id"`
	OwnerUserID      string          `json:"owner_user_id"`
	OwnerUsername    string          `json:"owner_username"`
	EngineSessionID  string          `json:"engine_session_id,omitempty"`
	TaskType         string          `json:"task_type"`
	Content          string          `json:"content"`
	Params           json.RawMessage `json:"params,omitempty"`
	AttachmentIDs    []string        `json:"attachment_ids,omitempty"`
	CountryIsoCode   string          `json:"country_iso_code,omitempty"`
	Status           Status          `json:"status"`
	DispatchError    string          `json:"dispatch_error,omitempty"`
	DispatchAttempts int             `json:"dispatch_attempts"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

func viewOf(task *Task) View {
	var attachments []string
	_ = json.Unmarshal(task.AttachmentRefs, &attachments)
	return View{
		ID: task.ID, OwnerUserID: task.OwnerUserID, OwnerUsername: task.OwnerUsername,
		EngineSessionID: task.EngineSessionID, TaskType: task.TaskType, Content: task.Content,
		Params: append(json.RawMessage(nil), task.Params...), AttachmentIDs: attachments,
		CountryIsoCode: task.CountryIsoCode, Status: task.Status, DispatchError: task.DispatchError,
		DispatchAttempts: task.DispatchAttempts, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
}
