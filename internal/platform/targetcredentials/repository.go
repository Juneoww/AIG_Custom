package targetcredentials

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"gorm.io/gorm"
)

type Repository interface {
	Create(context.Context, *Credential) error
	Get(context.Context, string, string) (*Credential, error)
	List(context.Context, string) ([]Credential, error)
	Replace(context.Context, *Credential, int64) error
	Delete(context.Context, string, string, int64) error
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db} }
func (r *GormRepository) Init() error {
	if r == nil || r.db == nil || !r.db.Migrator().HasTable(&Credential{}) {
		return ErrUnavailable
	}
	return nil
}
func (r *GormRepository) Create(ctx context.Context, c *Credential) error {
	return txcontext.Gorm(ctx, r.db).Create(c).Error
}
func (r *GormRepository) Get(ctx context.Context, owner, id string) (*Credential, error) {
	var c Credential
	err := txcontext.Gorm(ctx, r.db).Where("id = ? AND owner_user_id = ?", id, owner).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &c, err
}
func (r *GormRepository) List(ctx context.Context, owner string) ([]Credential, error) {
	rows := []Credential{}
	err := txcontext.Gorm(ctx, r.db).Select("id", "owner_user_id", "name", "origin", "auth_type", "header_name", "disabled", "revision", "created_at", "updated_at").Where("owner_user_id = ?", owner).Order("created_at DESC, id ASC").Find(&rows).Error
	return rows, err
}
func (r *GormRepository) Replace(ctx context.Context, c *Credential, revision int64) error {
	result := txcontext.Gorm(ctx, r.db).Model(&Credential{}).Where("id = ? AND owner_user_id = ? AND revision = ?", c.ID, c.OwnerUserID, revision).Select("name", "origin", "auth_type", "header_name", "disabled", "revision", "encrypted_secret", "secret_nonce", "key_id", "updated_at").Updates(c)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}
func (r *GormRepository) Delete(ctx context.Context, owner, id string, revision int64) error {
	result := txcontext.Gorm(ctx, r.db).Where("id = ? AND owner_user_id = ? AND revision = ?", id, owner, revision).Delete(&Credential{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

type MemoryRepository struct {
	mu   sync.Mutex
	rows map[string]*Credential
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{rows: map[string]*Credential{}}
}
func clone(c *Credential) *Credential {
	copy := *c
	copy.EncryptedSecret = append([]byte(nil), c.EncryptedSecret...)
	copy.SecretNonce = append([]byte(nil), c.SecretNonce...)
	return &copy
}
func (r *MemoryRepository) Create(_ context.Context, c *Credential) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rows[c.ID] != nil {
		return ErrConflict
	}
	r.rows[c.ID] = clone(c)
	return nil
}
func (r *MemoryRepository) Get(_ context.Context, owner, id string) (*Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.rows[id]
	if c == nil || c.OwnerUserID != owner {
		return nil, ErrNotFound
	}
	return clone(c), nil
}
func (r *MemoryRepository) List(_ context.Context, owner string) ([]Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := []Credential{}
	for _, c := range r.rows {
		if c.OwnerUserID == owner {
			rows = append(rows, *clone(c))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}
func (r *MemoryRepository) Replace(_ context.Context, c *Credential, revision int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	old := r.rows[c.ID]
	if old == nil || old.OwnerUserID != c.OwnerUserID || old.Revision != revision {
		return ErrConflict
	}
	r.rows[c.ID] = clone(c)
	return nil
}
func (r *MemoryRepository) Delete(_ context.Context, owner, id string, revision int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.rows[id]
	if c == nil || c.OwnerUserID != owner || c.Revision != revision {
		return ErrConflict
	}
	delete(r.rows, id)
	return nil
}
