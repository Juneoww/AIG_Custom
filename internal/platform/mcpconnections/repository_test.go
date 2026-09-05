package mcpconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type delayedProbePort struct {
	started chan<- struct{}
	release <-chan struct{}
	result  error
}

func (port *delayedProbePort) Initialize(_ context.Context, _ ProbeRequest) error {
	select {
	case port.started <- struct{}{}:
	default:
	}
	<-port.release
	return port.result
}

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
	firstAttempt, err := repository.StartProbe(ctx, config.ID, 1, "1")
	require.NoError(t, err)
	require.NoError(t, repository.RecordProbeResult(ctx, config.ID, 1, firstAttempt.Token, TransportSSE, ProbeStatusPassed))
	testedFirst, err := repository.GetVersion(ctx, config.ID, 1)
	require.NoError(t, err)
	firstCiphertext := append([]byte(nil), testedFirst.EncryptedPayload...)

	second := testConnectionVersion(config.ID, "version-history-two-sentinel", "ciphertext-history-two-sentinel")
	second.Version = 2
	second.DetectedTransport = TransportSSE
	second.ProbeStatus = ProbeStatusPassed
	updated, err := repository.CreateNextVersion(ctx, config.ID, 1, "3", second)
	require.NoError(t, err)
	assert.Equal(t, 2, updated.CurrentVersion)
	assert.Equal(t, "4", updated.ResourceRevision)
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

	require.ErrorIs(t, repository.RecordProbeResult(ctx, config.ID, 1, firstAttempt.Token, TransportStdio, ProbeStatusPassed), ErrConflict, "a completed or stale attempt cannot be replayed")
	storedFirst, err = repository.GetVersion(ctx, config.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, TransportSSE, storedFirst.DetectedTransport, "a stale result must not overwrite the completed version")
	storedSecond, err = repository.GetVersion(ctx, config.ID, 2)
	require.NoError(t, err)
	assert.Empty(t, storedSecond.DetectedTransport, "a probe result for v1 must not alter the current v2")
	assert.Equal(t, ProbeStatusNotTested, storedSecond.ProbeStatus)

	metadata, err := repository.UpdateDisplayMetadata(ctx, config.ID, "renamed-sentinel", "redescribed-sentinel")
	require.NoError(t, err)
	assert.Equal(t, 2, metadata.CurrentVersion, "display-only changes do not create a connection version")
	assert.Equal(t, "5", metadata.ResourceRevision)
	assert.Equal(t, "renamed-sentinel", metadata.Name)
	assert.Equal(t, "redescribed-sentinel", metadata.Description)
	versions, err := repository.ListVersions(ctx, config.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 2)
}

func TestRepositoryCreateNextVersionPreservesSealedVersionAndRejectsStaleSnapshot(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	keyring, err := NewKeyring("aes-gcm-version-contract", bytes.Repeat([]byte{0x41}, 32), nil)
	require.NoError(t, err)

	config := testConnectionConfig("config-sealed-version-contract")
	first := testConnectionVersion(config.ID, "version-sealed-version-one", "placeholder-version-one")
	firstPayload := ConnectionPayload{Endpoint: "https://service.example.test/mcp", Authentication: Authentication{Kind: AuthenticationNone}}
	require.NoError(t, keyring.SealConnectionPayload(config, first, firstPayload))
	require.NoError(t, repository.Create(ctx, config, first))

	wrongNext := testConnectionVersion(config.ID, "version-sealed-version-wrong-next", "placeholder-version-wrong-next")
	wrongNext.Version = 3
	require.NoError(t, keyring.SealConnectionPayload(config, wrongNext, firstPayload))
	_, err = repository.CreateNextVersion(ctx, config.ID, 1, "1", wrongNext)
	require.ErrorIs(t, err, ErrConflict, "the repository must not rewrite a sealed version to make it fit")

	second := testConnectionVersion(config.ID, "version-sealed-version-two", "placeholder-version-two")
	second.Version = 2
	secondPayload := ConnectionPayload{
		Endpoint:       "https://service.example.test/rotated",
		Authentication: Authentication{Kind: AuthenticationBearer, Secret: "rotated-secret"},
		Headers:        []Header{{Name: "X-Internal-Route", Value: "tenant-a"}},
	}
	require.NoError(t, keyring.SealConnectionPayload(config, second, secondPayload))

	updated, err := repository.CreateNextVersion(ctx, config.ID, 1, "1", second)
	require.NoError(t, err)
	require.Equal(t, 2, updated.CurrentVersion)
	storedConfig, err := repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	storedSecond, err := repository.GetVersion(ctx, config.ID, 2)
	require.NoError(t, err)
	opened, err := keyring.OpenConnectionPayload(storedConfig, storedSecond)
	require.NoError(t, err, "the version used to seal AAD must be the version stored by the repository")
	assert.Equal(t, secondPayload, opened)

	stale := testConnectionVersion(config.ID, "version-sealed-version-three", "placeholder-version-three")
	stale.Version = 3
	require.NoError(t, keyring.SealConnectionPayload(config, stale, secondPayload))
	_, err = repository.CreateNextVersion(ctx, config.ID, 1, "1", stale)
	require.ErrorIs(t, err, ErrConflict)
	storedConfig, err = repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, storedConfig.CurrentVersion)
}

