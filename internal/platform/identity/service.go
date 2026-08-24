package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials       = errors.New("用户名或密码错误")
	ErrAccountDisabled          = errors.New("账号已禁用")
	ErrUnauthenticated          = errors.New("未登录或会话已失效")
	ErrInvalidPassword          = errors.New("密码不能为空")
	ErrAdminAlreadyBootstrapped = errors.New("管理员已初始化")
	ErrInvalidPagination        = errors.New("分页参数无效")
	ErrPaginationUnavailable    = errors.New("用户分页仓库未配置")
)

const (
	maxListPageSize = 100
	maxListPage     = 1000
)

type CreateUserInput struct {
	ID, Username, Password string
	Role                   Role
	MustChangePassword     bool
}
type LoginResult struct {
	Token              string
	Subject            Subject
	MustChangePassword bool
}
type Service struct {
	repo       Repository
	sessionTTL time.Duration
	now        func() time.Time
}
type Option func(*Service)

func WithSessionTTL(ttl time.Duration) Option { return func(s *Service) { s.sessionTTL = ttl } }
func NewService(repo Repository, options ...Option) *Service {
	s := &Service{repo: repo, sessionTTL: 12 * time.Hour, now: nowUTC}
	for _, option := range options {
		option(s)
	}
	return s
}
func nowUTC() time.Time { return time.Now().UTC() }

func (s *Service) CreateUser(ctx context.Context, input CreateUserInput) (*User, error) {
	username := strings.TrimSpace(input.Username)
	if username == "" || !validRole(input.Role) {
		return nil, ErrInvalidCredentials
	}
	hash, err := HashPassword(input.Password)
	if err != nil {
		return nil, err
	}
	now := s.now()
	userID := strings.TrimSpace(input.ID)
	if userID == "" {
		userID = uuid.NewString()
	}
	user := &User{ID: userID, Username: username, PasswordHash: hash, Role: input.Role, Active: true, MustChangePassword: input.MustChangePassword, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

func validRole(role Role) bool {
	return role == RoleAdmin || role == RoleUser || role == RoleAuditor
}
func (s *Service) SetActive(ctx context.Context, username string, active bool) error {
	user, err := s.repo.UserByUsername(ctx, username)
	if err != nil {
		return err
	}
	return s.repo.UpdateUserActive(ctx, user.ID, active, s.now())
}

func (s *Service) ListUsers(ctx context.Context, page, pageSize int) ([]User, int64, error) {
	if page < 1 || page > maxListPage || pageSize < 1 || pageSize > maxListPageSize {
		return nil, 0, ErrInvalidPagination
	}
	repository, ok := s.repo.(userPageRepository)
	if !ok {
		return nil, 0, ErrPaginationUnavailable
	}
	return repository.ListUsersPage(ctx, page, pageSize)
}

func (s *Service) SetRole(ctx context.Context, userID string, role Role) error {
	if !validRole(role) {
		return ErrInvalidCredentials
	}
	return s.repo.UpdateUserRole(ctx, userID, role, s.now())
}

func (s *Service) SetActiveByID(ctx context.Context, userID string, active bool) error {
	if err := s.repo.UpdateUserActive(ctx, userID, active, s.now()); err != nil {
		return err
	}
	if !active {
		return s.repo.RevokeUserSessions(ctx, userID)
	}
	return nil
}
func (s *Service) Authenticate(ctx context.Context, username, password string) (*LoginResult, error) {
	user, err := s.repo.UserByUsername(ctx, strings.TrimSpace(username))
	if err != nil || !VerifyPassword(user.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	if !user.Active {
		return nil, ErrAccountDisabled
	}
	token, err := s.newSession(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	subject := subjectForUser(user)
	return &LoginResult{Token: token, Subject: subject, MustChangePassword: user.MustChangePassword}, nil
}
func (s *Service) newSession(ctx context.Context, userID string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := s.now()
	session := &Session{ID: uuid.NewString(), UserID: userID, TokenHash: tokenHash(token), CreatedAt: now, ExpiresAt: now.Add(s.sessionTTL)}
	return token, s.repo.CreateSession(ctx, session)
}
func (s *Service) SubjectForToken(ctx context.Context, token string) (Subject, error) {
	session, err := s.repo.SessionByTokenHash(ctx, tokenHash(token))
	if err != nil || session.RevokedAt != nil || !session.ExpiresAt.After(s.now()) {
		return Subject{}, ErrUnauthenticated
	}
	user, err := s.repo.UserByID(ctx, session.UserID)
	if err != nil || !user.Active {
		return Subject{}, ErrUnauthenticated
	}
	return subjectForUser(user), nil
}
func subjectForUser(user *User) Subject {
	return Subject{UserID: user.ID, Username: user.Username, Role: user.Role, MustChangePassword: user.MustChangePassword}
}
func (s *Service) RotateSession(ctx context.Context, token string) (string, error) {
	session, err := s.repo.SessionByTokenHash(ctx, tokenHash(token))
	if err != nil || session.RevokedAt != nil || !session.ExpiresAt.After(s.now()) {
		return "", ErrUnauthenticated
	}
	now := s.now()
	session.RevokedAt = &now
	if err := s.repo.UpdateSession(ctx, session); err != nil {
		return "", err
	}
	return s.newSession(ctx, session.UserID)
}
func (s *Service) RevokeSession(ctx context.Context, token string) error {
	session, err := s.repo.SessionByTokenHash(ctx, tokenHash(token))
	if err != nil {
		return ErrUnauthenticated
	}
	now := s.now()
	session.RevokedAt = &now
	return s.repo.UpdateSession(ctx, session)
}
func (s *Service) ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error {
	user, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !VerifyPassword(user.PasswordHash, oldPassword) {
		return ErrInvalidCredentials
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.repo.UpdateUserPassword(ctx, user.ID, hash, false, s.now()); err != nil {
		return err
	}
	return s.repo.RevokeUserSessions(ctx, user.ID)
}
func (s *Service) CreatePasswordReset(ctx context.Context, userID string) (string, error) {
	if _, err := s.repo.UserByID(ctx, userID); err != nil {
		return "", err
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := s.now()
	reset := &PasswordReset{ID: uuid.NewString(), UserID: userID, TokenHash: tokenHash(token), CreatedAt: now, ExpiresAt: now.Add(30 * time.Minute)}
	return token, s.repo.CreatePasswordReset(ctx, reset)
}

// CreatePasswordResetForUsername is used by the trusted local operator CLI.
// The returned token is deliberately never exposed by HTTP handlers.
func (s *Service) CreatePasswordResetForUsername(ctx context.Context, username string) (string, error) {
	user, err := s.repo.UserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		return "", err
	}
	return s.CreatePasswordReset(ctx, user.ID)
}
func (s *Service) ResetPassword(ctx context.Context, token, temporaryPassword string) error {
	reset, err := s.repo.PasswordResetByHash(ctx, tokenHash(token))
	if err != nil || reset.UsedAt != nil || !reset.ExpiresAt.After(s.now()) {
		return ErrUnauthenticated
	}
	user, err := s.repo.UserByID(ctx, reset.UserID)
	if err != nil {
		return err
	}
	hash, err := HashPassword(temporaryPassword)
	if err != nil {
		return err
	}
	now := s.now()
	reset.UsedAt = &now
	if err := s.repo.UpdateUserPassword(ctx, user.ID, hash, true, now); err != nil {
		return err
	}
	if err := s.repo.UpdatePasswordReset(ctx, reset); err != nil {
		return err
	}
	return s.repo.RevokeUserSessions(ctx, user.ID)
}
