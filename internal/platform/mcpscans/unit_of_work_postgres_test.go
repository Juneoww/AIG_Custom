package mcpscans

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type postgresNoopEngine struct{}

func (postgresNoopEngine) ValidateTaskReferences(context.Context, tasks.EngineTask) error { return nil }
func (postgresNoopEngine) SubmitTask(context.Context, tasks.EngineTask) (string, error) {
	return "", errors.New("dispatch is not part of this transaction test")
}
func (postgresNoopEngine) GetTaskStatus(context.Context, string) (tasks.EngineStatus, error) {
	return tasks.EngineStatus{}, tasks.ErrEngineTaskNotFound
}
func (postgresNoopEngine) GetResult(context.Context, string) (json.RawMessage, error) {
	return nil, tasks.ErrResultNotReady
}
func (postgresNoopEngine) CancelTask(context.Context, string) error { return nil }

type failingPostgresBindingRepository struct {
	delegate *mcpconnections.GormRepository
	err      error
}

type blockingPostgresBindingRepository struct {
	delegate *mcpconnections.GormRepository
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (repository *blockingPostgresBindingRepository) CreateTaskBinding(ctx context.Context, binding *mcpconnections.TaskBinding) error {
	blocked := false
	repository.once.Do(func() {
		blocked = true
		close(repository.started)
	})
	if blocked {
		<-repository.release
	}
	return repository.delegate.CreateTaskBinding(ctx, binding)
}

func (repository *failingPostgresBindingRepository) CreateTaskBinding(ctx context.Context, binding *mcpconnections.TaskBinding) error {
	if repository.err != nil {
		return repository.err
	}
	return repository.delegate.CreateTaskBinding(ctx, binding)
}

type committedPostgresDispatcher struct {
	tasks    *tasks.GormRepository
	bindings *mcpconnections.GormRepository
	calls    int
}

func (dispatcher *committedPostgresDispatcher) DispatchMCPAfterCommit(ctx context.Context, _ identity.Subject, taskID string) error {
	dispatcher.calls++
	if _, err := dispatcher.tasks.Get(ctx, taskID); err != nil {
		return fmt.Errorf("post-commit dispatcher cannot read task: %w", err)
	}
	if _, err := dispatcher.bindings.GetTaskBinding(ctx, taskID); err != nil {
		return fmt.Errorf("post-commit dispatcher cannot read binding: %w", err)
	}
	return nil
}

type mcpScanPolicyResolver func(context.Context, string) ([]net.IPAddr, error)

func (resolver mcpScanPolicyResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return resolver(ctx, host)
}

type mcpScanPolicyDialer struct{}

func (mcpScanPolicyDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("test policy must not dial")
}

func TestMCPCreateUnitOfWorkPostgresRollsBackTaskAttachmentBindingAndIdempotencyOnBindingFailure(t *testing.T) {
	ctx := context.Background()
	db := openMCPScanPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	taskRepository := tasks.NewGormRepository(db)
	auditRepository := audit.NewGormRepository(db)
	bindingRepository := mcpconnections.NewGormRepository(db)
	idempotencyRepository := idempotency.NewGormRepository(db)
	require.NoError(t, taskRepository.Init())
	require.NoError(t, auditRepository.Init())
	require.NoError(t, bindingRepository.Init())
	require.NoError(t, idempotencyRepository.Init())
	auditService := audit.NewService(auditRepository)
	attachmentService, err := tasks.NewAttachmentService(taskRepository, tasks.AttachmentConfig{
		MCPOnly: true, UploadDir: t.TempDir(), MaxFileBytes: 16, MaxChunkBytes: 8,
	}, auditService)
	require.NoError(t, err)
	taskService := tasks.NewService(taskRepository, postgresNoopEngine{}, auditService)
	taskService.SetMCPAttachmentService(attachmentService)
	keyring := postgresTestKeyring(t)
	policy := postgresTestPolicy(t)
	failingBindings := &failingPostgresBindingRepository{delegate: bindingRepository, err: errors.New("injected binding failure")}
	workflow := NewCreateUnitOfWork(CreateUnitOfWorkDependencies{
		Idempotency: idempotency.NewService(idempotencyRepository), Audits: auditService, Tasks: taskService,
		Bindings: failingBindings, Keyring: keyring, Policy: policy,
	})
	subject := identity.Subject{UserID: "rollback-owner", Username: "rollback-user", Role: identity.RoleUser}
	attachment := &tasks.Attachment{
		ID: "rollback-ready-attachment", OwnerUserID: subject.UserID, OriginalName: "private-source.zip", StorageName: "mcp-opaque-rollback-storage",
		Size: 8, State: tasks.AttachmentStateReady, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, taskRepository.CreateAttachment(ctx, attachment))
	input := CreateInput{IdempotencyKey: "rollback-binding-key", SourceKind: SourceKindRepository, AttachmentIDs: []string{attachment.ID}}

	_, err = workflow.Create(ctx, subject, input)
	require.Error(t, err)
	assertMCPCreatePersistenceCounts(t, db, 0, 0, 0)
	storedAttachment, attachmentErr := taskRepository.GetAttachment(ctx, attachment.ID)
	require.NoError(t, attachmentErr)
	assert.Equal(t, tasks.AttachmentStateReady, storedAttachment.State)

	failingBindings.err = nil
	created, err := workflow.Create(ctx, subject, input)
	require.NoError(t, err)
	assert.False(t, created.Replay)
	assertMCPCreatePersistenceCounts(t, db, 1, 1, 1)
	storedAttachment, attachmentErr = taskRepository.GetAttachment(ctx, attachment.ID)
	require.NoError(t, attachmentErr)
	assert.Equal(t, tasks.AttachmentStateAttached, storedAttachment.State)
}

func TestMCPCreateUnitOfWorkPostgresLocksServiceVersionAndDispatchesAfterCommit(t *testing.T) {
	ctx := context.Background()
	db := openMCPScanPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	taskRepository := tasks.NewGormRepository(db)
	auditRepository := audit.NewGormRepository(db)
	bindingRepository := mcpconnections.NewGormRepository(db)
	idempotencyRepository := idempotency.NewGormRepository(db)
	require.NoError(t, taskRepository.Init())
	require.NoError(t, auditRepository.Init())
	require.NoError(t, bindingRepository.Init())
	require.NoError(t, idempotencyRepository.Init())
	auditService := audit.NewService(auditRepository)
	taskService := tasks.NewService(taskRepository, postgresNoopEngine{}, auditService)
	keyring := postgresTestKeyring(t)
	policy := postgresTestPolicy(t)
	connectionService := mcpconnections.NewService(bindingRepository, keyring, nil, policy)
	subject := identity.Subject{UserID: "service-owner", Username: "service-user", Role: identity.RoleUser}
	connection, err := connectionService.Create(ctx, subject, mcpconnections.CreateConnectionInput{
		Name: "受控服务连接", Description: "safe", Scope: mcpconnections.ScopePrivate, Transport: mcpconnections.TransportHTTP,
		ServerURL: "https://mcp.allowed.example.test/endpoint", Authentication: mcpconnections.Authentication{
			Kind: mcpconnections.AuthenticationBearer, Secret: "opaque-test-token",
		},
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&mcpconnections.ConnectionConfig{}).Where("id = ?", connection.ID).Update("enabled", true).Error)
	require.NoError(t, db.Model(&mcpconnections.ConnectionVersion{}).
		Where("connection_config_id = ? AND version = ?", connection.ID, connection.CurrentVersion).
		Updates(map[string]any{"probe_status": mcpconnections.ProbeStatusPassed, "detected_transport": mcpconnections.TransportHTTP}).Error)
	dispatcher := &committedPostgresDispatcher{tasks: taskRepository, bindings: bindingRepository}
	workflow := NewCreateUnitOfWork(CreateUnitOfWorkDependencies{
		Idempotency: idempotency.NewService(idempotencyRepository), Audits: auditService, Tasks: taskService,
		Connections: connectionService, Bindings: bindingRepository, Keyring: keyring, Policy: policy, Dispatcher: dispatcher, Models: allowedModelDescriber{},
	})
	input := CreateInput{
		IdempotencyKey: "service-postgres-key", SourceKind: SourceKindService, ConnectionConfigID: connection.ID,
		ConnectionConfigVersion: connection.CurrentVersion, AuthorizationConfirmed: true, ModelID: "governed-model",
	}

	created, err := workflow.Create(ctx, subject, input)
	require.NoError(t, err)
	assert.False(t, created.Replay)
	assert.Equal(t, tasks.StatusPending, created.Status)
	assert.Equal(t, 1, dispatcher.calls, "the dispatcher must only observe committed task and binding rows")
	stored, taskErr := taskRepository.Get(ctx, created.TaskID)
	require.NoError(t, taskErr)
	assert.Empty(t, stored.Content)
	assert.JSONEq(t, `{"source_kind":"service","model_id":"governed-model","authorization_confirmed":true}`, string(stored.Params))
	assert.NotContains(t, string(stored.Params), connection.ID)
	binding, bindingErr := bindingRepository.GetTaskBinding(ctx, created.TaskID)
	require.NoError(t, bindingErr)
	require.NotNil(t, binding.ConnectionConfigID)
	require.NotNil(t, binding.ConnectionConfigVersion)
	assert.Equal(t, connection.ID, *binding.ConnectionConfigID)
	assert.Equal(t, connection.CurrentVersion, *binding.ConnectionConfigVersion)
	events, auditErr := auditRepository.List(ctx, audit.Filter{ResourceID: created.TaskID})
	require.NoError(t, auditErr)
	encodedEvents, marshalErr := json.Marshal(events)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(encodedEvents), "mcp.allowed.example.test")
	assert.NotContains(t, string(encodedEvents), "opaque-test-token")
}

