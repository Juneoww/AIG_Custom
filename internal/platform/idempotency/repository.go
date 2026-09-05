package idempotency

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"gorm.io/gorm"
)

var (
	ErrNotFound            = errors.New("MCP 幂等记录不存在")
	ErrInvalid             = errors.New("MCP 幂等请求无效")
	ErrTransactionRequired = errors.New("MCP 幂等成功结果必须在业务事务中持久化")
)

// Repository keeps the physical advisory lock distinct from the business
// transaction. The callback receives the same connection through txcontext so
// audit.Mutation.Run can open the one transaction that owns all writes.
type Repository interface {
	WithinKeyLock(context.Context, string, func(context.Context) error) error
	Get(context.Context, recordIdentity) (*Record, error)
	Create(context.Context, *Record) error
	Delete(context.Context, recordIdentity) error
	CleanupExpired(context.Context, time.Time) (int, error)
}

// GormRepository operates only on the v10 MCP idempotency table and never
// performs runtime DDL.
type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Init() error {
	if repository == nil || repository.db == nil {
		return errors.New("MCP 幂等数据库不能为空")
	}
	if !repository.db.Migrator().HasTable(&Record{}) {
		return errors.New("MCP 幂等数据库尚未迁移，请先运行 aig migrate")
	}
	for _, column := range []string{"id", "principal_id", "scope_key", "method", "path", "idempotency_key", "payload_hash", "status_code", "safe_response", "expires_at", "created_at"} {
		if !repository.db.Migrator().HasColumn(&Record{}, column) {
			return fmt.Errorf("MCP 幂等数据库尚未迁移，请先运行 aig migrate：缺少列 platform_idempotency_records.%s", column)
		}
	}
	valid, err := postgresScopeIndexValid(repository.db)
	if err != nil || !valid {
		return errors.New("MCP 幂等数据库尚未迁移，请先运行 aig migrate：缺少唯一索引 ux_platform_idempotency_records_scope")
	}
	return nil
}

func (repository *GormRepository) WithinKeyLock(ctx context.Context, lockKey string, apply func(context.Context) error) (resultErr error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(lockKey) == "" || apply == nil {
		return ErrInvalid
	}
	if _, hasTransaction := txcontext.FromGorm(ctx); hasTransaction {
		// The idempotency lock is deliberately outermost: acquiring a session lock
		// on a different connection after a business transaction begins would not
		// serialize the following UoW safely.
		return ErrInvalid
	}
	database, err := repository.db.DB()
	if err != nil {
		return err
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	if _, err = connection.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return err
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var unlocked bool
		unlockErr := connection.QueryRowContext(unlockContext, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey).Scan(&unlocked)
		if unlockErr == nil && unlocked {
			return
		}
		_ = connection.Raw(func(any) error { return driver.ErrBadConn })
		if resultErr == nil {
			resultErr = errors.New("释放 MCP 幂等锁失败")
		}
	}()
	lockedDB := repository.db.Session(&gorm.Session{Context: ctx, NewDB: true})
	lockedDB.Statement.ConnPool = connection
	return apply(txcontext.WithGorm(ctx, lockedDB))
}

func (repository *GormRepository) Get(ctx context.Context, identity recordIdentity) (*Record, error) {
	if repository == nil || repository.db == nil {
		return nil, ErrInvalid
	}
	var record Record
	err := txcontext.Gorm(ctx, repository.db).
		Where("principal_id = ? AND scope_key = ? AND method = ? AND path = ? AND idempotency_key = ?", identity.principalID, identity.scopeKey, identity.method, identity.path, identity.key).
		First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return cloneRecord(&record), nil
}

func (repository *GormRepository) Create(ctx context.Context, record *Record) error {
	if repository == nil || repository.db == nil || !validRecord(record) {
		return ErrInvalid
	}
	if !hasGormTransaction(ctx) {
		return ErrTransactionRequired
	}
	return txcontext.Gorm(ctx, repository.db).Create(cloneRecord(record)).Error
}

