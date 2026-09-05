package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestServiceReplaysCanonicalPrivateRequestOnlyOnce(t *testing.T) {
	service := NewService(NewMemoryRepository())
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{
		Scope: ScopePrivate, Method: "post", Path: "/api/v1/platform/mcp-scans",
		Key: "mcp-create-replay", Payload: json.RawMessage(`{"connection_version":1,"source_kind":"service"}`),
	}
	called := 0
	first, err := service.Execute(context.Background(), subject, operation, func(ctx context.Context, claim *Claim) error {
		called++
		return claim.PersistSuccess(ctx, 200, SafeResponse{TaskID: "01e5f3a4-ec5b-4a15-9d07-0161d42f0c18", Status: "pending"})
	})
	require.NoError(t, err)
	assert.False(t, first.Replay)
	assert.Equal(t, 200, first.StatusCode)
	assert.Equal(t, "pending", first.Response.Status)

	operation.Payload = json.RawMessage(`{"source_kind":"service","connection_version":1}`)
	second, err := service.Execute(context.Background(), subject, operation, func(context.Context, *Claim) error {
		called++
		return nil
	})
	require.NoError(t, err)
	assert.True(t, second.Replay)
	assert.Equal(t, first.StatusCode, second.StatusCode)
	assert.Equal(t, first.Response, second.Response)
	assert.Equal(t, 1, called)
}

func TestServiceRejectsQueryFromMCPMutationIdentity(t *testing.T) {
	service := NewService(NewMemoryRepository())
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{
		Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans?tenant=forged", Key: "query-is-not-identity",
		Payload: json.RawMessage(`{"source_kind":"service"}`),
	}
	called := false
	_, err := service.Execute(context.Background(), subject, operation, func(context.Context, *Claim) error {
		called = true
		return errors.New("query-bearing mutation must be rejected before business execution")
	})
	require.ErrorIs(t, err, ErrInvalid)
	assert.False(t, called)
}

func TestServiceRejectsEmptyQueryFromMCPMutationIdentity(t *testing.T) {
	service := NewService(NewMemoryRepository())
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{
		Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans?", Key: "empty-query-is-not-identity",
		Payload: json.RawMessage(`{"source_kind":"service"}`),
	}
	called := false
	_, err := service.Execute(context.Background(), subject, operation, func(context.Context, *Claim) error {
		called = true
		return errors.New("empty-query mutation must be rejected before business execution")
	})
	require.ErrorIs(t, err, ErrInvalid)
	assert.False(t, called)
}

func TestServiceRejectsReusedKeyForDifferentCanonicalPayload(t *testing.T) {
	service := NewService(NewMemoryRepository())
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans", Key: "mcp-create-conflict", Payload: json.RawMessage(`{"source_kind":"service"}`)}
	_, err := service.Execute(context.Background(), subject, operation, func(ctx context.Context, claim *Claim) error {
		return claim.PersistSuccess(ctx, 200, SafeResponse{TaskID: "01e5f3a4-ec5b-4a15-9d07-0161d42f0c19", Status: "pending"})
	})
	require.NoError(t, err)

	operation.Payload = json.RawMessage(`{"source_kind":"repository"}`)
	_, err = service.Execute(context.Background(), subject, operation, func(context.Context, *Claim) error {
		t.Fatal("a reused idempotency key must not re-run the callback")
		return nil
	})
	require.ErrorIs(t, err, ErrKeyReused)
}

func TestServiceReplaysNumericallyEquivalentCanonicalPayload(t *testing.T) {
	service := NewService(NewMemoryRepository())
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans", Key: "canonical-number", Payload: json.RawMessage(`{"concurrency":1}`)}
	called := 0
	_, err := service.Execute(context.Background(), subject, operation, func(ctx context.Context, claim *Claim) error {
		called++
		return claim.PersistSuccess(ctx, 200, SafeResponse{TaskID: "01e5f3a4-ec5b-4a15-9d07-0161d42f0c25", Status: "pending"})
	})
	require.NoError(t, err)

	operation.Payload = json.RawMessage(`{"concurrency":1.0}`)
	result, err := service.Execute(context.Background(), subject, operation, func(context.Context, *Claim) error {
		called++
		return nil
	})
	require.NoError(t, err)
	assert.True(t, result.Replay)
	assert.Equal(t, 1, called)
}

