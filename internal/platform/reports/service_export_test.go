package reports

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGovernedServiceListsAndReadsOnlyReportsAllowedForSubject(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	brands := brand.NewService(brand.NewMemoryRepository())
	service := NewGovernedService(repository, brands, audit.NewService(audit.NewMemoryRepository()), nil, nil)
	putGovernedSnapshot(t, repository, "alice-report", "task-alice", "alice")
	putGovernedSnapshot(t, repository, "bob-report", "task-bob", "bob")

	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	listed, err := service.List(ctx, alice, 1, 20)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "alice-report", listed[0].ID)
	_, err = service.Get(ctx, alice, "bob-report")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = service.Trend(ctx, alice, 30, time.Now().UTC())
	require.NoError(t, err)

	for _, subject := range []identity.Subject{{Role: identity.RoleAuditor}, {Role: identity.RoleAdmin}} {
		listed, err = service.List(ctx, subject, 1, 20)
		require.NoError(t, err)
		assert.Len(t, listed, 2)
		_, err = service.Get(ctx, subject, "bob-report")
		require.NoError(t, err)
	}
	_, err = service.List(ctx, identity.Subject{}, 1, 20)
	assert.ErrorIs(t, err, ErrForbidden)
}

func TestGovernedServiceUsesOwnerScopedRepositoryReadBeforeLoadingSnapshot(t *testing.T) {
	ctx := context.Background()
	repository := &ownerScopedRepository{delegate: NewMemoryRepository()}
	putGovernedSnapshot(t, repository.delegate, "alice-report", "alice-task", "alice")
	service := NewGovernedService(repository, brand.NewService(brand.NewMemoryRepository()), audit.NewService(audit.NewMemoryRepository()), nil, nil)

	_, err := service.Get(ctx, identity.Subject{UserID: "bob", Role: identity.RoleUser}, "alice-report")
	require.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, 1, repository.visibleGets)
	assert.Equal(t, 0, repository.unscopedGets, "cross-owner snapshots must not be loaded before authorization")
}

func TestGovernedServicePDFRetriesRenderSameSnapshotAndAuditsBothOutcomes(t *testing.T) {
	ctx := context.Background()
	repository := &countingRepository{delegate: NewMemoryRepository()}
	putGovernedSnapshot(t, repository.delegate, "report-1", "task-1", "alice")
	audits := audit.NewMemoryRepository()
	renderer := &retryRenderer{err: errors.New("renderer sentinel /private/token")}
	service := NewGovernedService(repository, brand.NewService(brand.NewMemoryRepository()), audit.NewService(audits), renderer, nil)
	subject := identity.Subject{UserID: "alice", Role: identity.RoleUser}

	_, err := service.ExportPDF(ctx, subject, "report-1")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "sentinel")
	assert.NotContains(t, err.Error(), "token")
	pdf, err := service.ExportPDF(ctx, subject, "report-1")
	require.NoError(t, err)
	assert.Equal(t, []byte("%PDF-retry"), pdf)
	require.Len(t, renderer.snapshots, 2)
	assert.Equal(t, renderer.snapshots[0].ID, renderer.snapshots[1].ID)
	assert.Equal(t, renderer.snapshots[0].RawResult, renderer.snapshots[1].RawResult)
	assert.Equal(t, renderer.snapshots[0].RenderData, renderer.snapshots[1].RenderData)
	assert.Equal(t, renderer.snapshots[0].Brand, renderer.snapshots[1].Brand)
	assert.Equal(t, 2, repository.gets)

	events, err := audits.List(ctx, audit.Filter{Action: audit.ActionReportExported, ResourceID: "report-1"})
	require.NoError(t, err)
	assert.Contains(t, outcomes(events), audit.OutcomePending)
	assert.Contains(t, outcomes(events), audit.OutcomeFailure)
	assert.Contains(t, outcomes(events), audit.OutcomeSuccess)
	for _, event := range events {
		encoded := string(event.Metadata)
		assert.NotContains(t, encoded, "sentinel")
		assert.NotContains(t, encoded, "private")
		assert.NotContains(t, encoded, "token")
	}
}

