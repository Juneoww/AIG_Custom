package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type EngineState string

const (
	EngineStatePending   EngineState = "pending"
	EngineStateRunning   EngineState = "running"
	EngineStateSucceeded EngineState = "succeeded"
	EngineStateFailed    EngineState = "failed"
	EngineStateCancelled EngineState = "cancelled"
)

type EngineTask struct {
	PlatformTaskID string
	OwnerUsername  string
	TaskType       string
	Content        string
	Params         json.RawMessage
	Attachments    []string
	CountryIsoCode string
}

type EngineStatus struct {
	State       EngineState
	CompletedAt time.Time
}

type EngineAdapter interface {
	// ValidateTaskReferences verifies governed IDs before a browser task can be persisted.
	ValidateTaskReferences(context.Context, EngineTask) error
	// SubmitTask must treat PlatformTaskID as the stable engine session ID.
	// Repeated calls for an already assigned/running/terminal session must never
	// enqueue a second scan. If the original network acknowledgement cannot be
	// proven, it returns ErrSubmitAcknowledgementUnknown and relies on trusted
	// status/event reconciliation instead of resubmitting the assignment.
	SubmitTask(context.Context, EngineTask) (string, error)
	GetTaskStatus(context.Context, string) (EngineStatus, error)
	GetResult(context.Context, string) (json.RawMessage, error)
	CancelTask(context.Context, string) error
}

var (
	ErrEngineTaskNotFound           = errors.New("engine task not found")
	ErrResultNotReady               = errors.New("task result is not ready")
	ErrSubmitAcknowledgementUnknown = errors.New("engine submission acknowledgement is unknown")
)

type transientDispatchError struct{ cause error }

func (err *transientDispatchError) Error() string { return err.cause.Error() }
func (err *transientDispatchError) Unwrap() error { return err.cause }

func NewTransientDispatchError(err error) error {
	if err == nil {
		return nil
	}
	return &transientDispatchError{cause: err}
}

func isTransientDispatchError(err error) bool {
	var transient *transientDispatchError
	return errors.As(err, &transient) || errors.Is(err, ErrSubmitAcknowledgementUnknown)
}
