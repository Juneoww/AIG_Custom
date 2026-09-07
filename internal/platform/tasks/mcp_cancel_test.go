package tasks

import (
	"context"
	"errors"
	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMCPCancelRollsBackStatusWhenReplayCompletionFails(t *testing.T) {
	db := openTaskSnapshotPostgresDB(t)
	repository := NewGormRepository(db)
	ctx := context.Background()
	_, _, err := repository.CreateOrGet(ctx, &Task{ID: "cancel-atomic", OwnerUserID: "alice", IdempotencyKey: "cancel-atomic", EngineSessionID: "cancel-atomic", TaskType: "mcp_scan", Status: StatusPending, Params: []byte(`{}`), AttachmentRefs: []byte(`[]`)})
	require.NoError(t, err)
	service := NewService(repository, &recordingEngine{}, audit.NewService(audit.NewGormRepository(db)))
	err = service.CancelMCPWithCompletion(ctx, identity.Subject{UserID: "alice", Role: identity.RoleUser}, "cancel-atomic", func(context.Context, Status) error { return errors.New("replay unavailable") })
	require.Error(t, err)
	task, err := repository.Get(ctx, "cancel-atomic")
	require.NoError(t, err)
	require.Equal(t, StatusPending, task.Status)
}
