package models

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrForbidden = errors.New("无权访问模型配置")
	ErrNotFound  = errors.New("模型配置不存在")
	ErrInvalid   = errors.New("模型配置无效")
)

const maxCompatibilityModelIDLength = 128

var compatibilityModelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Repository interface {
	Create(context.Context, *Model) error
	Get(context.Context, string) (*Model, error)
	List(context.Context) ([]Model, error)
	Update(context.Context, *Model) error
	Delete(context.Context, string) error
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Init() error {
	if repository == nil || repository.db == nil {
		return errors.New("模型数据库不能为空")
	}
	if !repository.db.Migrator().HasTable(&Model{}) {
		return fmt.Errorf("模型数据库尚未迁移，请先运行 aig migrate：缺少表 %s", Model{}.TableName())
	}
	return nil
}

func (repository *GormRepository) Create(ctx context.Context, model *Model) error {
	return txcontext.Gorm(ctx, repository.db).Create(model).Error
}

func (repository *GormRepository) Get(ctx context.Context, id string) (*Model, error) {
	var model Model
	if err := txcontext.Gorm(ctx, repository.db).Where("id = ?", id).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &model, nil
}

func (repository *GormRepository) List(ctx context.Context) ([]Model, error) {
	var models []Model
	return models, txcontext.Gorm(ctx, repository.db).Order("created_at ASC, id ASC").Find(&models).Error
}

func (repository *GormRepository) Update(ctx context.Context, model *Model) error {
	result := txcontext.Gorm(ctx, repository.db).Save(model)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (repository *GormRepository) Delete(ctx context.Context, id string) error {
	result := txcontext.Gorm(ctx, repository.db).Delete(&Model{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

type MemoryRepository struct {
	mu     sync.Mutex
	models map[string]*Model
}

func NewMemoryRepository() *MemoryRepository { return &MemoryRepository{models: map[string]*Model{}} }

func cloneModel(model *Model) *Model {
	copy := *model
	copy.EncryptedToken = append([]byte(nil), model.EncryptedToken...)
	copy.TokenNonce = append([]byte(nil), model.TokenNonce...)
	return &copy
}

func (repository *MemoryRepository) Create(_ context.Context, model *Model) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.models[model.ID]; exists {
		return errors.New("模型配置已存在")
	}
	repository.models[model.ID] = cloneModel(model)
	return nil
}

func (repository *MemoryRepository) Get(_ context.Context, id string) (*Model, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	model, exists := repository.models[id]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneModel(model), nil
}

func (repository *MemoryRepository) List(_ context.Context) ([]Model, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	models := make([]Model, 0, len(repository.models))
	for _, model := range repository.models {
		models = append(models, *cloneModel(model))
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].CreatedAt.Equal(models[j].CreatedAt) {
			return models[i].ID < models[j].ID
		}
		return models[i].CreatedAt.Before(models[j].CreatedAt)
	})
	return models, nil
}

func (repository *MemoryRepository) Update(_ context.Context, model *Model) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.models[model.ID]; !exists {
		return ErrNotFound
	}
	repository.models[model.ID] = cloneModel(model)
	return nil
}

func (repository *MemoryRepository) Delete(_ context.Context, id string) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.models[id]; !exists {
		return ErrNotFound
	}
	delete(repository.models, id)
	return nil
}

type Service struct {
	repository Repository
	keyring    *Keyring
	audits     audit.Recorder
	now        func() time.Time
}

func NewService(repository Repository, keyring *Keyring, audits audit.Recorder) *Service {
	return &Service{repository: repository, keyring: keyring, audits: audits, now: func() time.Time { return time.Now().UTC() }}
}

func (service *Service) Create(ctx context.Context, subject identity.Subject, input CreateInput) (View, error) {
	return service.create(ctx, subject, uuid.NewString(), input)
}

// CreateWithCompatibilityID preserves the stable model IDs used by the
// legacy application API while keeping storage in the encrypted platform
// table. IDs are deliberately bounded before reaching database queries.
func (service *Service) CreateWithCompatibilityID(ctx context.Context, subject identity.Subject, id string, input CreateInput) (View, error) {
	id = strings.TrimSpace(id)
	if !validCompatibilityModelID(id) {
		return View{}, ErrInvalid
	}
	return service.create(ctx, subject, id, input)
}

func (service *Service) create(ctx context.Context, subject identity.Subject, id string, input CreateInput) (View, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.ProviderModel = strings.TrimSpace(input.ProviderModel)
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	if input.Name == "" || input.Token == "" || input.Scope != ScopePrivate && input.Scope != ScopeGlobal {
		return View{}, ErrInvalid
	}
	ownerID := ""
	switch subject.Role {
	case identity.RoleAdmin:
		if input.Scope != ScopeGlobal {
			return View{}, ErrForbidden
		}
	case identity.RoleUser:
		if input.Scope != ScopePrivate || subject.UserID == "" {
			return View{}, ErrForbidden
		}
		ownerID = subject.UserID
	default:
		return View{}, ErrForbidden
	}
	now := service.now()
	model := &Model{ID: id, OwnerUserID: ownerID, Scope: input.Scope, Name: input.Name,
		ProviderModel: input.ProviderModel, BaseURL: input.BaseURL, Note: input.Note, Limit: input.Limit,
		CreatedAt: now, UpdatedAt: now}
	if err := service.keyring.SealToken(model, input.Token); err != nil {
		return View{}, err
	}
	mutation, err := service.beginMutation(ctx, subject, audit.ActionModelCreated, model)
	if err != nil {
		return View{}, err
	}
	if err := mutation.Run(ctx, "", modelAuditMetadata(model), func(transactionContext context.Context) error {
		return service.repository.Create(transactionContext, model)
	}); err != nil {
		return View{}, err
	}
	return viewOf(model), nil
}

