package mcpscans

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordedSpecializedTaskCreator struct {
	calls  int
	inputs []tasks.SpecializedCreateInput
	err    error
}

func (creator *recordedSpecializedTaskCreator) CreateSpecializedInUnitOfWork(
	ctx context.Context,
	subject identity.Subject,
	input tasks.SpecializedCreateInput,
) (*tasks.Task, error) {
	if !audit.InGovernedMutation(ctx) {
		return nil, errors.New("specialized task creator was called outside the governed mutation")
	}
	creator.calls++
	creator.inputs = append(creator.inputs, input)
	if creator.err != nil {
		return nil, creator.err
	}
	return &tasks.Task{
		ID: input.TaskID, OwnerUserID: subject.UserID, TaskType: "mcp_scan", Params: append(json.RawMessage(nil), input.Params...),
		Status: tasks.StatusPending,
	}, nil
}

type recordedTaskBindingRepository struct {
	calls    int
	bindings []*mcpconnections.TaskBinding
	err      error
}

func (repository *recordedTaskBindingRepository) CreateTaskBinding(_ context.Context, binding *mcpconnections.TaskBinding) error {
	repository.calls++
	if repository.err != nil {
		return repository.err
	}
	copy := *binding
	copy.EncryptedRepositoryURL = append([]byte(nil), binding.EncryptedRepositoryURL...)
	copy.RepositoryURLNonce = append([]byte(nil), binding.RepositoryURLNonce...)
	if binding.ConnectionConfigID != nil {
		value := *binding.ConnectionConfigID
		copy.ConnectionConfigID = &value
	}
	if binding.ConnectionConfigVersion != nil {
		value := *binding.ConnectionConfigVersion
		copy.ConnectionConfigVersion = &value
	}
	repository.bindings = append(repository.bindings, &copy)
	return nil
}

type recordedTaskConnectionLocker struct {
	calls     int
	configID  string
	version   int
	reference mcpconnections.TaskConnectionReference
	err       error
}

func (locker *recordedTaskConnectionLocker) LockTaskConnectionForCreate(
	_ context.Context,
	_ identity.Subject,
	configID string,
	version int,
) (mcpconnections.TaskConnectionReference, error) {
	locker.calls++
	locker.configID, locker.version = configID, version
	if locker.err != nil {
		return mcpconnections.TaskConnectionReference{}, locker.err
	}
	return locker.reference, nil
}

type recordedRepositorySourcePolicy struct {
	requireCalls  int
	validateCalls int
	gitURL        string
	requireErr    error
	validateErr   error
}

func (policy *recordedRepositorySourcePolicy) RequireControlledDialer() error {
	policy.requireCalls++
	return policy.requireErr
}

func (policy *recordedRepositorySourcePolicy) ValidateGitURL(_ context.Context, raw string) error {
	policy.validateCalls++
	policy.gitURL = raw
	return policy.validateErr
}

type recordedPostCommitDispatcher struct {
	calls       int
	taskID      string
	bindingRepo *recordedTaskBindingRepository
	err         error
}

func (dispatcher *recordedPostCommitDispatcher) DispatchMCPAfterCommit(_ context.Context, _ identity.Subject, taskID string) error {
	dispatcher.calls++
	dispatcher.taskID = taskID
	if len(dispatcher.bindingRepo.bindings) == 0 {
		return errors.New("dispatch ran before task binding was persisted")
	}
	return dispatcher.err
}

type unitOfWorkFixture struct {
	workflow     *CreateUnitOfWork
	keyring      *mcpconnections.Keyring
	tasks        *recordedSpecializedTaskCreator
	bindings     *recordedTaskBindingRepository
	connections  *recordedTaskConnectionLocker
	policy       *recordedRepositorySourcePolicy
	dispatcher   *recordedPostCommitDispatcher
	auditRecords *audit.MemoryRepository
}