func (repository *GormRepository) Delete(ctx context.Context, identity recordIdentity) error {
	if repository == nil || repository.db == nil {
		return ErrInvalid
	}
	result := txcontext.Gorm(ctx, repository.db).
		Where("principal_id = ? AND scope_key = ? AND method = ? AND path = ? AND idempotency_key = ?", identity.principalID, identity.scopeKey, identity.method, identity.path, identity.key).
		Delete(&Record{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
}

func (repository *GormRepository) CleanupExpired(ctx context.Context, now time.Time) (int, error) {
	if repository == nil || repository.db == nil {
		return 0, ErrInvalid
	}
	result := txcontext.Gorm(ctx, repository.db).Where("expires_at <= ?", now.UTC()).Delete(&Record{})
	return int(result.RowsAffected), result.Error
}

type memoryLock struct {
	gate       chan struct{}
	references int
}

// MemoryRepository mirrors locking and expiry behavior for fast UoW tests.
type MemoryRepository struct {
	mu      sync.Mutex
	records map[string]*Record
	locksMu sync.Mutex
	locks   map[string]*memoryLock
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{records: map[string]*Record{}, locks: map[string]*memoryLock{}}
}

func (repository *MemoryRepository) WithinKeyLock(ctx context.Context, lockKey string, apply func(context.Context) error) error {
	if repository == nil || strings.TrimSpace(lockKey) == "" || apply == nil {
		return ErrInvalid
	}
	repository.locksMu.Lock()
	lock := repository.locks[lockKey]
	if lock == nil {
		lock = &memoryLock{gate: make(chan struct{}, 1)}
		lock.gate <- struct{}{}
		repository.locks[lockKey] = lock
	}
	lock.references++
	repository.locksMu.Unlock()
	acquired := false
	select {
	case <-ctx.Done():
	case <-lock.gate:
		acquired = true
	}
	if !acquired {
		repository.releaseLock(lockKey, lock, false)
		return ctx.Err()
	}
	defer repository.releaseLock(lockKey, lock, true)
	if err := ctx.Err(); err != nil {
		return err
	}
	return apply(ctx)
}

func (repository *MemoryRepository) releaseLock(lockKey string, lock *memoryLock, acquired bool) {
	if acquired {
		lock.gate <- struct{}{}
	}
	repository.locksMu.Lock()
	lock.references--
	if lock.references == 0 {
		delete(repository.locks, lockKey)
	}
	repository.locksMu.Unlock()
}

func (repository *MemoryRepository) Get(_ context.Context, identity recordIdentity) (*Record, error) {
	if repository == nil {
		return nil, ErrInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	record, exists := repository.records[identity.storageKey()]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneRecord(record), nil
}

func (repository *MemoryRepository) Create(_ context.Context, record *Record) error {
	if repository == nil || !validRecord(record) {
		return ErrInvalid
	}
	identity := recordIdentity{principalID: record.PrincipalID, scopeKey: record.ScopeKey, method: record.Method, path: record.Path, key: record.IdempotencyKey}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	storageKey := identity.storageKey()
	if _, exists := repository.records[storageKey]; exists {
		return errors.New("MCP 幂等记录冲突")
	}
	repository.records[storageKey] = cloneRecord(record)
	return nil
}

func (repository *MemoryRepository) Delete(_ context.Context, identity recordIdentity) error {
	if repository == nil {
		return ErrInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	storageKey := identity.storageKey()
	if _, exists := repository.records[storageKey]; !exists {
		return ErrNotFound
	}
	delete(repository.records, storageKey)
	return nil
}

func (repository *MemoryRepository) CleanupExpired(_ context.Context, now time.Time) (int, error) {
	if repository == nil {
		return 0, ErrInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	removed := 0
	for key, record := range repository.records {
		if !record.ExpiresAt.After(now.UTC()) {
			delete(repository.records, key)
			removed++
		}
	}
	return removed, nil
}

func (identity recordIdentity) storageKey() string {
	return identity.principalID + "\x00" + identity.scopeKey + "\x00" + identity.method + "\x00" + identity.path + "\x00" + identity.key
}

func (identity recordIdentity) advisoryLockKey() string {
	digest := sha256.Sum256([]byte(identity.storageKey()))
	return fmt.Sprintf("platform-mcp-idempotency:%x", digest[:])
}

func validRecord(record *Record) bool {
	return record != nil && strings.TrimSpace(record.ID) != "" && strings.TrimSpace(record.PrincipalID) != "" && strings.TrimSpace(record.ScopeKey) != "" &&
		strings.TrimSpace(record.Method) != "" && strings.TrimSpace(record.Path) != "" && strings.TrimSpace(record.IdempotencyKey) != "" &&
		len(record.PayloadHash) == sha256.Size && record.StatusCode >= 200 && record.StatusCode < 300 && !record.ExpiresAt.IsZero() && !record.CreatedAt.IsZero() &&
		func() bool { _, err := decodeSafeResponse(record.SafeResponse); return err == nil }()
}

// postgresScopeIndexValid verifies the actual unique-key semantics rather
// than merely accepting an arbitrary unique index with the expected name.
func postgresScopeIndexValid(db *gorm.DB) (bool, error) {
	const query = `
SELECT COALESCE((
  SELECT index_definition.indisvalid
     AND index_definition.indisready
     AND index_definition.indisunique
     AND index_definition.indimmediate
     AND index_method.amname = 'btree'
     AND index_definition.indpred IS NULL
     AND index_definition.indexprs IS NULL
     AND index_definition.indnkeyatts = 5
     AND index_definition.indnatts = 5
     AND (
       SELECT string_agg(attribute.attname, ',' ORDER BY key_column.ordinality)
       FROM unnest(index_definition.indkey) WITH ORDINALITY AS key_column(attribute_number, ordinality)
       JOIN pg_catalog.pg_attribute AS attribute
         ON attribute.attrelid = table_definition.oid
        AND attribute.attnum = key_column.attribute_number
       WHERE key_column.ordinality <= index_definition.indnkeyatts
     ) = 'principal_id,scope_key,method,path,idempotency_key'
  FROM pg_catalog.pg_class AS table_definition
  JOIN pg_catalog.pg_namespace AS table_namespace ON table_namespace.oid = table_definition.relnamespace
  JOIN pg_catalog.pg_index AS index_definition ON index_definition.indrelid = table_definition.oid
  JOIN pg_catalog.pg_class AS index_name ON index_name.oid = index_definition.indexrelid
  JOIN pg_catalog.pg_am AS index_method ON index_method.oid = index_name.relam
  WHERE table_namespace.nspname = current_schema()
    AND table_definition.relname = 'platform_idempotency_records'
    AND index_name.relname = 'ux_platform_idempotency_records_scope'
), false)`
	var valid bool
	if err := db.Raw(query).Scan(&valid).Error; err != nil {
		return false, err
	}
	return valid, nil
}

func cloneRecord(record *Record) *Record {
	if record == nil {
		return nil
	}
	copy := *record
	copy.PayloadHash = append([]byte(nil), record.PayloadHash...)
	copy.SafeResponse = append([]byte(nil), record.SafeResponse...)
	return &copy
}

// hasGormTransaction rejects the advisory-lock connection itself: a session
// connection serializes callers but auto-commits writes. The success record
// must instead share audit.Mutation.Run's explicit business transaction.
func hasGormTransaction(ctx context.Context) bool {
	database, carried := txcontext.FromGorm(ctx)
	if !carried || database == nil || database.Statement == nil || database.Statement.ConnPool == nil {
		return false
	}
	_, transactional := database.Statement.ConnPool.(interface {
		Commit() error
		Rollback() error
	})
	return transactional
}