func TestRepositorySetEnabledRejectsStaleCurrentVersion(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	config := testConnectionConfig("config-enable-stale-sentinel")
	first := testConnectionVersion(config.ID, "version-enable-stale-one-sentinel", "ciphertext-enable-stale-one-sentinel")
	require.NoError(t, repository.Create(ctx, config, first))

	second := testConnectionVersion(config.ID, "version-enable-stale-two-sentinel", "ciphertext-enable-stale-two-sentinel")
	second.Version = 2
	_, err := repository.CreateNextVersion(ctx, config.ID, 1, "1", second)
	require.NoError(t, err)

	_, err = repository.SetEnabled(ctx, config.ID, 1, "1", true)
	require.ErrorIs(t, err, ErrConflict)
	stored, err := repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)
	assert.Equal(t, 2, stored.CurrentVersion)
}

func TestRepositorySetEnabledRequiresPassedCurrentVersionInsideTransaction(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	config := testConnectionConfig("config-enable-current-status-sentinel")
	version := testConnectionVersion(config.ID, "version-enable-current-status-sentinel", "ciphertext-enable-current-status-sentinel")
	require.NoError(t, repository.Create(ctx, config, version))

	_, err := repository.SetEnabled(ctx, config.ID, 1, "1", true)
	require.ErrorIs(t, err, ErrTaskConnectionUnavailable)
	stored, err := repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)
}

func TestRepositorySetEnabledPersistsDefensiveDisableForFailedCurrentVersion(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	config := testConnectionConfig("config-enable-persisted-disable-sentinel")
	version := testConnectionVersion(config.ID, "version-enable-persisted-disable-sentinel", "ciphertext-enable-persisted-disable-sentinel")
	require.NoError(t, repository.Create(ctx, config, version))

	// 模拟旧进程或故障恢复留下的 enabled + failed 不一致状态；防御性禁用必须
	// 在返回 unavailable 前提交，不能被 transaction 内的返回错误回滚。
	require.NoError(t, db.Model(&ConnectionConfig{}).Where("id = ?", config.ID).Update("enabled", true).Error)
	require.NoError(t, db.Model(&ConnectionVersion{}).
		Where("connection_config_id = ? AND version = ?", config.ID, 1).
		Updates(map[string]any{"probe_status": ProbeStatusFailed, "detected_transport": ""}).Error)

	_, err := repository.SetEnabled(ctx, config.ID, 1, "1", true)
	require.ErrorIs(t, err, ErrTaskConnectionUnavailable)
	stored, err := repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)
	assert.Equal(t, "2", stored.ResourceRevision, "the defensive disable must commit before unavailable is returned")
}