func TestMCPCreateUnitOfWorkPostgresAllowsOnlyOneCompetingAttachmentBinding(t *testing.T) {
	ctx := context.Background()
	db := openMCPScanPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	taskRepository := tasks.NewGormRepository(db)
	auditRepository := audit.NewGormRepository(db)
	bindingRepository := mcpconnections.NewGormRepository(db)
	idempotencyRepository := idempotency.NewGormRepository(db)
	require.NoError(t, taskRepository.Init())
	require.NoError(t, auditRepository.Init())
	require.NoError(t, bindingRepository.Init())
	require.NoError(t, idempotencyRepository.Init())
	auditService := audit.NewService(auditRepository)
	attachmentService, err := tasks.NewAttachmentService(taskRepository, tasks.AttachmentConfig{
		MCPOnly: true, UploadDir: t.TempDir(), MaxFileBytes: 16, MaxChunkBytes: 8,
	}, auditService)
	require.NoError(t, err)
	taskService := tasks.NewService(taskRepository, postgresNoopEngine{}, auditService)
	taskService.SetMCPAttachmentService(attachmentService)
	blockingBindings := &blockingPostgresBindingRepository{
		delegate: bindingRepository, started: make(chan struct{}), release: make(chan struct{}),
	}
	workflow := NewCreateUnitOfWork(CreateUnitOfWorkDependencies{
		Idempotency: idempotency.NewService(idempotencyRepository), Audits: auditService, Tasks: taskService,
		Bindings: blockingBindings, Keyring: postgresTestKeyring(t), Policy: postgresTestPolicy(t),
	})
	subject := identity.Subject{UserID: "attachment-race-owner", Username: "attachment-race-user", Role: identity.RoleUser}
	attachment := &tasks.Attachment{
		ID: "attachment-race-ready", OwnerUserID: subject.UserID, OriginalName: "private-source.zip", StorageName: "mcp-opaque-attachment-race",
		Size: 8, State: tasks.AttachmentStateReady, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, taskRepository.CreateAttachment(ctx, attachment))
	firstInput := CreateInput{IdempotencyKey: "attachment-race-first", SourceKind: SourceKindRepository, AttachmentIDs: []string{attachment.ID}}
	secondInput := firstInput
	secondInput.IdempotencyKey = "attachment-race-second"
	firstResult := make(chan error, 1)
	go func() {
		_, createErr := workflow.Create(ctx, subject, firstInput)
		firstResult <- createErr
	}()
	select {
	case <-blockingBindings.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first create did not reach the binding stage")
	}
	secondResult := make(chan error, 1)
	go func() {
		_, createErr := workflow.Create(ctx, subject, secondInput)
		secondResult <- createErr
	}()
	select {
	case createErr := <-secondResult:
		t.Fatalf("competing create returned before first transaction committed: %v", createErr)
	case <-time.After(150 * time.Millisecond):
	}
	close(blockingBindings.release)
	select {
	case createErr := <-firstResult:
		require.NoError(t, createErr)
	case <-time.After(5 * time.Second):
		t.Fatal("first create did not finish after binding release")
	}
	select {
	case createErr := <-secondResult:
		require.ErrorIs(t, createErr, tasks.ErrAttachmentNotReady)
	case <-time.After(5 * time.Second):
		t.Fatal("competing create did not finish after the first transaction committed")
	}
	assertMCPCreatePersistenceCounts(t, db, 1, 1, 1)
	storedAttachment, attachmentErr := taskRepository.GetAttachment(ctx, attachment.ID)
	require.NoError(t, attachmentErr)
	assert.Equal(t, tasks.AttachmentStateAttached, storedAttachment.State)
}

