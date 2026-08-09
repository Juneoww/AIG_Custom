package audit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"gorm.io/gorm"
)

type Repository interface {
	Append(context.Context, *Event) error
	List(context.Context, Filter) ([]Event, error)
}

type CompletionRepository interface {
	EnqueueCompletion(context.Context, *CompletionOutbox) error
	Completion(context.Context, string) (*CompletionOutbox, error)
	ListPendingCompletions(context.Context, int) ([]CompletionOutbox, error)
	UpdateCompletion(context.Context, *CompletionOutbox) error
	EventExists(context.Context, string) (bool, error)
}

var (
	ErrCompletionNotFound = errors.New("审计完成投递不存在")
	ErrEventAlreadyExists = errors.New("审计事件已存在")
)

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

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
	return repository.db.WithContext(ctx).Create(event).Error
}

func (repository *GormRepository) List(ctx context.Context, filter Filter) ([]Event, error) {
	query := repository.db.WithContext(ctx).Model(&Event{}).Order("occurred_at ASC, id ASC")
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
	return repository.db.WithContext(ctx).Create(completion).Error
}

func (repository *GormRepository) Completion(ctx context.Context, id string) (*CompletionOutbox, error) {
	var completion CompletionOutbox
	if err := repository.db.WithContext(ctx).Where("id = ?", id).First(&completion).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCompletionNotFound
		}
		return nil, err
	}
	return &completion, nil
}

func (repository *GormRepository) ListPendingCompletions(ctx context.Context, limit int) ([]CompletionOutbox, error) {
	var completions []CompletionOutbox
	err := repository.db.WithContext(ctx).Where("delivered_at IS NULL").Order("created_at ASC, id ASC").Limit(normalizedLimit(limit)).Find(&completions).Error
	return completions, err
}

func (repository *GormRepository) UpdateCompletion(ctx context.Context, completion *CompletionOutbox) error {
	result := repository.db.WithContext(ctx).Save(completion)
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
	err := repository.db.WithContext(ctx).Model(&Event{}).Where("id = ?", id).Count(&count).Error
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
	return &copy
}

func (repository *MemoryRepository) EnqueueCompletion(_ context.Context, completion *CompletionOutbox) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.completions == nil {
		repository.completions = map[string]*CompletionOutbox{}
	}
	if _, exists := repository.completions[completion.ID]; exists {
		return errors.New("审计完成投递已存在")
	}
	repository.completions[completion.ID] = cloneCompletion(completion)
	return nil
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