func TestServiceSetEnabledPersistsDefensiveDisableForHistoricalFailedCurrentVersion(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	config := testConnectionConfig("config-service-enable-persisted-disable-sentinel")
	config.OwnerUserID = "owner-service-enable-persisted-disable-sentinel"
	version := testConnectionVersion(config.ID, "version-service-enable-persisted-disable-sentinel", "ciphertext-service-enable-persisted-disable-sentinel")
	require.NoError(t, repository.Create(ctx, config, version))

	// 模拟旧进程或故障恢复留下的 enabled + failed 不一致状态。公开 Service
	// 路径必须进入仓储层锁内防御性禁用，不能在锁外资格预检直接返回。
	require.NoError(t, db.Model(&ConnectionConfig{}).Where("id = ?", config.ID).Update("enabled", true).Error)
	require.NoError(t, db.Model(&ConnectionVersion{}).
		Where("connection_config_id = ? AND version = ?", config.ID, 1).
		Updates(map[string]any{"probe_status": ProbeStatusFailed, "detected_transport": ""}).Error)

	service := NewService(repository, testKeyring(t), nil, testPolicy(t, true))
	_, err := service.SetEnabled(ctx, identity.Subject{UserID: config.OwnerUserID, Role: identity.RoleUser}, config.ID, true)
	require.ErrorIs(t, err, ErrTaskConnectionUnavailable)

	stored, err := repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)
	assert.Equal(t, "2", stored.ResourceRevision, "the public service path must commit defensive disable before unavailable is returned")
}

func TestServiceSetEnabledWithoutControlledDialerDefensivelyDisablesHistoricalUnavailableConfig(t *testing.T) {
	for _, test := range []struct {
		name        string
		probeStatus ProbeStatus
	}{
		{name: "failed", probeStatus: ProbeStatusFailed},
		{name: "not tested", probeStatus: ProbeStatusNotTested},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := openMCPConnectionPostgresDB(t)
			require.NoError(t, database.Migrate(db))
			repository := NewGormRepository(db)
			config := testConnectionConfig("config-service-no-gateway-historical-" + strings.ReplaceAll(test.name, " ", "-"))
			config.OwnerUserID = "owner-service-no-gateway-historical-" + strings.ReplaceAll(test.name, " ", "-")
			version := testConnectionVersion(config.ID, "version-service-no-gateway-historical-"+strings.ReplaceAll(test.name, " ", "-"), "ciphertext-service-no-gateway-historical-"+strings.ReplaceAll(test.name, " ", "-"))
			require.NoError(t, repository.Create(ctx, config, version))

			// 模拟旧进程或故障恢复遗留的 enabled + unavailable 组合。即使本次
			// 部署没有受控网关，Service 也必须让仓储层在锁内修复此状态。
			require.NoError(t, db.Model(&ConnectionConfig{}).Where("id = ?", config.ID).Update("enabled", true).Error)
			require.NoError(t, db.Model(&ConnectionVersion{}).
				Where("connection_config_id = ? AND version = ?", config.ID, 1).
				Updates(map[string]any{"probe_status": test.probeStatus, "detected_transport": ""}).Error)

			service := NewService(repository, testKeyring(t), nil, testPolicy(t, false))
			_, err := service.SetEnabled(ctx, identity.Subject{UserID: config.OwnerUserID, Role: identity.RoleUser}, config.ID, true)
			require.ErrorIs(t, err, ErrTaskConnectionUnavailable)

			stored, err := repository.GetConfig(ctx, config.ID)
			require.NoError(t, err)
			assert.False(t, stored.Enabled)
			assert.Equal(t, "2", stored.ResourceRevision, "the defensive disable must be committed before unavailable is returned")
		})
	}
}

func TestServiceSetEnabledWithoutControlledDialerLeavesNewUntestedConfigUntouched(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	config := testConnectionConfig("config-service-no-gateway-new-untested")
	config.OwnerUserID = "owner-service-no-gateway-new-untested"
	version := testConnectionVersion(config.ID, "version-service-no-gateway-new-untested", "ciphertext-service-no-gateway-new-untested")
	require.NoError(t, repository.Create(ctx, config, version))

	before, err := repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.False(t, before.Enabled)
	assert.Equal(t, "1", before.ResourceRevision)

	service := NewService(repository, testKeyring(t), nil, testPolicy(t, false))
	_, err = service.SetEnabled(ctx, identity.Subject{UserID: config.OwnerUserID, Role: identity.RoleUser}, config.ID, true)
	require.ErrorIs(t, err, ErrControlledEgressRequired)

	after, err := repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.False(t, after.Enabled)
	assert.Equal(t, before.ResourceRevision, after.ResourceRevision, "a normal disabled connection must not be mutated when no controlled gateway is available")
}

