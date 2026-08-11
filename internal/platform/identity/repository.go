package identity

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"gorm.io/gorm"
)

var ErrNotFound = errors.New("身份记录不存在")

type Repository interface {
	CreateUser(context.Context, *User) error
	CreateInitialAdministrator(context.Context, *User) (bool, error)
	ListUsers(context.Context) ([]User, error)
	UserByUsername(context.Context, string) (*User, error)
	UserByID(context.Context, string) (*User, error)
	UpdateUserRole(context.Context, string, Role, time.Time) error
	UpdateUserActive(context.Context, string, bool, time.Time) error
	UpdateUserPassword(context.Context, string, string, bool, time.Time) error
	HasAdministrator(context.Context) (bool, error)
	CreateSession(context.Context, *Session) error
	SessionByTokenHash(context.Context, string) (*Session, error)
	UpdateSession(context.Context, *Session) error
	RevokeUserSessions(context.Context, string) error
	CreatePasswordReset(context.Context, *PasswordReset) error
	PasswordResetByHash(context.Context, string) (*PasswordReset, error)
	UpdatePasswordReset(context.Context, *PasswordReset) error
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (r *GormRepository) Init() error {
	if r == nil || r.db == nil {
		return errors.New("身份数据库不能为空")
	}
	for table, model := range map[string]interface{}{
		"identity_users":           &User{},
		"identity_sessions":        &Session{},
		"identity_password_resets": &PasswordReset{},
	} {
		if !r.db.Migrator().HasTable(model) {
			return fmt.Errorf("身份数据库尚未迁移，请先运行 aig migrate：缺少表 %s", table)
		}
	}
	return nil
}

func (r *GormRepository) CreateUser(ctx context.Context, user *User) error {
	return txcontext.Gorm(ctx, r.db).Create(user).Error
}

func (r *GormRepository) ListUsers(ctx context.Context) ([]User, error) {
	var users []User
	return users, txcontext.Gorm(ctx, r.db).Order("created_at ASC, id ASC").Find(&users).Error
}

// CreateInitialAdministrator atomically creates the first administrator.
// The project supports PostgreSQL only, so a transaction-scoped advisory lock
// safely serializes concurrent bootstrap commands across independent processes.
func (r *GormRepository) CreateInitialAdministrator(ctx context.Context, user *User) (bool, error) {
	const bootstrapAdminLock int64 = 849238492384923
	created := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", bootstrapAdminLock).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&User{}).Where("role = ?", RoleAdmin).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		if err := tx.Create(user).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}
func (r *GormRepository) UserByUsername(ctx context.Context, username string) (*User, error) {
	var user User
	if err := txcontext.Gorm(ctx, r.db).Where("username = ?", username).First(&user).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &user, nil
}
func (r *GormRepository) UserByID(ctx context.Context, id string) (*User, error) {
	var user User
	if err := txcontext.Gorm(ctx, r.db).Where("id = ?", id).First(&user).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &user, nil
}
func (r *GormRepository) UpdateUserRole(ctx context.Context, userID string, role Role, updatedAt time.Time) error {
	return mapUpdateResult(txcontext.Gorm(ctx, r.db).Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"role":       role,
		"updated_at": updatedAt,
	}))
}
func (r *GormRepository) UpdateUserActive(ctx context.Context, userID string, active bool, updatedAt time.Time) error {
	return mapUpdateResult(txcontext.Gorm(ctx, r.db).Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"active":     active,
		"updated_at": updatedAt,
	}))
}
func (r *GormRepository) UpdateUserPassword(ctx context.Context, userID, passwordHash string, mustChangePassword bool, updatedAt time.Time) error {
	return mapUpdateResult(txcontext.Gorm(ctx, r.db).Model(&User{}).Where("id = ?", userID).Updates(map[string]interface{}{
		"password_hash":        passwordHash,
		"must_change_password": mustChangePassword,
		"updated_at":           updatedAt,
	}))
}
func mapUpdateResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
func (r *GormRepository) HasAdministrator(ctx context.Context) (bool, error) {
	var count int64
	err := txcontext.Gorm(ctx, r.db).Model(&User{}).Where("role = ?", RoleAdmin).Count(&count).Error
	return count > 0, err
}
func (r *GormRepository) CreateSession(ctx context.Context, session *Session) error {
	return txcontext.Gorm(ctx, r.db).Create(session).Error
}
func (r *GormRepository) SessionByTokenHash(ctx context.Context, hash string) (*Session, error) {
	var session Session
	if err := txcontext.Gorm(ctx, r.db).Where("token_hash = ?", hash).First(&session).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &session, nil
}
func (r *GormRepository) UpdateSession(ctx context.Context, session *Session) error {
	return txcontext.Gorm(ctx, r.db).Save(session).Error
}
func (r *GormRepository) RevokeUserSessions(ctx context.Context, userID string) error {
	return txcontext.Gorm(ctx, r.db).Model(&Session{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", gorm.Expr("CURRENT_TIMESTAMP")).Error
}
func (r *GormRepository) CreatePasswordReset(ctx context.Context, reset *PasswordReset) error {
	return txcontext.Gorm(ctx, r.db).Create(reset).Error
}
func (r *GormRepository) PasswordResetByHash(ctx context.Context, hash string) (*PasswordReset, error) {
	var reset PasswordReset
	if err := txcontext.Gorm(ctx, r.db).Where("token_hash = ?", hash).First(&reset).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &reset, nil
}
func (r *GormRepository) UpdatePasswordReset(ctx context.Context, reset *PasswordReset) error {
	return txcontext.Gorm(ctx, r.db).Save(reset).Error
}
func mapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

type MemoryRepository struct {
	mu        sync.Mutex
	users     map[string]*User
	usersByID map[string]*User
	sessions  map[string]*Session
	resets    map[string]*PasswordReset
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{users: map[string]*User{}, usersByID: map[string]*User{}, sessions: map[string]*Session{}, resets: map[string]*PasswordReset{}}
}
func cloneUser(v *User) *User                    { copy := *v; return &copy }
func cloneSession(v *Session) *Session           { copy := *v; return &copy }
func cloneReset(v *PasswordReset) *PasswordReset { copy := *v; return &copy }
func (r *MemoryRepository) CreateUser(_ context.Context, v *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.users[v.Username]; ok {
		return errors.New("用户名已存在")
	}
	r.users[v.Username] = cloneUser(v)
	r.usersByID[v.ID] = r.users[v.Username]
	return nil
}
func (r *MemoryRepository) ListUsers(_ context.Context) ([]User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	users := make([]User, 0, len(r.usersByID))
	for _, user := range r.usersByID {
		users = append(users, *cloneUser(user))
	}
	sort.Slice(users, func(i, j int) bool {
		if users[i].CreatedAt.Equal(users[j].CreatedAt) {
			return users[i].ID < users[j].ID
		}
		return users[i].CreatedAt.Before(users[j].CreatedAt)
	})
	return users, nil
}
func (r *MemoryRepository) CreateInitialAdministrator(_ context.Context, v *User) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, user := range r.users {
		if user.Role == RoleAdmin {
			return false, nil
		}
	}
	if _, ok := r.users[v.Username]; ok {
		return false, errors.New("用户名已存在")
	}
	r.users[v.Username] = cloneUser(v)
	r.usersByID[v.ID] = r.users[v.Username]
	return true, nil
}
func (r *MemoryRepository) UserByUsername(_ context.Context, username string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.users[username]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneUser(v), nil
}
func (r *MemoryRepository) UserByID(_ context.Context, id string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.usersByID[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneUser(v), nil
}
func (r *MemoryRepository) UpdateUserRole(_ context.Context, userID string, role Role, updatedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, ok := r.usersByID[userID]
	if !ok {
		return ErrNotFound
	}
	user.Role = role
	user.UpdatedAt = updatedAt
	return nil
}
func (r *MemoryRepository) UpdateUserActive(_ context.Context, userID string, active bool, updatedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, ok := r.usersByID[userID]
	if !ok {
		return ErrNotFound
	}
	user.Active = active
	user.UpdatedAt = updatedAt
	return nil
}
func (r *MemoryRepository) UpdateUserPassword(_ context.Context, userID, passwordHash string, mustChangePassword bool, updatedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	user, ok := r.usersByID[userID]
	if !ok {
		return ErrNotFound
	}
	user.PasswordHash = passwordHash
	user.MustChangePassword = mustChangePassword
	user.UpdatedAt = updatedAt
	return nil
}
func (r *MemoryRepository) HasAdministrator(_ context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, u := range r.users {
		if u.Role == RoleAdmin {
			return true, nil
		}
	}
	return false, nil
}
func (r *MemoryRepository) CreateSession(_ context.Context, v *Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[v.TokenHash] = cloneSession(v)
	return nil
}
func (r *MemoryRepository) SessionByTokenHash(_ context.Context, hash string) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.sessions[hash]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneSession(v), nil
}
func (r *MemoryRepository) UpdateSession(_ context.Context, v *Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[v.TokenHash]; !ok {
		return ErrNotFound
	}
	r.sessions[v.TokenHash] = cloneSession(v)
	return nil
}
func (r *MemoryRepository) RevokeUserSessions(_ context.Context, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := nowUTC()
	for _, v := range r.sessions {
		if v.UserID == userID && v.RevokedAt == nil {
			v.RevokedAt = &now
		}
	}
	return nil
}
func (r *MemoryRepository) CreatePasswordReset(_ context.Context, v *PasswordReset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resets[v.TokenHash] = cloneReset(v)
	return nil
}
func (r *MemoryRepository) PasswordResetByHash(_ context.Context, hash string) (*PasswordReset, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.resets[hash]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneReset(v), nil
}
func (r *MemoryRepository) UpdatePasswordReset(_ context.Context, v *PasswordReset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.resets[v.TokenHash]; !ok {
		return ErrNotFound
	}
	r.resets[v.TokenHash] = cloneReset(v)
	return nil
}
func (r *MemoryRepository) Sessions() []*Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Session, 0, len(r.sessions))
	for _, v := range r.sessions {
		out = append(out, cloneSession(v))
	}
	return out
}
