package audit

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"gorm.io/gorm"
)

type Repository interface {
	Append(context.Context, *Event) error
	List(context.Context, Filter) ([]Event, error)
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Init() error {
	if repository == nil || repository.db == nil {
		return errors.New("审计数据库不能为空")
	}
	if !repository.db.Migrator().HasTable(&Event{}) {
		return fmt.Errorf("审计数据库尚未迁移，请先运行 aig migrate：缺少表 %s", Event{}.TableName())
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

type MemoryRepository struct {
	mu     sync.Mutex
	events []Event
}

func NewMemoryRepository() *MemoryRepository { return &MemoryRepository{} }

func (repository *MemoryRepository) Append(_ context.Context, event *Event) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	copy := *event
	copy.Metadata = append([]byte(nil), event.Metadata...)
	repository.events = append(repository.events, copy)
	return nil
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