func TestMCPCreateUnitOfWorkPostgresSerializesServiceCreationWithDisable(t *testing.T) {
	ctx := context.Background()
	db := openMCPScanPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	taskRepository := tasks.NewGormRepository(db)
	auditRepository := audit.NewGormRepository(db)
	bindingRepository := mcpconnections.NewGormRepository(db)
	idempotencyRepository := idempotency.NewGormRepository(db)
	require.NoError(t, taskRepository.Init())
	require.NoError(t, auditRepository.Init())
	require.NoError(t, bindingRepository.Init())
	require.NoError(t, idempotencyRepository.Init())
	auditService := audit.NewService(auditRepository)
	taskService := tasks.NewService(taskRepository, postgresNoopEngine{}, auditService)
	keyring := postgresTestKeyring(t)
	policy := postgresTestPolicy(t)
	connectionService := mcpconnections.NewService(bindingRepository, keyring, nil, policy)
	subject := identity.Subject{UserID: "disable-race-owner", Username: "disable-race-user", Role: identity.RoleUser}
	connection := createEnabledPostgresConnection(t, ctx, db, connectionService, subject)
	blockingBindings := &blockingPostgresBindingRepository{
		delegate: bindingRepository, started: make(chan struct{}), release: make(chan struct{}),
	}
	workflow := NewCreateUnitOfWork(CreateUnitOfWorkDependencies{
		Idempotency: idempotency.NewService(idempotencyRepository), Audits: auditService, Tasks: taskService,
		Connections: connectionService, Bindings: blockingBindings, Keyring: keyring, Policy: policy,
	})
	input := CreateInput{
		IdempotencyKey: "disable-race-create", SourceKind: SourceKindService, ConnectionConfigID: connection.ID,
		ConnectionConfigVersion: connection.CurrentVersion, AuthorizationConfirmed: true,
	}
	createResult := make(chan error, 1)
	go func() {
		_, createErr := workflow.Create(ctx, subject, input)
		createResult <- createErr
	}()
	select {
	case <-blockingBindings.started:
	case <-time.After(5 * time.Second):
		t.Fatal("service create did not reach the binding stage")
	}
	disableResult := make(chan error, 1)
	go func() {
		_, disableErr := connectionService.SetEnabled(ctx, subject, connection.ID, false)
		disableResult <- disableErr
	}()
	select {
	case disableErr := <-disableResult:
		t.Fatalf("disable returned before the locked create transaction committed: %v", disableErr)
	case <-time.After(150 * time.Millisecond):
	}
	close(blockingBindings.release)
	select {
	case createErr := <-createResult:
		require.NoError(t, createErr)
	case <-time.After(5 * time.Second):
		t.Fatal("service create did not finish after binding release")
	}
	select {
	case disableErr := <-disableResult:
		require.NoError(t, disableErr)
	case <-time.After(5 * time.Second):
		t.Fatal("disable did not finish after service create committed")
	}
	stored, err := bindingRepository.GetConfig(ctx, connection.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)
	assertMCPCreatePersistenceCounts(t, db, 1, 1, 1)
}

