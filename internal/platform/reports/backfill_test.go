package reports

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGovernedServiceBackfillRestrictsSourceToAdminsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	audits := audit.NewMemoryRepository()
	source := &backfillSource{task: CompletedTask{TaskID: "task-1", OwnerUserID: "alice", TaskType: "mcp_scan", RawResult: json.RawMessage(event(`{"score":73,"results":[{"level":"high"},{"level":"medium"},{"level":"low"}]}`)), CompletedAt: time.Now().UTC()}}
	service := NewGovernedService(repository, brand.NewService(brand.NewMemoryRepository()), audit.NewService(audits), nil, source)

	for _, subject := range []identity.Subject{{UserID: "alice", Role: identity.RoleUser}, {Role: identity.RoleAuditor}} {
		_, err := service.Backfill(ctx, subject, "task-1")
		assert.ErrorIs(t, err, ErrForbidden)
	}
	assert.Zero(t, source.calls)
	_, err := repository.GetByTaskID(ctx, "task-1")
	assert.ErrorIs(t, err, ErrNotFound)
	events, err := audits.List(ctx, audit.Filter{Action: audit.ActionReportBackfilled})
	require.NoError(t, err)
	assert.Empty(t, events)

	admin := identity.Subject{UserID: "admin", Role: identity.RoleAdmin}
	created, err := service.Backfill(ctx, admin, "task-1")
	require.NoError(t, err)
	assert.Equal(t, "task-1", created.TaskID)
	assert.Equal(t, 1, source.calls)
	again, err := service.Backfill(ctx, admin, "task-1")
	require.NoError(t, err)
	assert.Equal(t, created.ID, again.ID)
	assert.Equal(t, 1, source.calls)
	events, err = audits.List(ctx, audit.Filter{Action: audit.ActionReportBackfilled, ResourceID: "task-1"})
	require.NoError(t, err)
	assert.Equal(t, []audit.Outcome{audit.OutcomePending, audit.OutcomeSuccess}, outcomes(events))
}

func TestGovernedServiceBackfillAuditsFailureWithoutRawResult(t *testing.T) {
	ctx := context.Background()
	audits := audit.NewMemoryRepository()
	source := &backfillSource{err: errors.New("source sentinel token /private/path")}
	service := NewGovernedService(NewMemoryRepository(), brand.NewService(brand.NewMemoryRepository()), audit.NewService(audits), nil, source)
	_, err := service.Backfill(ctx, identity.Subject{UserID: "admin", Role: identity.RoleAdmin}, "missing")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sentinel")
	events, listErr := audits.List(ctx, audit.Filter{Action: audit.ActionReportBackfilled, ResourceID: "missing"})
	require.NoError(t, listErr)
	assert.Contains(t, outcomes(events), audit.OutcomeFailure)
	for _, event := range events {
		assert.NotContains(t, string(event.Metadata), "sentinel")
		assert.NotContains(t, string(event.Metadata), "token")
	}
}

func TestGovernedServiceBackfillFailureLeavesPreparedCompletionWhenFailureFinalizationCannotPersist(t *testing.T) {
	ctx := context.Background()
	auditRepository := &completionFailingAuditRepository{delegate: audit.NewMemoryRepository(), failReady: true}
	audits := audit.NewService(auditRepository)
	service := NewGovernedService(
		NewMemoryRepository(), brand.NewService(brand.NewMemoryRepository()), audits, nil,
		&backfillSource{err: errors.New("source unavailable")},
	)

	_, err := service.Backfill(ctx, identity.Subject{UserID: "admin", Role: identity.RoleAdmin}, "missing-task")
	require.Error(t, err)
	pending, err := audits.PendingCompletions(ctx, identity.Subject{Role: identity.RoleAuditor}, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "trusted source reads must start only after a durable prepared recovery intent exists")
	assert.Equal(t, audit.CompletionStatePrepared, pending[0].State)
	assert.Equal(t, audit.OutcomePending, pending[0].Outcome)
}

type backfillSource struct {
	task  CompletedTask
	err   error
	calls int
}

func (source *backfillSource) GetCompletedTask(_ context.Context, _ string) (CompletedTask, error) {
	source.calls++
	if source.err != nil {
		return CompletedTask{}, source.err
	}
	return source.task, nil
}
