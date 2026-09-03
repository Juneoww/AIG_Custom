package mcpconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRepositoryCreatesDisabledUntestedVersionOne(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	repository := NewGormRepository(db)
	require.Error(t, repository.Init(), "a repository must reject an unmigrated schema")
	require.NoError(t, database.Migrate(db))
	require.NoError(t, repository.Init())

	createdAt := time.Date(2026, 9, 3, 1, 2, 3, 0, time.UTC)
	config := &ConnectionConfig{
		ID: "config-version-one-sentinel", OwnerUserID: "owner-version-one-sentinel", Scope: ScopePrivate,
		Name: "connection-name-sentinel", Description: "connection-description-sentinel", Enabled: true,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	version := &ConnectionVersion{
		ID: "version-one-sentinel", ConnectionConfigID: config.ID, Version: 99, Transport: TransportHTTP,
		EncryptedPayload: []byte("ciphertext-version-one-sentinel"), PayloadNonce: []byte("nonce-version-one-sentinel"), KeyID: "key-version-one-sentinel",
		DetectedTransport: TransportSSE, ProbeStatus: ProbeStatusPassed, CreatedAt: createdAt,
	}
	require.NoError(t, repository.Create(ctx, config, version))

	storedConfig, err := repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, storedConfig.CurrentVersion)
	assert.Equal(t, "1", storedConfig.ResourceRevision)
	assert.False(t, storedConfig.Enabled, "new connections cannot be enabled before a trusted test")

	storedVersion, err := repository.GetVersion(ctx, config.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, config.ID, storedVersion.ConnectionConfigID)
	assert.Equal(t, 1, storedVersion.Version)
	assert.Equal(t, version.EncryptedPayload, storedVersion.EncryptedPayload)
	assert.Equal(t, version.PayloadNonce, storedVersion.PayloadNonce)
	assert.Equal(t, version.KeyID, storedVersion.KeyID)
	assert.Equal(t, TransportHTTP, storedVersion.Transport)
	assert.Empty(t, storedVersion.DetectedTransport)
	assert.Equal(t, ProbeStatusNotTested, storedVersion.ProbeStatus)
}

func TestRepositoryVersionsConnectionMaterialAndLeavesMetadataOutOfHistory(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	require.NoError(t, repository.Init())

	config := testConnectionConfig("config-history-sentinel")
	first := testConnectionVersion(config.ID, "version-history-one-sentinel", "ciphertext-history-one-sentinel")
	require.NoError(t, repository.Create(ctx, config, first))
	require.NoError(t, repository.RecordProbeResult(ctx, config.ID, 1, TransportSSE, ProbeStatusPassed))
	testedFirst, err := repository.GetVersion(ctx, config.ID, 1)
	require.NoError(t, err)
	firstCiphertext := append([]byte(nil), testedFirst.EncryptedPayload...)

	second := testConnectionVersion(config.ID, "version-history-two-sentinel", "ciphertext-history-two-sentinel")
	second.Version = 999
	second.DetectedTransport = TransportSSE
	second.ProbeStatus = ProbeStatusPassed
	updated, err := repository.CreateNextVersion(ctx, config.ID, second)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.CurrentVersion)
	assert.Equal(t, "2", updated.ResourceRevision)
	assert.False(t, updated.Enabled)

	storedFirst, err := repository.GetVersion(ctx, config.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, firstCiphertext, storedFirst.EncryptedPayload, "a completed version's connection material is immutable")
	assert.Equal(t, TransportSSE, storedFirst.DetectedTransport)
	assert.Equal(t, ProbeStatusPassed, storedFirst.ProbeStatus)
	storedSecond, err := repository.GetVersion(ctx, config.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, second.EncryptedPayload, storedSecond.EncryptedPayload)
	assert.Equal(t, 2, storedSecond.Version)
	assert.Empty(t, storedSecond.DetectedTransport)
	assert.Equal(t, ProbeStatusNotTested, storedSecond.ProbeStatus)

	require.NoError(t, repository.RecordProbeResult(ctx, config.ID, 1, TransportStdio, ProbeStatusPassed))
	storedFirst, err = repository.GetVersion(ctx, config.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, TransportStdio, storedFirst.DetectedTransport, "a probe result must target the version that began the probe")
	storedSecond, err = repository.GetVersion(ctx, config.ID, 2)
	require.NoError(t, err)
	assert.Empty(t, storedSecond.DetectedTransport, "a probe result for v1 must not alter the current v2")
	assert.Equal(t, ProbeStatusNotTested, storedSecond.ProbeStatus)

	metadata, err := repository.UpdateDisplayMetadata(ctx, config.ID, "renamed-sentinel", "redescribed-sentinel")
	require.NoError(t, err)
	assert.Equal(t, 2, metadata.CurrentVersion, "display-only changes do not create a connection version")
	assert.Equal(t, "3", metadata.ResourceRevision)
	assert.Equal(t, "renamed-sentinel", metadata.Name)
	assert.Equal(t, "redescribed-sentinel", metadata.Description)
	versions, err := repository.ListVersions(ctx, config.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 2)
}

