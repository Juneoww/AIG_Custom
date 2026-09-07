package targetcredentials

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/models"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) (*Service, *MemoryRepository, identity.Subject) {
	t.Helper()
	keyring, err := models.NewKeyring("test", make([]byte, 32), nil)
	require.NoError(t, err)
	repo := NewMemoryRepository()
	return NewService(repo, keyring, audit.NewService(audit.NewMemoryRepository())), repo, identity.Subject{UserID: "owner", Username: "owner", Role: identity.RoleUser}
}

func sample() Input {
	return Input{Name: "Inference", Origin: "https://inference.example.com", AuthType: "bearer", Secret: "private-token-123"}
}

func TestCredentialEncryptedOwnershipAndAAD(t *testing.T) {
	s, repo, owner := fixture(t)
	ctx := context.Background()
	view, err := s.Create(ctx, owner, sample())
	require.NoError(t, err)
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(raw), sample().Secret)
	stored, err := repo.Get(ctx, owner.UserID, view.ID)
	require.NoError(t, err)
	require.NotContains(t, string(stored.EncryptedSecret), sample().Secret)
	runtime, err := s.Resolve(ctx, owner.UserID, view.ID, view.Revision, "https://inference.example.com/api/version")
	require.NoError(t, err)
	require.Equal(t, "Bearer "+sample().Secret, runtime.Headers["Authorization"])
	outsider := identity.Subject{UserID: "other", Username: "other", Role: identity.RoleAdmin}
	_, err = s.Get(ctx, outsider, view.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.Resolve(ctx, outsider.UserID, view.ID, view.Revision, sample().Origin)
	require.Error(t, err)
	stored.Origin = "https://different.example.com"
	_, err = s.open(stored)
	require.Error(t, err, "AAD must bind target origin")
	stored.Origin = sample().Origin
	stored.Revision++
	_, err = s.open(stored)
	require.Error(t, err, "AAD must bind revision")
}

func TestCredentialLifecycleAndTaskScope(t *testing.T) {
	s, _, owner := fixture(t)
	ctx := context.Background()
	view, err := s.Create(ctx, owner, sample())
	require.NoError(t, err)
	for _, target := range []string{"http://inference.example.com", "https://inference.example.com:8443", "https://other.example.com", "inference.example.com", "https://inference.example.com\nhttps://other.example.com"} {
		_, err := s.Resolve(ctx, owner.UserID, view.ID, view.Revision, target)
		require.Error(t, err, target)
	}
	updated := sample()
	updated.Secret = ""
	updated.Name = "Renamed"
	next, err := s.Update(ctx, owner, view.ID, view.Revision, updated)
	require.NoError(t, err)
	require.EqualValues(t, 2, next.Revision)
	_, err = s.Resolve(ctx, owner.UserID, view.ID, view.Revision, sample().Origin)
	require.Error(t, err, "queued old revision must fail")
	_, err = s.Update(ctx, owner, view.ID, view.Revision, updated)
	require.ErrorIs(t, err, ErrConflict)
	updated.Origin = "https://other.example.com"
	_, err = s.Update(ctx, owner, next.ID, next.Revision, updated)
	require.ErrorIs(t, err, ErrInvalid, "rebinding needs fresh secret")
	updated.Origin = sample().Origin
	updated.Disabled = true
	disabled, err := s.Update(ctx, owner, next.ID, next.Revision, updated)
	require.NoError(t, err)
	_, err = s.Resolve(ctx, owner.UserID, disabled.ID, disabled.Revision, sample().Origin)
	require.Error(t, err)
	require.NoError(t, s.Delete(ctx, owner, disabled.ID, disabled.Revision))
	_, err = s.Get(ctx, owner, disabled.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCredentialRejectsUnsafeInputsAndAuditor(t *testing.T) {
	s, _, owner := fixture(t)
	ctx := context.Background()
	for _, origin := range []string{"ftp://example.com", "https://u:p@example.com", "https://example.com/path", "https://example.com?token=x", "https://example.com#x"} {
		input := sample()
		input.Origin = origin
		_, err := s.Create(ctx, owner, input)
		require.ErrorIs(t, err, ErrInvalid)
	}
	for _, header := range []string{"Host", "Proxy-Authorization", "Connection", "X-Test\r\nHost", "Content-Length"} {
		input := sample()
		input.AuthType = "api_key"
		input.HeaderName = header
		_, err := s.Create(ctx, owner, input)
		require.ErrorIs(t, err, ErrInvalid, header)
	}
	input := sample()
	input.Secret = "a\r\nb"
	_, err := s.Create(ctx, owner, input)
	require.ErrorIs(t, err, ErrInvalid)
	owner.Role = identity.RoleAuditor
	_, err = s.Create(ctx, owner, sample())
	require.ErrorIs(t, err, ErrForbidden)
	_, err = s.List(ctx, owner)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestCredentialAuthenticationForms(t *testing.T) {
	for _, kind := range []string{"bearer", "api_key", "basic", "cookie"} {
		t.Run(kind, func(t *testing.T) {
			s, _, owner := fixture(t)
			input := sample()
			input.AuthType = kind
			switch kind {
			case "api_key":
				input.HeaderName = "X-API-Key"
			case "basic":
				input.Username = "scanner"
			case "cookie":
				input.Secret = "session=abc123; tenant=def456"
			}
			view, err := s.Create(context.Background(), owner, input)
			require.NoError(t, err)
			auth, err := s.Resolve(context.Background(), owner.UserID, view.ID, view.Revision, input.Origin)
			require.NoError(t, err)
			require.Len(t, auth.Headers, 1)
		})
	}
}
