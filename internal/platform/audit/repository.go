package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository interface {
	Append(context.Context, *Event) error
	List(context.Context, Filter) ([]Event, error)
}

type CompletionRepository interface {
	EnqueueCompletion(context.Context, *CompletionOutbox) error
	Completion(context.Context, string) (*CompletionOutbox, error)
	ListPendingCompletions(context.Context, int) ([]CompletionOutbox, error)
	ListReadyCompletions(context.Context, int) ([]CompletionOutbox, error)
	UpdateCompletion(context.Context, *CompletionOutbox) error
	EventExists(context.Context, string) (bool, error)
}

var (
	ErrCompletionNotFound = errors.New("审计完成投递不存在")
	ErrCompletionConflict = errors.New("审计完成投递状态冲突")
	ErrCompletionNotReady = errors.New("审计完成投递尚未就绪")
	ErrEventAlreadyExists = errors.New("审计事件已存在")
)

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

// TransactionDB exposes the shared database handle to the governance service,
// which is the sole owner of transaction lifecycle orchestration.
func (repository *GormRepository) TransactionDB() *gorm.DB { return repository.db }

func (repository *GormRepository) Init() error {
	if repository == nil || repository.db == nil {
		return errors.New("审计数据库不能为空")
	}
	if !repository.db.Migrator().HasTable(&Event{}) {
		return fmt.Errorf("审计数据库尚未迁移，请先运行 aig migrate：缺少表 %s", Event{}.TableName())
	}
	if !repository.db.Migrator().HasTable(&CompletionOutbox{}) {
		return fmt.Errorf("审计数据库尚未迁移，请先运行 aig migrate：缺少表 %s", CompletionOutbox{}.TableName())
	}
	return nil
}

func (repository *GormRepository) Append(ctx context.Context, event *Event) error {
	return txcontext.Gorm(ctx, repository.db).Create(event).Error
}

func (repository *GormRepository) List(ctx context.Context, filter Filter) ([]Event, error) {
	query := txcontext.Gorm(ctx, repository.db).Model(&Event{}).Order("occurred_at ASC, id ASC")
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.ActorUserID != "" {
		query = query.Where("actor_user_id = ?", filter.ActorUserID)
	}
	if filter.ResourceType != "" {
		query = query.Where("resource_type = ?", filter.ResourceType)
	}
	if filter.ResourceID != "" {
		query = query.Where("resource_id = ?", filter.ResourceID)
	}
	query = query.Limit(normalizedLimit(filter.Limit))
	var events []Event
	return events, query.Find(&events).Error
}

func (repository *GormRepository) EnqueueCompletion(ctx context.Context, completion *CompletionOutbox) error {
	result := txcontext.Gorm(ctx, repository.db).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"actor_user_id", "actor_username", "actor_role", "action", "resource_type", "resource_id",
			"outcome", "client_ip", "metadata", "state", "ready_at",
		}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Table: clause.CurrentTable, Name: "state"}, Value: CompletionStatePrepared},
		}},
	}).Create(completion)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}
	existing, err := repository.Completion(ctx, completion.ID)
	if err != nil {
		return err
	}
	if completionRetryCompatible(existing, completion) {
		return nil
	}
	return ErrCompletionConflict
}

func (repository *GormRepository) Completion(ctx context.Context, id string) (*CompletionOutbox, error) {
	var completion CompletionOutbox
	if err := txcontext.Gorm(ctx, repository.db).Where("id = ?", id).First(&completion).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCompletionNotFound
		}
		return nil, err
	}
	return &completion, nil
}

func (repository *GormRepository) ListPendingCompletions(ctx context.Context, limit int) ([]CompletionOutbox, error) {
	var completions []CompletionOutbox
	err := txcontext.Gorm(ctx, repository.db).Where("delivered_at IS NULL").Order("created_at ASC, id ASC").Limit(normalizedLimit(limit)).Find(&completions).Error
	return completions, err
}

func (repository *GormRepository) ListReadyCompletions(ctx context.Context, limit int) ([]CompletionOutbox, error) {
	var completions []CompletionOutbox
	err := txcontext.Gorm(ctx, repository.db).
		Where("delivered_at IS NULL AND state = ?", CompletionStateReady).
		Order("created_at ASC, id ASC").
		Limit(normalizedLimit(limit)).
		Find(&completions).Error
	return completions, err
}

func (repository *GormRepository) UpdateCompletion(ctx context.Context, completion *CompletionOutbox) error {
	result := txcontext.Gorm(ctx, repository.db).Save(completion)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrCompletionNotFound
	}
	return nil
}

func (repository *GormRepository) EventExists(ctx context.Context, id string) (bool, error) {
	var count int64
	err := txcontext.Gorm(ctx, repository.db).Model(&Event{}).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}

type MemoryRepository struct {
	mu          sync.Mutex
	events      []Event
	completions map[string]*CompletionOutbox
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{completions: map[string]*CompletionOutbox{}}
}

func (repository *MemoryRepository) Append(_ context.Context, event *Event) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for index := range repository.events {
		if repository.events[index].ID == event.ID {
			return ErrEventAlreadyExists
		}
	}
	copy := *event
	copy.Metadata = append([]byte(nil), event.Metadata...)
	repository.events = append(repository.events, copy)
	return nil
}