func TestRepositoryPersistsOnlyEncryptedRepositorySourceBinding(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	require.NoError(t, repository.Init())

	keyring, err := NewKeyring("repository-persisted-key-sentinel", bytes.Repeat([]byte{0x53}, 32), nil)
	require.NoError(t, err)
	bindingContext := BindingEncryptionContext{OwnerUserID: "repository-persisted-owner-sentinel", Scope: ScopePrivate, Version: 1}
	snapshot := RepositorySourceSnapshot{RepositoryURL: "repository-source-sentinel"}
	binding := &TaskBinding{
		ID: "binding-persisted-sentinel", TaskID: "task-persisted-sentinel", SourceKind: "repository",
		CreatedAt: time.Date(2026, 9, 3, 4, 5, 6, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 3, 4, 5, 6, 0, time.UTC),
	}
	require.NoError(t, keyring.SealRepositorySource(binding, bindingContext, snapshot))
	assert.True(t, strings.HasPrefix(binding.RepositoryURLKeyID, "mcp_connection_binding_v2:"), "sealed repository bindings must carry the v2 key-ID envelope")
	require.NoError(t, repository.CreateTaskBinding(ctx, binding))
	stored, err := repository.GetTaskBinding(ctx, binding.TaskID)
	require.NoError(t, err)
	assert.Equal(t, binding.EncryptedRepositoryURL, stored.EncryptedRepositoryURL)
	assert.Equal(t, binding.RepositoryURLNonce, stored.RepositoryURLNonce)
	assert.Equal(t, binding.RepositoryURLKeyID, stored.RepositoryURLKeyID)
	opened, err := keyring.OpenRepositorySource(stored, bindingContext)
	require.NoError(t, err)
	assert.Equal(t, snapshot, opened)
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "repository-source-sentinel")
}