func TestServiceRejectsDuplicatePayloadFields(t *testing.T) {
	service := NewService(NewMemoryRepository())
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{
		Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans", Key: "duplicate-payload-field",
		Payload: json.RawMessage(`{"source_kind":"service","source_kind":"repository"}`),
	}
	called := false
	_, err := service.Execute(context.Background(), subject, operation, func(context.Context, *Claim) error {
		called = true
		return errors.New("duplicate payload must not reach the business callback")
	})
	require.ErrorIs(t, err, ErrInvalid)
	assert.False(t, called)
}

func TestServiceNormalizesEquivalentEscapedPaths(t *testing.T) {
	service := NewService(NewMemoryRepository())
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{Scope: ScopePrivate, Method: "POST", Path: "/api/v1/%6dcp-scans", Key: "canonical-path", Payload: json.RawMessage(`{"source_kind":"service"}`)}
	called := 0
	_, err := service.Execute(context.Background(), subject, operation, func(ctx context.Context, claim *Claim) error {
		called++
		return claim.PersistSuccess(ctx, 200, SafeResponse{TaskID: "01e5f3a4-ec5b-4a15-9d07-0161d42f0c26", Status: "pending"})
	})
	require.NoError(t, err)

	operation.Path = "/api/v1/mcp-scans"
	result, err := service.Execute(context.Background(), subject, operation, func(context.Context, *Claim) error {
		called++
		return nil
	})
	require.NoError(t, err)
	assert.True(t, result.Replay)
	assert.Equal(t, 1, called)
}

func TestDeriveScopeKeyUsesTrustedSubjectOnly(t *testing.T) {
	user := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	admin := identity.Subject{UserID: "admin-ivy", Role: identity.RoleAdmin}

	privateScope, err := DeriveScopeKey(user, ScopePrivate)
	require.NoError(t, err)
	assert.Equal(t, "private:user-alice", privateScope)
	globalScope, err := DeriveScopeKey(admin, ScopeGlobal)
	require.NoError(t, err)
	assert.Equal(t, "global", globalScope)

	_, err = DeriveScopeKey(user, ScopeGlobal)
	require.ErrorIs(t, err, ErrInvalidScope)
	_, err = DeriveScopeKey(identity.Subject{Role: identity.RoleAdmin}, ScopePrivate)
	require.ErrorIs(t, err, ErrInvalidScope)
}

func TestServiceRejectsUnsafeResponseAndDoesNotPersistIt(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository)
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans", Key: "unsafe-response", Payload: json.RawMessage(`{"source_kind":"service"}`)}

	_, err := service.Execute(context.Background(), subject, operation, func(ctx context.Context, claim *Claim) error {
		return claim.PersistSuccess(ctx, 200, SafeResponse{TaskID: "https://internal.example.test/mcp?token=secret", Status: "pending"})
	})
	require.ErrorIs(t, err, ErrUnsafeResponse)
	assert.Empty(t, repository.records)
}