func TestRepositoryRejectsOutOfOrderProbeResultAcrossPersistentRepositories(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	firstProcess := NewGormRepository(db)
	secondProcess := NewGormRepository(db)
	config := testConnectionConfig("config-probe-attempt-order-sentinel")
	version := testConnectionVersion(config.ID, "version-probe-attempt-order-sentinel", "ciphertext-probe-attempt-order-sentinel")
	require.NoError(t, firstProcess.Create(ctx, config, version))

	firstAttempt, err := firstProcess.StartProbe(ctx, config.ID, 1, "1")
	require.NoError(t, err)
	secondAttempt, err := secondProcess.StartProbe(ctx, config.ID, firstAttempt.Version, firstAttempt.Token)
	require.NoError(t, err)
	require.NotEqual(t, firstAttempt.Token, secondAttempt.Token)

	// 第二个 Engine 的较晚失败结论先持久化；第一个 Engine 随后才完成的成功
	// 结果必须因 attempt token 已失效而被拒绝，不能重新放行此连接。
	require.NoError(t, secondProcess.RecordProbeResult(ctx, config.ID, secondAttempt.Version, secondAttempt.Token, "", ProbeStatusFailed))
	require.ErrorIs(t, firstProcess.RecordProbeResult(ctx, config.ID, firstAttempt.Version, firstAttempt.Token, TransportHTTP, ProbeStatusPassed), ErrConflict)

	storedConfig, err := firstProcess.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.False(t, storedConfig.Enabled)
	storedVersion, err := firstProcess.GetVersion(ctx, config.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, ProbeStatusFailed, storedVersion.ProbeStatus)
	assert.Empty(t, storedVersion.DetectedTransport)
	_, err = firstProcess.SetEnabled(ctx, config.ID, storedConfig.CurrentVersion, storedConfig.ResourceRevision, true)
	require.ErrorIs(t, err, ErrTaskConnectionUnavailable)
}

func TestServiceRejectsDelayedSuccessAfterAnotherEnginePersistsFailure(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	firstRepository := NewGormRepository(db)
	secondRepository := NewGormRepository(db)
	keyring := testKeyring(t)
	policy := testPolicy(t, true)
	alice := identity.Subject{UserID: "probe-order-owner-sentinel", Role: identity.RoleUser}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	firstService := NewService(firstRepository, keyring, NewProbeEngine(&delayedProbePort{started: started, release: release}, ProbeOptions{
		Timeout: time.Second, MinimumInterval: time.Hour,
	}), policy)
	secondService := NewService(secondRepository, keyring, NewProbeEngine(&scriptedProbePort{errors: map[Transport]error{
		TransportHTTP: errors.New("second probe failure"),
	}}, ProbeOptions{Timeout: time.Second, MinimumInterval: time.Hour}), policy)

	created, err := firstService.Create(ctx, alice, serviceInput("跨引擎探测", ScopePrivate, TransportHTTP))
	require.NoError(t, err)
	firstDone := make(chan error, 1)
	go func() {
		_, probeErr := firstService.Probe(ctx, alice, created.ID)
		firstDone <- probeErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("the first Engine did not persist and begin its probe")
	}

	_, err = secondService.Probe(ctx, alice, created.ID)
	require.ErrorIs(t, err, ErrProbeFailed)
	close(release)
	select {
	case delayedErr := <-firstDone:
		require.ErrorIs(t, delayedErr, ErrConflict, "the delayed success must not overwrite the later failure")
	case <-time.After(time.Second):
		t.Fatal("the delayed probe did not complete")
	}

	storedConfig, err := firstRepository.GetConfig(ctx, created.ID)
	require.NoError(t, err)
	assert.False(t, storedConfig.Enabled)
	storedVersion, err := firstRepository.GetVersion(ctx, created.ID, storedConfig.CurrentVersion)
	require.NoError(t, err)
	assert.Equal(t, ProbeStatusFailed, storedVersion.ProbeStatus)
	assert.Empty(t, storedVersion.DetectedTransport)
	_, err = firstService.SetEnabled(ctx, alice, created.ID, true)
	require.ErrorIs(t, err, ErrTaskConnectionUnavailable)
}