func assertMCPCreatePersistenceCounts(t *testing.T, db *gorm.DB, taskCount, bindingCount, idempotencyCount int64) {
	t.Helper()
	var actualTasks, actualBindings, actualIdempotency int64
	require.NoError(t, db.Model(&tasks.Task{}).Count(&actualTasks).Error)
	require.NoError(t, db.Model(&mcpconnections.TaskBinding{}).Count(&actualBindings).Error)
	require.NoError(t, db.Model(&idempotency.Record{}).Count(&actualIdempotency).Error)
	assert.Equal(t, taskCount, actualTasks)
	assert.Equal(t, bindingCount, actualBindings)
	assert.Equal(t, idempotencyCount, actualIdempotency)
}

func postgresTestKeyring(t *testing.T) *mcpconnections.Keyring {
	t.Helper()
	keyring, err := mcpconnections.NewKeyring("postgres-mcp-scan-key", []byte("01234567890123456789012345678901"), nil)
	require.NoError(t, err)
	return keyring
}

func postgresTestPolicy(t *testing.T) *mcpconnections.OutboundPolicy {
	t.Helper()
	policy, err := mcpconnections.NewOutboundPolicy(mcpconnections.OutboundPolicyConfig{
		AllowedCIDRs: []string{"203.0.113.0/24"}, GitAllowedCIDRs: []string{"198.51.100.0/24"},
		GitAllowedHosts: []string{"git.allowed.example.test"}, Dialer: mcpScanPolicyDialer{}, ControlledDialerAvailable: true,
		Resolver: mcpScanPolicyResolver(func(_ context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "mcp.allowed.example.test":
				return []net.IPAddr{{IP: net.ParseIP("203.0.113.20")}}, nil
			case "git.allowed.example.test":
				return []net.IPAddr{{IP: net.ParseIP("198.51.100.20")}}, nil
			default:
				return nil, errors.New("unexpected test host")
			}
		}),
	})
	require.NoError(t, err)
	return policy
}

