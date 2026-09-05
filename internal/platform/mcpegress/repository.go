package mcpegress

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrCapabilityNotFound = errors.New("MCP 运行时能力不存在")
	ErrCapabilityInvalid  = errors.New("MCP 运行时能力无效")
)

// CapabilityRepository serializes per-task token rotation and exposes only
// the most recent hash. A new assignment invalidates the prior short-lived
// capability rather than retaining several usable credentials.
type CapabilityRepository interface {
	Issue(context.Context, string, []byte, time.Time, time.Time) (*RuntimeCapability, error)
	Latest(context.Context, string) (*RuntimeCapability, error)
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Init() error {
	if repository == nil || repository.db == nil || !repository.db.Migrator().HasTable(&RuntimeCapability{}) {
		return ErrCapabilityInvalid
	}
	for _, column := range []string{"id", "task_id", "capability_hash", "issued_at", "expires_at", "rotation", "version", "created_at"} {
		if !repository.db.Migrator().HasColumn(&RuntimeCapability{}, column) {
			return ErrCapabilityInvalid
		}
	}
	return nil
}

// Issue holds a transaction-scoped PostgreSQL advisory lock while it chooses
// the next rotation. The schema's unique (task_id, rotation) index remains a
// second line of defense; a caller can never obtain two "latest" values.
func (repository *GormRepository) Issue(ctx context.Context, taskID string, digest []byte, issuedAt, expiresAt time.Time) (*RuntimeCapability, error) {
	if repository == nil || repository.db == nil || !validCapabilityInput(taskID, digest, issuedAt, expiresAt) {
		return nil, ErrCapabilityInvalid
	}
	var issued *RuntimeCapability
	err := txcontext.Gorm(ctx, repository.db).Transaction(func(transaction *gorm.DB) error {
		if transaction.Dialector == nil || transaction.Dialector.Name() != "postgres" {
			return ErrCapabilityInvalid
		}
		if err := transaction.Exec("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "platform-mcp-runtime-capability:"+taskID).Error; err != nil {
			return err
		}
		rotation := 1
		var latest RuntimeCapability
		err := transaction.Where("task_id = ?", taskID).Order("rotation DESC").First(&latest).Error
		if err == nil {
			rotation = latest.Rotation + 1
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		record := &RuntimeCapability{
			ID: uuid.NewString(), TaskID: taskID, CapabilityHash: append([]byte(nil), digest...),
			IssuedAt: issuedAt.UTC(), ExpiresAt: expiresAt.UTC(), Rotation: rotation, Version: 1, CreatedAt: issuedAt.UTC(),
		}
		if err := transaction.Create(record).Error; err != nil {
			return err
		}
		issued = cloneCapability(record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return issued, nil
}

func (repository *GormRepository) Latest(ctx context.Context, taskID string) (*RuntimeCapability, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(taskID) == "" {
		return nil, ErrCapabilityInvalid
	}
	var record RuntimeCapability
	err := txcontext.Gorm(ctx, repository.db).Where("task_id = ?", taskID).Order("rotation DESC").First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrCapabilityNotFound
	}
	if err != nil {
		return nil, err
	}
	return cloneCapability(&record), nil
}

type MemoryCapabilityRepository struct {
	mu     sync.Mutex
	byTask map[string]*RuntimeCapability
}

func NewMemoryCapabilityRepository() *MemoryCapabilityRepository {
	return &MemoryCapabilityRepository{byTask: map[string]*RuntimeCapability{}}
}

func (repository *MemoryCapabilityRepository) Issue(_ context.Context, taskID string, digest []byte, issuedAt, expiresAt time.Time) (*RuntimeCapability, error) {
	if repository == nil || !validCapabilityInput(taskID, digest, issuedAt, expiresAt) {
		return nil, ErrCapabilityInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	rotation := 1
	if latest := repository.byTask[taskID]; latest != nil {
		rotation = latest.Rotation + 1
	}
	record := &RuntimeCapability{
		ID: uuid.NewString(), TaskID: taskID, CapabilityHash: append([]byte(nil), digest...),
		IssuedAt: issuedAt.UTC(), ExpiresAt: expiresAt.UTC(), Rotation: rotation, Version: 1, CreatedAt: issuedAt.UTC(),
	}
	repository.byTask[taskID] = record
	return cloneCapability(record), nil
}

func (repository *MemoryCapabilityRepository) Latest(_ context.Context, taskID string) (*RuntimeCapability, error) {
	if repository == nil || strings.TrimSpace(taskID) == "" {
		return nil, ErrCapabilityInvalid
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	record := repository.byTask[taskID]
	if record == nil {
		return nil, ErrCapabilityNotFound
	}
	return cloneCapability(record), nil
}

func validCapabilityInput(taskID string, digest []byte, issuedAt, expiresAt time.Time) bool {
	return strings.TrimSpace(taskID) != "" && len(digest) == sha256.Size && !issuedAt.IsZero() && expiresAt.After(issuedAt)
}

func cloneCapability(record *RuntimeCapability) *RuntimeCapability {
	if record == nil {
		return nil
	}
	copy := *record
	copy.CapabilityHash = append([]byte(nil), record.CapabilityHash...)
	return &copy
}
