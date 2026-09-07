package reports

import (
	"context"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestTaskReportIDChecksOwnerAndExistence(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	require.NoError(t, repository.Create(ctx, reportSnapshotForTest(t, "report-1", "task-1", "alice", time.Now().UTC())))
	service := NewService(repository, nil)
	for _, subject := range []identity.Subject{{UserID: "alice", Role: identity.RoleUser}, {UserID: "auditor", Role: identity.RoleAuditor}, {UserID: "admin", Role: identity.RoleAdmin}} {
		id, err := service.TaskReportID(ctx, subject, "task-1")
		require.NoError(t, err)
		assert.Equal(t, "report-1", id)
	}
	_, err := service.TaskReportID(ctx, identity.Subject{UserID: "bob", Role: identity.RoleUser}, "task-1")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.TaskReportID(ctx, identity.Subject{UserID: "alice", Role: identity.RoleUser}, "missing")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.TaskReportID(ctx, identity.Subject{}, "task-1")
	require.ErrorIs(t, err, ErrForbidden)
}