func createEnabledPostgresConnection(
	t *testing.T,
	ctx context.Context,
	db *gorm.DB,
	service *mcpconnections.Service,
	subject identity.Subject,
) *mcpconnections.ConnectionSummary {
	t.Helper()
	connection, err := service.Create(ctx, subject, mcpconnections.CreateConnectionInput{
		Name: "并发受控连接", Description: "safe", Scope: mcpconnections.ScopePrivate, Transport: mcpconnections.TransportHTTP,
		ServerURL: "https://mcp.allowed.example.test/endpoint", Authentication: mcpconnections.Authentication{
			Kind: mcpconnections.AuthenticationBearer, Secret: "opaque-test-token",
		},
	})
	require.NoError(t, err)
	require.NoError(t, db.Model(&mcpconnections.ConnectionConfig{}).Where("id = ?", connection.ID).Update("enabled", true).Error)
	require.NoError(t, db.Model(&mcpconnections.ConnectionVersion{}).
		Where("connection_config_id = ? AND version = ?", connection.ID, connection.CurrentVersion).
		Updates(map[string]any{"probe_status": mcpconnections.ProbeStatusPassed, "detected_transport": mcpconnections.TransportHTTP}).Error)
	return connection
}

func openMCPScanPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "mcp_scans_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	return db
}
