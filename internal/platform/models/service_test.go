package models

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingModelRecorder struct {
	delegate audit.Recorder
	failOn   int
	calls    int
}

func (recorder *failingModelRecorder) Record(ctx context.Context, subject identity.Subject, input audit.EventInput) error {
	recorder.calls++
	if recorder.calls == recorder.failOn {
		return errors.New("injected audit failure")
	}
	return recorder.delegate.Record(ctx, subject, input)
}

func TestPrivateAndGlobalModelVisibilityAndWrites(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	auditService := audit.NewService(audit.NewMemoryRepository())
	keyring := mustTestKeyring(t, "current", bytesOf(1), nil)
	service := NewService(repository, keyring, auditService)
	alice := identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser}
	bob := identity.Subject{UserID: "bob-id", Username: "bob", Role: identity.RoleUser}
	admin := identity.Subject{UserID: "admin-id", Username: "admin", Role: identity.RoleAdmin}
	auditor := identity.Subject{UserID: "audit-id", Username: "auditor", Role: identity.RoleAuditor}

	aliceModel, err := service.Create(ctx, alice, CreateInput{Name: "alice-private", ProviderModel: "gpt-test", BaseURL: "https://models.invalid", Token: "alice-plain-token", Scope: ScopePrivate})
	require.NoError(t, err)
	_, err = service.Get(ctx, bob, aliceModel.ID)
	assert.ErrorIs(t, err, ErrForbidden)
	_, err = service.Create(ctx, auditor, CreateInput{Name: "forbidden", Token: "secret", Scope: ScopePrivate})
	assert.ErrorIs(t, err, ErrForbidden)
	_, err = service.Create(ctx, alice, CreateInput{Name: "forbidden-global", Token: "secret", Scope: ScopeGlobal})
	assert.ErrorIs(t, err, ErrForbidden)

	global, err := service.Create(ctx, admin, CreateInput{Name: "shared", ProviderModel: "gpt-shared", BaseURL: "https://models.invalid", Token: "global-plain-token", Scope: ScopeGlobal})
	require.NoError(t, err)
	userList, err := service.List(ctx, alice)
	require.NoError(t, err)
	require.Len(t, userList, 2)
	auditorList, err := service.List(ctx, auditor)
	require.NoError(t, err)
	require.Len(t, auditorList, 1)
	assert.Equal(t, global.ID, auditorList[0].ID)
	adminList, err := service.List(ctx, admin)
	require.NoError(t, err)
	require.Len(t, adminList, 2, "administrators may inspect private model metadata for governance")

	for _, view := range append(append(userList, auditorList...), adminList...) {
		assert.Equal(t, MaskedToken, view.Token)
		encoded, marshalErr := json.Marshal(view)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(encoded), "plain-token")
	}
}

func TestTokenUsesAuthenticatedEncryptionAndSupportsKeyRotationBoundary(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	oldRing := mustTestKeyring(t, "old", bytesOf(2), nil)
	service := NewService(repository, oldRing, audit.NewService(audit.NewMemoryRepository()))
	owner := identity.Subject{UserID: "owner-id", Username: "owner", Role: identity.RoleUser}
	created, err := service.Create(ctx, owner, CreateInput{Name: "private", Token: "rotate-me", Scope: ScopePrivate})
	require.NoError(t, err)

	stored, err := repository.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "old", stored.KeyID)
	assert.NotContains(t, string(stored.EncryptedToken), "rotate-me")
	encodedEntity, err := json.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(encodedEntity), "rotate-me")
	assert.NotContains(t, fmt.Sprintf("%+v", CreateInput{Name: "private", Token: "rotate-me"}), "rotate-me")
	encodedCreate, err := json.Marshal(CreateInput{Name: "private", Token: "rotate-me"})
	require.NoError(t, err)
	assert.NotContains(t, string(encodedCreate), "rotate-me", "structured log serialization must mask create tokens")
	updateToken := "rotate-me"
	encodedUpdate, err := json.Marshal(UpdateInput{Token: &updateToken})
	require.NoError(t, err)
	assert.NotContains(t, string(encodedUpdate), "rotate-me", "structured log serialization must mask update tokens")

	newRing := mustTestKeyring(t, "new", bytesOf(3), map[string][]byte{"old": bytesOf(2)})
	plaintext, err := newRing.OpenToken(stored)
	require.NoError(t, err)
	assert.Equal(t, "rotate-me", plaintext)
	require.NoError(t, NewService(repository, newRing, audit.NewService(audit.NewMemoryRepository())).RotateEncryption(ctx, identity.Subject{Role: identity.RoleAdmin}, created.ID))
	rotated, err := repository.Get(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "new", rotated.KeyID)

	rotated.EncryptedToken[0] ^= 0xff
	_, err = newRing.OpenToken(rotated)
	assert.Error(t, err, "AES-GCM must reject modified ciphertext")
}

func TestMasterKeyMustBeInjectedAndCanLoadPreviousKeys(t *testing.T) {
	t.Setenv(EnvMasterKeyID, "active")
	t.Setenv(EnvMasterKey, base64.StdEncoding.EncodeToString(bytesOf(4)))
	t.Setenv(EnvPreviousMasterKeys, `{"previous":"`+base64.StdEncoding.EncodeToString(bytesOf(5))+`"}`)
	ring, err := LoadKeyringFromEnv()
	require.NoError(t, err)
	assert.Equal(t, "active", ring.ActiveKeyID())

	t.Setenv(EnvMasterKey, "")
	_, err = LoadKeyringFromEnv()
	assert.Error(t, err)
}

func TestModelMutationDoesNotStartWhenDurableAuditIntentFails(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	service := NewService(repository, mustTestKeyring(t, "current", bytesOf(6), nil), &failingModelRecorder{
		delegate: audit.NewService(audit.NewMemoryRepository()), failOn: 1,
	})
	owner := identity.Subject{UserID: "owner-id", Role: identity.RoleUser}

	_, err := service.Create(ctx, owner, CreateInput{Name: "never-created", Token: "plain-token", Scope: ScopePrivate})
	assert.Error(t, err)
	models, listErr := repository.List(ctx)
	require.NoError(t, listErr)
	assert.Empty(t, models)
}

func mustTestKeyring(t *testing.T, activeID string, active []byte, previous map[string][]byte) *Keyring {
	t.Helper()
	ring, err := NewKeyring(activeID, active, previous)
	require.NoError(t, err)
	return ring
}

func bytesOf(value byte) []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = value
	}
	return key
}