func TestServiceKeepsRecordsForAtLeastTwentyFourHours(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans", Key: "retention", Payload: json.RawMessage(`{"source_kind":"service"}`)}
	_, err := service.Execute(context.Background(), subject, operation, func(ctx context.Context, claim *Claim) error {
		return claim.PersistSuccess(ctx, 200, SafeResponse{TaskID: "01e5f3a4-ec5b-4a15-9d07-0161d42f0c20", Status: "pending"})
	})
	require.NoError(t, err)

	now = now.Add(24*time.Hour - time.Nanosecond)
	removed, err := service.CleanupExpired(context.Background())
	require.NoError(t, err)
	assert.Zero(t, removed)
	assert.Len(t, repository.records, 1)

	now = now.Add(time.Nanosecond)
	removed, err = service.CleanupExpired(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.Empty(t, repository.records)
}

func TestGormServiceSerializesConcurrentMCPKeysAndPersistsOnlySafeFields(t *testing.T) {
	ctx := context.Background()
	db := openIdempotencyPostgresDB(t)
	repository := NewGormRepository(db)
	require.Error(t, repository.Init(), "runtime code must fail closed before migration")
	require.NoError(t, database.Migrate(db))
	require.NoError(t, repository.Init())
	first := NewService(repository)
	secondRepository := NewGormRepository(db)
	type lockAttempt struct {
		backendPID int
		err        error
	}
	secondLockAttempt := make(chan lockAttempt, 1)
	secondRepository.beforeKeyLock = func(locked context.Context) error {
		var backendPID int
		err := txcontext.Gorm(locked, db).Raw("SELECT pg_backend_pid()").Scan(&backendPID).Error
		secondLockAttempt <- lockAttempt{backendPID: backendPID, err: err}
		return err
	}
	second := NewService(secondRepository)
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{
		Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans",
		Key: "concurrent-mcp-create", Payload: json.RawMessage(`{"source_kind":"service","endpoint":"https://internal.example.test/mcp","token":"do-not-store"}`),
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var callbacks int
	var callbacksMu sync.Mutex
	type execution struct {
		result Result
		err    error
	}
	firstDone := make(chan execution, 1)
	var lockBackendPID int
	var transactionBackendPID int
	go func() {
		result, err := first.Execute(ctx, subject, operation, func(locked context.Context, claim *Claim) error {
			callbacksMu.Lock()
			callbacks++
			callbacksMu.Unlock()
			if err := txcontext.Gorm(locked, db).Raw("SELECT pg_backend_pid()").Scan(&lockBackendPID).Error; err != nil {
				return err
			}
			close(started)
			<-release
			return txcontext.Gorm(locked, db).Transaction(func(transaction *gorm.DB) error {
				transactionContext := txcontext.WithGorm(locked, transaction)
				_, carried := txcontext.FromGorm(transactionContext)
				if !carried {
					return fmt.Errorf("the business transaction lost the idempotency lock connection")
				}
				if err := txcontext.Gorm(transactionContext, db).Raw("SELECT pg_backend_pid()").Scan(&transactionBackendPID).Error; err != nil {
					return err
				}
				if transactionBackendPID != lockBackendPID {
					return fmt.Errorf("the business transaction did not use the idempotency lock connection")
				}
				return claim.PersistSuccess(transactionContext, 200, SafeResponse{TaskID: "01e5f3a4-ec5b-4a15-9d07-0161d42f0c21", Status: "pending"})
			})
		})
		firstDone <- execution{result: result, err: err}
	}()
	<-started
	secondDone := make(chan execution, 1)
	secondCallbacks := 0
	go func() {
		result, err := second.Execute(ctx, subject, operation, func(context.Context, *Claim) error {
			callbacksMu.Lock()
			secondCallbacks++
			callbacksMu.Unlock()
			return fmt.Errorf("the waiting caller must replay instead of executing")
		})
		secondDone <- execution{result: result, err: err}
	}()
	secondAttempt := <-secondLockAttempt
	require.NoError(t, secondAttempt.err)
	waitForAdvisoryLockWaiter(t, db, lockBackendPID, secondAttempt.backendPID)
	close(release)
	firstResult := <-firstDone
	secondResult := <-secondDone
	require.NoError(t, firstResult.err)
	require.NoError(t, secondResult.err)
	assert.False(t, firstResult.result.Replay)
	assert.True(t, secondResult.result.Replay)
	assert.Equal(t, lockBackendPID, transactionBackendPID)
	callbacksMu.Lock()
	assert.Equal(t, 1, callbacks)
	assert.Zero(t, secondCallbacks)
	callbacksMu.Unlock()

	var safeResponse string
	require.NoError(t, db.Table((Record{}).TableName()).Select("safe_response::text").Scan(&safeResponse).Error)
	for _, forbidden := range []string{"internal.example.test", "do-not-store", "endpoint", "token"} {
		assert.NotContains(t, safeResponse, forbidden)
	}
}

func waitForAdvisoryLockWaiter(t *testing.T, db *gorm.DB, holderBackendPID, waitingBackendPID int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var waiting int64
		err := db.Raw(`
SELECT count(*)
FROM pg_locks AS waiter
JOIN pg_locks AS holder
  ON holder.locktype = waiter.locktype
 AND holder.database IS NOT DISTINCT FROM waiter.database
 AND holder.classid = waiter.classid
 AND holder.objid = waiter.objid
 AND holder.objsubid = waiter.objsubid
WHERE waiter.locktype = 'advisory'
  AND NOT waiter.granted
  AND waiter.pid = ?
  AND holder.granted
  AND holder.pid = ?`, waitingBackendPID, holderBackendPID).Scan(&waiting).Error
		if err == nil && waiting > 0 {
			return
		}
		if time.Now().After(deadline) {
			require.NoError(t, err)
			t.Fatal("the second request never waited for the first request's PostgreSQL advisory lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGormClaimSuccessRecordRollsBackWithItsBusinessTransaction(t *testing.T) {
	ctx := context.Background()
	db := openIdempotencyPostgresDB(t)
	repository := NewGormRepository(db)
	require.NoError(t, database.Migrate(db))
	service := NewService(repository)
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans", Key: "transaction-rollback", Payload: json.RawMessage(`{"source_kind":"service"}`)}

	_, err := service.Execute(ctx, subject, operation, func(locked context.Context, claim *Claim) error {
		return txcontext.Gorm(locked, db).Transaction(func(transaction *gorm.DB) error {
			if err := claim.PersistSuccess(txcontext.WithGorm(locked, transaction), 200, SafeResponse{TaskID: "01e5f3a4-ec5b-4a15-9d07-0161d42f0c22", Status: "pending"}); err != nil {
				return err
			}
			return errors.New("force idempotency transaction rollback")
		})
	})
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&Record{}).Count(&count).Error)
	assert.Zero(t, count)

	called := 0
	result, err := service.Execute(ctx, subject, operation, func(locked context.Context, claim *Claim) error {
		called++
		return txcontext.Gorm(locked, db).Transaction(func(transaction *gorm.DB) error {
			return claim.PersistSuccess(txcontext.WithGorm(locked, transaction), 200, SafeResponse{TaskID: "01e5f3a4-ec5b-4a15-9d07-0161d42f0c22", Status: "pending"})
		})
	})
	require.NoError(t, err)
	assert.False(t, result.Replay)
	assert.Equal(t, 1, called)
}

func TestGormServiceRejectsSuccessOutsideBusinessTransaction(t *testing.T) {
	ctx := context.Background()
	db := openIdempotencyPostgresDB(t)
	repository := NewGormRepository(db)
	require.NoError(t, database.Migrate(db))
	service := NewService(repository)
	subject := identity.Subject{UserID: "user-alice", Role: identity.RoleUser}
	operation := Operation{Scope: ScopePrivate, Method: "POST", Path: "/api/v1/platform/mcp-scans", Key: "transaction-required", Payload: json.RawMessage(`{"source_kind":"service"}`)}

	_, err := service.Execute(ctx, subject, operation, func(locked context.Context, claim *Claim) error {
		return claim.PersistSuccess(locked, 200, SafeResponse{TaskID: "01e5f3a4-ec5b-4a15-9d07-0161d42f0c27", Status: "pending"})
	})
	require.ErrorIs(t, err, ErrTransactionRequired)
	var count int64
	require.NoError(t, db.Model(&Record{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestRepositoryRejectsUnsafeRawSafeResponse(t *testing.T) {
	repository := NewMemoryRepository()
	record := &Record{
		ID: "record-unsafe", PrincipalID: "user-alice", ScopeKey: "private:user-alice", Method: "POST", Path: "/api/v1/platform/mcp-scans", IdempotencyKey: "raw-unsafe",
		PayloadHash: make([]byte, 32), StatusCode: 200, SafeResponse: json.RawMessage(`{"task_id":"01e5f3a4-ec5b-4a15-9d07-0161d42f0c23","status":"pending","endpoint":"https://internal.example.test/mcp"}`),
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
	require.ErrorIs(t, repository.Create(context.Background(), record), ErrInvalid)
}

func TestRepositoryRejectsDuplicateSafeResponseFields(t *testing.T) {
	repository := NewMemoryRepository()
	record := &Record{
		ID: "record-duplicate-safe-field", PrincipalID: "user-alice", ScopeKey: "private:user-alice", Method: "POST", Path: "/api/v1/platform/mcp-scans", IdempotencyKey: "duplicate-safe-field",
		PayloadHash: make([]byte, 32), StatusCode: 200,
		SafeResponse: json.RawMessage(`{"task_id":"https://internal.example.test/mcp?token=secret","task_id":"01e5f3a4-ec5b-4a15-9d07-0161d42f0c24","status":"pending"}`),
		CreatedAt:    time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	}
	require.ErrorIs(t, repository.Create(context.Background(), record), ErrInvalid)
}

func TestGormRepositoryInitRejectsWrongUniqueIndexColumns(t *testing.T) {
	db := openIdempotencyPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	require.NoError(t, repository.Init())

	require.NoError(t, db.Exec(`DROP INDEX ux_platform_idempotency_records_scope`).Error)
	require.NoError(t, db.Exec(`CREATE UNIQUE INDEX ux_platform_idempotency_records_scope ON platform_idempotency_records (id)`).Error)
	require.Error(t, repository.Init())
}

func openIdempotencyPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "idempotency_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