func newUnitOfWorkFixture(t *testing.T) unitOfWorkFixture {
	t.Helper()
	keyring, err := mcpconnections.NewKeyring("mcp-scan-uow-test-key", []byte("01234567890123456789012345678901"), nil)
	require.NoError(t, err)
	auditRecords := audit.NewMemoryRepository()
	taskCreator := &recordedSpecializedTaskCreator{}
	bindingRepository := &recordedTaskBindingRepository{}
	connectionLocker := &recordedTaskConnectionLocker{
		reference: mcpconnections.TaskConnectionReference{ConnectionConfigID: "connection-reference-sentinel", ConnectionConfigVersion: 7},
	}
	policy := &recordedRepositorySourcePolicy{}
	dispatcher := &recordedPostCommitDispatcher{bindingRepo: bindingRepository}
	workflow := NewCreateUnitOfWork(CreateUnitOfWorkDependencies{
		Idempotency: idempotency.NewService(idempotency.NewMemoryRepository()),
		Audits:      audit.NewService(auditRecords),
		Tasks:       taskCreator,
		Bindings:    bindingRepository,
		Connections: connectionLocker,
		Keyring:     keyring,
		Policy:      policy,
		Dispatcher:  dispatcher,
	})
	return unitOfWorkFixture{
		workflow: workflow, keyring: keyring, tasks: taskCreator, bindings: bindingRepository,
		connections: connectionLocker, policy: policy, dispatcher: dispatcher, auditRecords: auditRecords,
	}
}

func TestCreateUnitOfWorkSealsRepositorySourceAndReplaysWithoutRedispatch(t *testing.T) {
	ctx := context.Background()
	fixture := newUnitOfWorkFixture(t)
	subject := identity.Subject{UserID: "repository-owner", Username: "repository-user", Role: identity.RoleUser}
	input := CreateInput{
		IdempotencyKey: "repository-create-key", SourceKind: SourceKindRepository,
		RepositoryURL: "https://git.allowed.example.test/team/private-repository.git", ModelID: "governed-model", Thread: pointerTo(4),
	}

	first, err := fixture.workflow.Create(ctx, subject, input)
	require.NoError(t, err)
	assert.False(t, first.Replay)
	assert.Equal(t, tasks.StatusPending, first.Status)
	require.Len(t, fixture.tasks.inputs, 1)
	assert.JSONEq(t, `{"source_kind":"repository","model_id":"governed-model","thread":4}`, string(fixture.tasks.inputs[0].Params))
	require.Len(t, fixture.bindings.bindings, 1)
	binding := fixture.bindings.bindings[0]
	assert.Equal(t, "repository", binding.SourceKind)
	assert.Nil(t, binding.ConnectionConfigID)
	assert.NotEmpty(t, binding.EncryptedRepositoryURL)
	opened, openErr := fixture.keyring.OpenRepositorySource(binding, mcpconnections.BindingEncryptionContext{
		OwnerUserID: subject.UserID, Scope: mcpconnections.ScopePrivate, Version: 1,
	})
	require.NoError(t, openErr)
	assert.Equal(t, input.RepositoryURL, opened.RepositoryURL)
	encodedBinding, encodeErr := json.Marshal(binding)
	require.NoError(t, encodeErr)
	assert.NotContains(t, string(encodedBinding), input.RepositoryURL)
	assert.Equal(t, 1, fixture.policy.requireCalls)
	assert.Equal(t, 1, fixture.policy.validateCalls)
	assert.Equal(t, input.RepositoryURL, fixture.policy.gitURL)
	assert.Equal(t, 1, fixture.dispatcher.calls)
	assert.Equal(t, first.TaskID, fixture.dispatcher.taskID)

	replayed, err := fixture.workflow.Create(ctx, subject, input)
	require.NoError(t, err)
	assert.True(t, replayed.Replay)
	assert.Equal(t, first.TaskID, replayed.TaskID)
	assert.Equal(t, 1, fixture.tasks.calls)
	assert.Equal(t, 1, fixture.bindings.calls)
	assert.Equal(t, 1, fixture.dispatcher.calls, "a replay cannot schedule a second dispatch")
	events, eventErr := fixture.auditRecords.List(ctx, audit.Filter{})
	require.NoError(t, eventErr)
	assert.Len(t, events, 2, "only the first mutation writes audit intent and completion")
}