func TestRepositoryRejectsIncompleteOrMixedTaskBindings(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	require.NoError(t, repository.Init())

	configID := "connection-binding-shape-sentinel"
	versionOne := 1
	versionZero := 0
	repositoryMaterial := func() (*[]byte, *[]byte, *string) {
		ciphertext := []byte("ciphertext-binding-shape-sentinel")
		nonce := []byte("nonce-binding-shape-sentinel")
		keyID := "key-binding-shape-sentinel"
		return &ciphertext, &nonce, &keyID
	}
	newBinding := func(id, sourceKind string) *TaskBinding {
		return &TaskBinding{ID: id, TaskID: "task-" + id, SourceKind: sourceKind}
	}
	withRepositoryMaterial := func(binding *TaskBinding) *TaskBinding {
		ciphertext, nonce, keyID := repositoryMaterial()
		binding.EncryptedRepositoryURL = append([]byte(nil), (*ciphertext)...)
		binding.RepositoryURLNonce = append([]byte(nil), (*nonce)...)
		binding.RepositoryURLKeyID = *keyID
		return binding
	}

	tests := []struct {
		name    string
		binding *TaskBinding
	}{
		{name: "service without connection reference", binding: newBinding("binding-service-no-reference-sentinel", "service")},
		{name: "service with only config ID", binding: func() *TaskBinding {
			binding := newBinding("binding-service-id-only-sentinel", "service")
			binding.ConnectionConfigID = &configID
			return binding
		}()},
		{name: "service with only config version", binding: func() *TaskBinding {
			binding := newBinding("binding-service-version-only-sentinel", "service")
			binding.ConnectionConfigVersion = &versionOne
			return binding
		}()},
		{name: "service with nonpositive config version", binding: func() *TaskBinding {
			binding := newBinding("binding-service-zero-version-sentinel", "service")
			binding.ConnectionConfigID = &configID
			binding.ConnectionConfigVersion = &versionZero
			return binding
		}()},
		{name: "service with repository material", binding: withRepositoryMaterial(newBinding("binding-service-mixed-sentinel", "service"))},
		{name: "repository without encrypted material", binding: newBinding("binding-repository-no-material-sentinel", "repository")},
		{name: "repository with incomplete encrypted material", binding: func() *TaskBinding {
			binding := newBinding("binding-repository-partial-material-sentinel", "repository")
			binding.EncryptedRepositoryURL = []byte("ciphertext-binding-shape-sentinel")
			binding.RepositoryURLKeyID = "key-binding-shape-sentinel"
			return binding
		}()},
		{name: "repository with legacy key ID", binding: withRepositoryMaterial(newBinding("binding-repository-legacy-key-id-sentinel", "repository"))},
		{name: "repository with empty binding-v2 key ID", binding: func() *TaskBinding {
			binding := newBinding("binding-repository-empty-v2-key-id-sentinel", "repository")
			binding.EncryptedRepositoryURL = []byte("ciphertext-binding-shape-sentinel")
			binding.RepositoryURLNonce = []byte("nonce-binding-shape-sentinel")
			binding.RepositoryURLKeyID = "mcp_connection_binding_v2:"
			return binding
		}()},
		{name: "repository with whitespace binding-v2 key ID", binding: func() *TaskBinding {
			binding := newBinding("binding-repository-whitespace-v2-key-id-sentinel", "repository")
			binding.EncryptedRepositoryURL = []byte("ciphertext-binding-shape-sentinel")
			binding.RepositoryURLNonce = []byte("nonce-binding-shape-sentinel")
			binding.RepositoryURLKeyID = "mcp_connection_binding_v2: key-binding-shape-sentinel "
			return binding
		}()},
		{name: "repository with connection reference", binding: func() *TaskBinding {
			binding := withRepositoryMaterial(newBinding("binding-repository-mixed-sentinel", "repository"))
			binding.ConnectionConfigID = &configID
			binding.ConnectionConfigVersion = &versionOne
			return binding
		}()},
		{name: "unknown source kind with repository material", binding: withRepositoryMaterial(newBinding("binding-unknown-source-sentinel", "unknown"))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorIs(t, repository.CreateTaskBinding(ctx, test.binding), ErrInvalid)
		})
	}
}

func testConnectionConfig(id string) *ConnectionConfig {
	now := time.Date(2026, 9, 3, 2, 3, 4, 0, time.UTC)
	return &ConnectionConfig{
		ID: id, OwnerUserID: "owner-history-sentinel", Scope: ScopePrivate,
		Name: "name-history-sentinel", Description: "description-history-sentinel", CreatedAt: now, UpdatedAt: now,
	}
}

func testConnectionVersion(configID, id, ciphertext string) *ConnectionVersion {
	return &ConnectionVersion{
		ID: id, ConnectionConfigID: configID, Version: 1, Transport: TransportHTTP,
		EncryptedPayload: []byte(ciphertext), PayloadNonce: []byte("nonce-" + id), KeyID: "key-history-sentinel",
		CreatedAt: time.Date(2026, 9, 3, 2, 3, 4, 0, time.UTC),
	}
}

func openMCPConnectionPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to the isolated PostgreSQL test service")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "mcp_connections_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	return db
}