func cloneCompletion(completion *CompletionOutbox) *CompletionOutbox {
	copy := *completion
	copy.Metadata = append([]byte(nil), completion.Metadata...)
	if completion.DeliveredAt != nil {
		deliveredAt := *completion.DeliveredAt
		copy.DeliveredAt = &deliveredAt
	}
	if completion.ReadyAt != nil {
		readyAt := *completion.ReadyAt
		copy.ReadyAt = &readyAt
	}
	return &copy
}

func (repository *MemoryRepository) EnqueueCompletion(_ context.Context, completion *CompletionOutbox) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.completions == nil {
		repository.completions = map[string]*CompletionOutbox{}
	}
	existing, exists := repository.completions[completion.ID]
	if exists {
		if existing.State == CompletionStatePrepared {
			if !completionIdentityCompatible(existing, completion) {
				return ErrCompletionConflict
			}
			updated := cloneCompletion(completion)
			updated.CreatedAt = existing.CreatedAt
			repository.completions[completion.ID] = updated
			return nil
		}
		if completionRetryCompatible(existing, completion) {
			return nil
		}
		return ErrCompletionConflict
	}
	repository.completions[completion.ID] = cloneCompletion(completion)
	return nil
}

func completionRetryCompatible(existing, desired *CompletionOutbox) bool {
	if !completionIdentityCompatible(existing, desired) {
		return false
	}
	if desired.State == CompletionStatePrepared && existing.State == CompletionStateReady {
		return true
	}
	return existing.State == desired.State && existing.Outcome == desired.Outcome && bytes.Equal(existing.Metadata, desired.Metadata)
}

func completionIdentityCompatible(existing, desired *CompletionOutbox) bool {
	return existing.ID == desired.ID && existing.EventID == desired.EventID && existing.RequestID == desired.RequestID &&
		existing.ActorUserID == desired.ActorUserID && existing.ActorUsername == desired.ActorUsername && existing.ActorRole == desired.ActorRole &&
		existing.Action == desired.Action && existing.ResourceType == desired.ResourceType && existing.ResourceID == desired.ResourceID &&
		existing.ClientIP == desired.ClientIP
}

func (repository *MemoryRepository) Completion(_ context.Context, id string) (*CompletionOutbox, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	completion, exists := repository.completions[id]
	if !exists {
		return nil, ErrCompletionNotFound
	}
	return cloneCompletion(completion), nil
}

func (repository *MemoryRepository) ListPendingCompletions(_ context.Context, limit int) ([]CompletionOutbox, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	completions := make([]CompletionOutbox, 0, len(repository.completions))
	for _, completion := range repository.completions {
		if completion.DeliveredAt == nil {
			completions = append(completions, *cloneCompletion(completion))
		}
	}
	sort.Slice(completions, func(i, j int) bool {
		if completions[i].CreatedAt.Equal(completions[j].CreatedAt) {
			return completions[i].ID < completions[j].ID
		}
		return completions[i].CreatedAt.Before(completions[j].CreatedAt)
	})
	if len(completions) > normalizedLimit(limit) {
		completions = completions[:normalizedLimit(limit)]
	}
	return completions, nil
}

func (repository *MemoryRepository) ListReadyCompletions(_ context.Context, limit int) ([]CompletionOutbox, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	completions := make([]CompletionOutbox, 0, len(repository.completions))
	for _, completion := range repository.completions {
		if completion.DeliveredAt == nil && completion.State == CompletionStateReady {
			completions = append(completions, *cloneCompletion(completion))
		}
	}
	sort.Slice(completions, func(i, j int) bool {
		if completions[i].CreatedAt.Equal(completions[j].CreatedAt) {
			return completions[i].ID < completions[j].ID
		}
		return completions[i].CreatedAt.Before(completions[j].CreatedAt)
	})
	if len(completions) > normalizedLimit(limit) {
		completions = completions[:normalizedLimit(limit)]
	}
	return completions, nil
}

func (repository *MemoryRepository) UpdateCompletion(_ context.Context, completion *CompletionOutbox) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.completions[completion.ID]; !exists {
		return ErrCompletionNotFound
	}
	repository.completions[completion.ID] = cloneCompletion(completion)
	return nil
}

func (repository *MemoryRepository) EventExists(_ context.Context, id string) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for index := range repository.events {
		if repository.events[index].ID == id {
			return true, nil
		}
	}
	return false, nil
}

func (repository *MemoryRepository) List(_ context.Context, filter Filter) ([]Event, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	events := make([]Event, 0, len(repository.events))
	for _, event := range repository.events {
		if filter.Action != "" && event.Action != filter.Action ||
			filter.ActorUserID != "" && event.ActorUserID != filter.ActorUserID ||
			filter.ResourceType != "" && event.ResourceType != filter.ResourceType ||
			filter.ResourceID != "" && event.ResourceID != filter.ResourceID {
			continue
		}
		copy := event
		copy.Metadata = append([]byte(nil), event.Metadata...)
		events = append(events, copy)
		if len(events) == normalizedLimit(filter.Limit) {
			break
		}
	}
	return events, nil
}

func normalizedLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 500 {
		return 500
	}
	return limit
}