func TestCreateUnitOfWorkReplaysBeforeRepositoryPolicyCheck(t *testing.T) {
	ctx := context.Background()
	fixture := newUnitOfWorkFixture(t)
	subject := identity.Subject{UserID: "replay-policy-owner", Username: "replay-policy-user", Role: identity.RoleUser}
	input := CreateInput{
		IdempotencyKey: "replay-before-policy-key", SourceKind: SourceKindRepository,
		RepositoryURL: "https://git.allowed.example.test/team/replay-repository.git",
	}

	first, err := fixture.workflow.Create(ctx, subject, input)
	require.NoError(t, err)
	assert.False(t, first.Replay)
	assert.Equal(t, 1, fixture.policy.requireCalls)

	// A previously committed, safe replay must not depend on the current
	// gateway/DNS policy. That policy governs a fresh source binding only.
	fixture.policy.requireErr = mcpconnections.ErrControlledEgressRequired
	replayed, err := fixture.workflow.Create(ctx, subject, input)
	require.NoError(t, err)
	assert.True(t, replayed.Replay)
	assert.Equal(t, first.TaskID, replayed.TaskID)
	assert.Equal(t, 1, fixture.policy.requireCalls)
	assert.Equal(t, 1, fixture.tasks.calls)
	assert.Equal(t, 1, fixture.bindings.calls)
	assert.Equal(t, 1, fixture.dispatcher.calls)
}

func TestCreateUnitOfWorkUsesLockedServiceConnectionReferenceOnly(t *testing.T) {
	ctx := context.Background()
	fixture := newUnitOfWorkFixture(t)
	subject := identity.Subject{UserID: "service-owner", Username: "service-user", Role: identity.RoleUser}
	input := CreateInput{
		IdempotencyKey: "service-create-key", SourceKind: SourceKindService,
		ConnectionConfigID: "connection-reference-sentinel", ConnectionConfigVersion: 7, AuthorizationConfirmed: true,
		ModelID: "governed-model",
	}

	created, err := fixture.workflow.Create(ctx, subject, input)
	require.NoError(t, err)
	assert.False(t, created.Replay)
	assert.Equal(t, 1, fixture.connections.calls)
	assert.Equal(t, input.ConnectionConfigID, fixture.connections.configID)
	assert.Equal(t, input.ConnectionConfigVersion, fixture.connections.version)
	require.Len(t, fixture.bindings.bindings, 1)
	binding := fixture.bindings.bindings[0]
	assert.Equal(t, "service", binding.SourceKind)
	require.NotNil(t, binding.ConnectionConfigID)
	require.NotNil(t, binding.ConnectionConfigVersion)
	assert.Equal(t, input.ConnectionConfigID, *binding.ConnectionConfigID)
	assert.Equal(t, input.ConnectionConfigVersion, *binding.ConnectionConfigVersion)
	assert.Empty(t, binding.EncryptedRepositoryURL)
	require.Len(t, fixture.tasks.inputs, 1)
	assert.JSONEq(t, `{"source_kind":"service","model_id":"governed-model","authorization_confirmed":true}`, string(fixture.tasks.inputs[0].Params))
	assert.NotContains(t, string(fixture.tasks.inputs[0].Params), input.ConnectionConfigID)
	assert.Zero(t, fixture.policy.requireCalls, "service source delegates controlled-egress validation to the locked connection service")
}

func TestCreateUnitOfWorkDoesNotPersistIdempotencySuccessOrDispatchWhenBindingFails(t *testing.T) {
	ctx := context.Background()
	fixture := newUnitOfWorkFixture(t)
	fixture.bindings.err = errors.New("binding persistence failed")
	subject := identity.Subject{UserID: "failure-owner", Username: "failure-user", Role: identity.RoleUser}
	input := CreateInput{
		IdempotencyKey: "binding-failure-key", SourceKind: SourceKindRepository,
		RepositoryURL: "https://git.allowed.example.test/team/failure-repository.git",
	}

	_, err := fixture.workflow.Create(ctx, subject, input)
	require.Error(t, err)
	assert.Equal(t, 1, fixture.tasks.calls)
	assert.Equal(t, 1, fixture.bindings.calls)
	assert.Zero(t, fixture.dispatcher.calls)

	fixture.bindings.err = nil
	retried, err := fixture.workflow.Create(ctx, subject, input)
	require.NoError(t, err)
	assert.False(t, retried.Replay, "a failed transaction cannot leave a successful replay record")
	assert.Equal(t, 2, fixture.tasks.calls)
	assert.Equal(t, 2, fixture.bindings.calls)
	assert.Equal(t, 1, fixture.dispatcher.calls)
}

func pointerTo(value int) *int { return &value }
