package identity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T) (*Service, *MemoryRepository) {
	t.Helper()
	repo := NewMemoryRepository()
	return NewService(repo, WithSessionTTL(time.Hour)), repo
}

func TestPasswordHashUsesArgon2idAndVerifies(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	require.NoError(t, err)
	assert.Contains(t, hash, "$argon2id$")
	assert.NotContains(t, hash, "correct horse battery staple")
	assert.True(t, VerifyPassword(hash, "correct horse battery staple"))
	assert.False(t, VerifyPassword(hash, "incorrect"))
}

func TestAuthenticateRejectsWrongPasswordAndDisabledUser(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	_, err := service.CreateUser(ctx, CreateUserInput{Username: "alice", Password: "correct", Role: RoleUser})
	require.NoError(t, err)

	_, err = service.Authenticate(ctx, "alice", "wrong")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	require.NoError(t, service.SetActive(ctx, "alice", false))
	_, err = service.Authenticate(ctx, "alice", "correct")
	require.ErrorIs(t, err, ErrAccountDisabled)
}

func TestFirstLoginAndPasswordResetRequirePasswordChange(t *testing.T) {
	service, _ := newTestService(t)
	ctx := context.Background()
	user, err := service.CreateUser(ctx, CreateUserInput{Username: "alice", Password: "temporary", Role: RoleUser, MustChangePassword: true})
	require.NoError(t, err)

	login, err := service.Authenticate(ctx, "alice", "temporary")
	require.NoError(t, err)
	assert.True(t, login.MustChangePassword)

	resetToken, err := service.CreatePasswordReset(ctx, user.ID)
	require.NoError(t, err)
	require.NoError(t, service.ResetPassword(ctx, resetToken, "reset-temporary"))
	login, err = service.Authenticate(ctx, "alice", "reset-temporary")
	require.NoError(t, err)
	assert.True(t, login.MustChangePassword)

	require.NoError(t, service.ChangePassword(ctx, user.ID, "reset-temporary", "permanent"))
	login, err = service.Authenticate(ctx, "alice", "permanent")
	require.NoError(t, err)
	assert.False(t, login.MustChangePassword)
}

func TestSessionsStoreOnlyTokenHashAndRotateAndRevoke(t *testing.T) {
	service, repo := newTestService(t)
	ctx := context.Background()
	_, err := service.CreateUser(ctx, CreateUserInput{Username: "alice", Password: "secret", Role: RoleUser})
	require.NoError(t, err)
	login, err := service.Authenticate(ctx, "alice", "secret")
	require.NoError(t, err)

	stored := repo.Sessions()
	require.Len(t, stored, 1)
	assert.NotEqual(t, login.Token, stored[0].TokenHash)
	assert.NotContains(t, stored[0].TokenHash, login.Token)

	rotated, err := service.RotateSession(ctx, login.Token)
	require.NoError(t, err)
	assert.NotEqual(t, login.Token, rotated)
	_, err = service.SubjectForToken(ctx, login.Token)
	require.ErrorIs(t, err, ErrUnauthenticated)
	require.NoError(t, service.RevokeSession(ctx, rotated))
	_, err = service.SubjectForToken(ctx, rotated)
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestListUsersPaginationUsesStableRepositoryPage(t *testing.T) {
	service, _ := newTestService(t)
	service.now = func() time.Time { return time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC) }
	ctx := context.Background()
	for index := 0; index < 25; index++ {
		_, err := service.CreateUser(ctx, CreateUserInput{
			ID: fmt.Sprintf("user-%02d", index), Username: fmt.Sprintf("member-%02d", index), Password: "secret", Role: RoleUser,
		})
		require.NoError(t, err)
	}

	users, total, err := service.ListUsers(ctx, 2, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(25), total)
	require.Len(t, users, 10)
	assert.Equal(t, "user-10", users[0].ID)
	assert.Empty(t, users[0].PasswordHash)
	_, _, err = service.ListUsers(ctx, 1001, 20)
	assert.ErrorIs(t, err, ErrInvalidPagination)
}