// CheckWritable is used by compatibility collection operations to authorize
// every ID before any mutation begins.
func (service *Service) CheckWritable(ctx context.Context, subject identity.Subject, id string) error {
	model, err := service.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if !canWrite(subject, model) {
		return ErrForbidden
	}
	return nil
}

func (service *Service) Get(ctx context.Context, subject identity.Subject, id string) (View, error) {
	model, err := service.repository.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if !canRead(subject, model) {
		return View{}, ErrForbidden
	}
	return viewOf(model), nil
}

func (service *Service) List(ctx context.Context, subject identity.Subject) ([]View, error) {
	models, err := service.repository.List(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(models))
	for index := range models {
		if canRead(subject, &models[index]) {
			views = append(views, viewOf(&models[index]))
		}
	}
	return views, nil
}

func (service *Service) Update(ctx context.Context, subject identity.Subject, id string, input UpdateInput) (View, error) {
	model, err := service.repository.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if !canWrite(subject, model) {
		return View{}, ErrForbidden
	}
	if input.Name != nil {
		model.Name = strings.TrimSpace(*input.Name)
	}
	if input.ProviderModel != nil {
		model.ProviderModel = strings.TrimSpace(*input.ProviderModel)
	}
	if input.BaseURL != nil {
		model.BaseURL = strings.TrimSpace(*input.BaseURL)
	}
	if input.Note != nil {
		model.Note = *input.Note
	}
	if input.Limit != nil {
		model.Limit = *input.Limit
	}
	if input.Disabled != nil {
		model.Disabled = *input.Disabled
	}
	if model.Name == "" {
		return View{}, ErrInvalid
	}
	if input.Token != nil && *input.Token != "" && *input.Token != MaskedToken {
		if err := service.keyring.SealToken(model, *input.Token); err != nil {
			return View{}, err
		}
	}
	model.UpdatedAt = service.now()
	mutation, err := service.beginMutation(ctx, subject, audit.ActionModelUpdated, model)
	if err != nil {
		return View{}, err
	}
	if err := mutation.Run(ctx, "", modelAuditMetadata(model), func(transactionContext context.Context) error {
		return service.repository.Update(transactionContext, model)
	}); err != nil {
		return View{}, err
	}
	return viewOf(model), nil
}

func (service *Service) Delete(ctx context.Context, subject identity.Subject, id string) error {
	model, err := service.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if !canWrite(subject, model) {
		return ErrForbidden
	}
	mutation, err := service.beginMutation(ctx, subject, audit.ActionModelDeleted, model)
	if err != nil {
		return err
	}
	return mutation.Run(ctx, "", modelAuditMetadata(model), func(transactionContext context.Context) error {
		return service.repository.Delete(transactionContext, id)
	})
}

func (service *Service) RotateEncryption(ctx context.Context, subject identity.Subject, id string) error {
	if subject.Role != identity.RoleAdmin {
		return ErrForbidden
	}
	model, err := service.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	plaintext, err := service.keyring.OpenToken(model)
	if err != nil {
		return err
	}
	if err := service.keyring.SealToken(model, plaintext); err != nil {
		return err
	}
	model.UpdatedAt = service.now()
	mutation, err := service.beginMutation(ctx, subject, audit.ActionModelEncryptionRotated, model)
	if err != nil {
		return err
	}
	return mutation.Run(ctx, "", modelAuditMetadata(model), func(transactionContext context.Context) error {
		return service.repository.Update(transactionContext, model)
	})
}

func canRead(subject identity.Subject, model *Model) bool {
	switch subject.Role {
	case identity.RoleAdmin:
		return true
	case identity.RoleAuditor:
		return model.Scope == ScopeGlobal
	case identity.RoleUser:
		return model.Scope == ScopeGlobal || model.Scope == ScopePrivate && model.OwnerUserID == subject.UserID
	default:
		return false
	}
}

func canWrite(subject identity.Subject, model *Model) bool {
	return subject.Role == identity.RoleAdmin && model.Scope == ScopeGlobal ||
		subject.Role == identity.RoleUser && model.Scope == ScopePrivate && model.OwnerUserID == subject.UserID
}

func (service *Service) beginMutation(ctx context.Context, subject identity.Subject, action audit.Action, model *Model) (*audit.Mutation, error) {
	return audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: action, ResourceType: "model", ResourceID: model.ID, Metadata: modelAuditMetadata(model),
	})
}

func modelAuditMetadata(model *Model) map[string]any {
	return map[string]any{"scope": model.Scope, "owner_user_id": model.OwnerUserID, "key_id": model.KeyID}
}