func TestGovernedServicePDFReturnsAfterCompletionAppendFailureAndLeavesRecoverableOutbox(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	putGovernedSnapshot(t, repository, "report-append-fault", "task-append-fault", "alice")
	auditRepository := &appendFailingAuditRepository{delegate: audit.NewMemoryRepository(), failOn: 2}
	audits := audit.NewService(auditRepository)
	service := NewGovernedService(repository, brand.NewService(brand.NewMemoryRepository()), audits, &retryRenderer{}, nil)
	subject := identity.Subject{UserID: "alice", Username: "alice", Role: identity.RoleUser}

	pdf, err := service.ExportPDF(ctx, subject, "report-append-fault")
	require.NoError(t, err)
	assert.Equal(t, []byte("%PDF-retry"), pdf)
	pending, err := audits.PendingCompletions(ctx, identity.Subject{Role: identity.RoleAuditor}, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, audit.CompletionStateReady, pending[0].State)
	assert.Nil(t, pending[0].DeliveredAt)

	auditRepository.failOn = 0
	delivered, err := audits.Reconcile(ctx, identity.Subject{Role: identity.RoleAdmin}, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, delivered)
}

func TestGovernedServicePDFFailureLeavesPreparedCompletionWhenFailureFinalizationCannotPersist(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	putGovernedSnapshot(t, repository, "report-finalize-fault", "task-finalize-fault", "alice")
	auditRepository := &completionFailingAuditRepository{delegate: audit.NewMemoryRepository(), failReady: true}
	audits := audit.NewService(auditRepository)
	service := NewGovernedService(repository, brand.NewService(brand.NewMemoryRepository()), audits, &retryRenderer{err: errors.New("renderer sentinel")}, nil)
	subject := identity.Subject{UserID: "alice", Username: "alice", Role: identity.RoleUser}

	_, err := service.ExportPDF(ctx, subject, "report-finalize-fault")
	require.Error(t, err)
	pending, err := audits.PendingCompletions(ctx, identity.Subject{Role: identity.RoleAuditor}, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "render must start only after a durable prepared recovery intent exists")
	assert.Equal(t, audit.CompletionStatePrepared, pending[0].State)
	assert.Equal(t, audit.OutcomePending, pending[0].Outcome)

	auditRepository.failReady = false
	_, err = audits.FinalizePrepared(ctx, identity.Subject{Role: identity.RoleAdmin}, pending[0].RequestID, audit.OutcomeFailure, map[string]any{"format": "pdf"})
	require.NoError(t, err)
	delivered, err := audits.Reconcile(ctx, identity.Subject{Role: identity.RoleAdmin}, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, delivered)
}

func putGovernedSnapshot(t *testing.T, repository Repository, id, taskID, owner string) {
	t.Helper()
	now := time.Now().UTC()
	require.NoError(t, repository.Create(context.Background(), &Snapshot{ID: id, TaskID: taskID, OwnerUserID: owner, TaskType: "mcp_scan", CompletedAt: now, CreatedAt: now, RawResult: []byte(`{"findings":[]}`), RenderData: []byte(`{}`)}))
}

type countingRepository struct {
	delegate *MemoryRepository
	gets     int
}

type ownerScopedRepository struct {
	delegate     *MemoryRepository
	visibleGets  int
	unscopedGets int
}

