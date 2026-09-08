package targetcredentials

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func httpInput(t *testing.T, consent bool) Input {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"name": "Internal inference", "origin": "http://inference.internal:80", "auth_type": "bearer", "secret": "fictional-http-token", "allow_insecure_http": consent})
	require.NoError(t, err)
	var input Input
	require.NoError(t, json.Unmarshal(raw, &input))
	return input
}

func TestCredentialHTTPAcceptedByDefault(t *testing.T) {
	s, _, owner := fixture(t)
	_, err := s.Create(context.Background(), owner, httpInput(t, false))
	require.NoError(t, err, "HTTP is selected directly by the origin, without a separate opt-in")
	view, err := s.Create(context.Background(), owner, httpInput(t, true))
	require.NoError(t, err)
	require.Equal(t, "http://inference.internal", view.Origin)
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"allow_insecure_http":true`)
	require.NotContains(t, string(raw), "fictional-http-token")
}

func TestCredentialHTTPRetainsProtocolScopeAndEncryption(t *testing.T) {
	s, repo, owner := fixture(t)
	ctx := context.Background()
	input := httpInput(t, false)
	view, err := s.Create(ctx, owner, input)
	require.NoError(t, err)
	require.NoError(t, s.ValidateReference(ctx, owner.UserID, view.ID, 1, "http://inference.internal:80/api/version"))
	runtime, err := s.Resolve(ctx, owner.UserID, view.ID, 1, "http://inference.internal/api/version")
	require.NoError(t, err)
	raw, err := json.Marshal(runtime)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"allow_insecure_http":true`)
	require.Equal(t, "Bearer "+input.Secret, runtime.Headers["Authorization"])
	for _, target := range []string{"https://inference.internal", "http://inference.internal:8080", "http://elsewhere.internal", "inference.internal"} {
		_, err := s.Resolve(ctx, owner.UserID, view.ID, 1, target)
		require.Error(t, err, target)
	}
	stored, err := repo.Get(ctx, owner.UserID, view.ID)
	require.NoError(t, err)
	stored.Origin = "https://inference.internal"
	_, err = s.open(stored)
	require.Error(t, err, "changing protocol must invalidate the authenticated ciphertext")
	input.Secret = ""
	updated, err := s.Update(ctx, owner, view.ID, 1, input)
	require.NoError(t, err)
	require.EqualValues(t, 2, updated.Revision)
	input.Origin = "https://inference.internal"
	_, err = s.Update(ctx, owner, view.ID, 2, input)
	require.ErrorIs(t, err, ErrInvalid, "protocol changes require a fresh secret")
	input.Secret = "fresh-https-token"
	updated, err = s.Update(ctx, owner, view.ID, 2, input)
	require.NoError(t, err)
	raw, err = json.Marshal(updated)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"allow_insecure_http":false`, "HTTPS remains bound to HTTPS even if input consent was true")
	input.Origin = "http://inference.internal"
	input.Secret = ""
	_, err = s.Update(ctx, owner, view.ID, 3, input)
	require.ErrorIs(t, err, ErrInvalid, "HTTPS to HTTP also requires a new secret")
}