func TestRepositoryConfigurationMutationInvalidatesStartedProbeAttempt(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(context.Context, *GormRepository, *ConnectionConfig, string) error
	}{
		{
			name: "display metadata",
			mutate: func(ctx context.Context, repository *GormRepository, config *ConnectionConfig, _ string) error {
				_, err := repository.UpdateDisplayMetadata(ctx, config.ID, "updated-name-sentinel", "updated-description-sentinel")
				return err
			},
		},
		{
			name: "new version",
			mutate: func(ctx context.Context, repository *GormRepository, config *ConnectionConfig, token string) error {
				next := testConnectionVersion(config.ID, "version-probe-attempt-next-sentinel", "ciphertext-probe-attempt-next-sentinel")
				next.Version = 2
				_, err := repository.CreateNextVersion(ctx, config.ID, 1, token, next)
				return err
			},
		},
		{
			name: "enable mutation",
			mutate: func(ctx context.Context, repository *GormRepository, config *ConnectionConfig, token string) error {
				_, err := repository.SetEnabled(ctx, config.ID, 1, token, false)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := openMCPConnectionPostgresDB(t)
			require.NoError(t, database.Migrate(db))
			repository := NewGormRepository(db)
			config := testConnectionConfig("config-probe-attempt-mutation-" + strings.ReplaceAll(test.name, " ", "-"))
			version := testConnectionVersion(config.ID, "version-probe-attempt-mutation-"+strings.ReplaceAll(test.name, " ", "-"), "ciphertext-probe-attempt-mutation-"+strings.ReplaceAll(test.name, " ", "-"))
			require.NoError(t, repository.Create(ctx, config, version))

			attempt, err := repository.StartProbe(ctx, config.ID, 1, "1")
			require.NoError(t, err)
			require.NoError(t, test.mutate(ctx, repository, config, attempt.Token))
			require.ErrorIs(t, repository.RecordProbeResult(ctx, config.ID, attempt.Version, attempt.Token, TransportHTTP, ProbeStatusPassed), ErrConflict)

			startedVersion, err := repository.GetVersion(ctx, config.ID, 1)
			require.NoError(t, err)
			assert.Equal(t, ProbeStatusNotTested, startedVersion.ProbeStatus)
			assert.Empty(t, startedVersion.DetectedTransport)
		})
	}
}

func TestRepositoryFailedCurrentProbeDisablesConnectionAndOldVersionDoesNotChangeCurrent(t *testing.T) {
	ctx := context.Background()
	db := openMCPConnectionPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	config := testConnectionConfig("config-probe-failure-state-sentinel")
	first := testConnectionVersion(config.ID, "version-probe-failure-one-sentinel", "ciphertext-probe-failure-one-sentinel")
	require.NoError(t, repository.Create(ctx, config, first))
	passedAttempt, err := repository.StartProbe(ctx, config.ID, 1, "1")
	require.NoError(t, err)
	require.NoError(t, repository.RecordProbeResult(ctx, config.ID, 1, passedAttempt.Token, TransportHTTP, ProbeStatusPassed))
	stored, err := repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	_, err = repository.SetEnabled(ctx, config.ID, 1, stored.ResourceRevision, true)
	require.NoError(t, err)

	stored, err = repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	failedAttempt, err := repository.StartProbe(ctx, config.ID, 1, stored.ResourceRevision)
	require.NoError(t, err)
	require.NoError(t, repository.RecordProbeResult(ctx, config.ID, 1, failedAttempt.Token, "", ProbeStatusFailed))
	stored, err = repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)
	failed, err := repository.GetVersion(ctx, config.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, ProbeStatusFailed, failed.ProbeStatus)
	assert.Empty(t, failed.DetectedTransport)
	_, err = repository.SetEnabled(ctx, config.ID, stored.CurrentVersion, stored.ResourceRevision, true)
	require.ErrorIs(t, err, ErrTaskConnectionUnavailable)

	second := testConnectionVersion(config.ID, "version-probe-failure-two-sentinel", "ciphertext-probe-failure-two-sentinel")
	second.Version = 2
	_, err = repository.CreateNextVersion(ctx, config.ID, stored.CurrentVersion, stored.ResourceRevision, second)
	require.NoError(t, err)
	require.ErrorIs(t, repository.RecordProbeResult(ctx, config.ID, 1, failedAttempt.Token, "", ProbeStatusFailed), ErrConflict, "a stale probe result cannot alter an old version after a new configuration version exists")
	current, err := repository.GetVersion(ctx, config.ID, 2)
	require.NoError(t, err)
	assert.Equal(t, ProbeStatusNotTested, current.ProbeStatus)
	assert.Empty(t, current.DetectedTransport)
	stored, err = repository.GetConfig(ctx, config.ID)
	require.NoError(t, err)
	assert.False(t, stored.Enabled)
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