func (repository *ownerScopedRepository) Create(ctx context.Context, snapshot *Snapshot) error {
	return repository.delegate.Create(ctx, snapshot)
}
func (repository *ownerScopedRepository) Get(ctx context.Context, id string) (*Snapshot, error) {
	repository.unscopedGets++
	return repository.delegate.Get(ctx, id)
}
func (repository *ownerScopedRepository) GetVisible(ctx context.Context, id, owner string) (*Snapshot, error) {
	repository.visibleGets++
	return repository.delegate.GetVisible(ctx, id, owner)
}
func (repository *ownerScopedRepository) GetByTaskID(ctx context.Context, id string) (*Snapshot, error) {
	return repository.delegate.GetByTaskID(ctx, id)
}
func (repository *ownerScopedRepository) List(ctx context.Context, query ListQuery) ([]Snapshot, error) {
	return repository.delegate.List(ctx, query)
}
func (repository *ownerScopedRepository) Trend(ctx context.Context, query TrendQuery) ([]TrendPoint, error) {
	return repository.delegate.Trend(ctx, query)
}

func (repository *countingRepository) Create(ctx context.Context, snapshot *Snapshot) error {
	return repository.delegate.Create(ctx, snapshot)
}
func (repository *countingRepository) Get(ctx context.Context, id string) (*Snapshot, error) {
	repository.gets++
	return repository.delegate.Get(ctx, id)
}
func (repository *countingRepository) GetByTaskID(ctx context.Context, id string) (*Snapshot, error) {
	return repository.delegate.GetByTaskID(ctx, id)
}
func (repository *countingRepository) Trend(ctx context.Context, query TrendQuery) ([]TrendPoint, error) {
	return repository.delegate.Trend(ctx, query)
}
func (repository *countingRepository) List(ctx context.Context, query ListQuery) ([]Snapshot, error) {
	return repository.delegate.List(ctx, query)
}

type retryRenderer struct {
	err       error
	snapshots []*Snapshot
}

type appendFailingAuditRepository struct {
	delegate *audit.MemoryRepository
	failOn   int
	calls    int
}

type completionFailingAuditRepository struct {
	delegate  *audit.MemoryRepository
	failReady bool
}

func (repository *appendFailingAuditRepository) Append(ctx context.Context, event *audit.Event) error {
	repository.calls++
	if repository.failOn > 0 && repository.calls == repository.failOn {
		return errors.New("injected audit append failure")
	}
	return repository.delegate.Append(ctx, event)
}
func (repository *appendFailingAuditRepository) List(ctx context.Context, filter audit.Filter) ([]audit.Event, error) {
	return repository.delegate.List(ctx, filter)
}
func (repository *appendFailingAuditRepository) EnqueueCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.EnqueueCompletion(ctx, completion)
}
func (repository *appendFailingAuditRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}
func (repository *appendFailingAuditRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}
func (repository *appendFailingAuditRepository) ListReadyCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListReadyCompletions(ctx, limit)
}
func (repository *appendFailingAuditRepository) UpdateCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}
func (repository *appendFailingAuditRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func (repository *completionFailingAuditRepository) Append(ctx context.Context, event *audit.Event) error {
	return repository.delegate.Append(ctx, event)
}
func (repository *completionFailingAuditRepository) List(ctx context.Context, filter audit.Filter) ([]audit.Event, error) {
	return repository.delegate.List(ctx, filter)
}
func (repository *completionFailingAuditRepository) EnqueueCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	if repository.failReady && completion.State == audit.CompletionStateReady {
		return errors.New("injected completion persistence failure")
	}
	return repository.delegate.EnqueueCompletion(ctx, completion)
}
func (repository *completionFailingAuditRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}
func (repository *completionFailingAuditRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}
func (repository *completionFailingAuditRepository) ListReadyCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListReadyCompletions(ctx, limit)
}
func (repository *completionFailingAuditRepository) UpdateCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}
func (repository *completionFailingAuditRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func (renderer *retryRenderer) Render(_ context.Context, snapshot *Snapshot) ([]byte, error) {
	renderer.snapshots = append(renderer.snapshots, cloneSnapshot(snapshot))
	if renderer.err != nil {
		err := renderer.err
		renderer.err = nil
		return nil, err
	}
	return []byte("%PDF-retry"), nil
}

func outcomes(events []audit.Event) []audit.Outcome {
	values := make([]audit.Outcome, 0, len(events))
	for _, event := range events {
		values = append(values, event.Outcome)
	}
	return values
}
