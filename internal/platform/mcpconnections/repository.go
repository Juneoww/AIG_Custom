package mcpconnections

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound = errors.New("MCP 连接配置不存在")
	ErrInvalid  = errors.New("MCP 连接配置无效")
	ErrConflict = errors.New("MCP 连接配置版本冲突")
)

// GormRepository 只操作由 v10 创建并由 v11 扩展的 MCP 专用表；它不会自动建表，
// 从而保证运行时不能绕过显式的 aig migrate 流程。
type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Init() error {
	if repository == nil || repository.db == nil {
		return errors.New("MCP 连接数据库不能为空")
	}
	for _, table := range []string{
		(ConnectionConfig{}).TableName(),
		(ConnectionVersion{}).TableName(),
		(TaskBinding{}).TableName(),
	} {
		if !repository.db.Migrator().HasTable(table) {
			return fmt.Errorf("MCP 连接数据库尚未迁移，请先运行 aig migrate：缺少表 %s", table)
		}
	}
	if !repository.db.Migrator().HasColumn((ConnectionConfig{}).TableName(), "last_probe_started_at") {
		return errors.New("MCP 连接数据库尚未迁移，请先运行 aig migrate：缺少列 platform_mcp_connection_configs.last_probe_started_at")
	}
	return nil
}

// Create 在同一事务中写入初始配置及其不可变版本。调用方不能借由输入字段伪造
// 已信任的测试结论：无论输入为何，v1 都必须是 disabled 和 not_tested。
func (repository *GormRepository) Create(ctx context.Context, config *ConnectionConfig, version *ConnectionVersion) error {
	if repository == nil || repository.db == nil || !validConfig(config) || !validVersionMaterial(version) || version.ConnectionConfigID != config.ID || version.Version != 1 {
		return ErrInvalid
	}
	storedConfig := cloneConnectionConfig(config)
	storedVersion := cloneConnectionVersionRecord(version)
	// 探测限流状态只能由 StartProbe 的锁定事务写入，创建请求不得预占或伪造它。
	storedConfig.LastProbeStartedAt = nil
	now := time.Now().UTC()
	if storedConfig.CreatedAt.IsZero() {
		storedConfig.CreatedAt = now
	}
	if storedConfig.UpdatedAt.IsZero() {
		storedConfig.UpdatedAt = storedConfig.CreatedAt
	}
	if storedVersion.CreatedAt.IsZero() {
		storedVersion.CreatedAt = storedConfig.CreatedAt
	}
	storedConfig.CurrentVersion = 1
	storedConfig.ResourceRevision = "1"
	storedConfig.Enabled = false
	storedVersion.DetectedTransport = ""
	storedVersion.ProbeStatus = ProbeStatusNotTested

	return txcontext.Gorm(ctx, repository.db).Transaction(func(transaction *gorm.DB) error {
		if err := transaction.Create(storedConfig).Error; err != nil {
			return err
		}
		return transaction.Create(storedVersion).Error
	})
}

func (repository *GormRepository) GetConfig(ctx context.Context, id string) (*ConnectionConfig, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(id) == "" {
		return nil, ErrInvalid
	}
	var config ConnectionConfig
	if err := txcontext.Gorm(ctx, repository.db).Where("id = ?", id).First(&config).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &config, nil
}

func (repository *GormRepository) GetVersion(ctx context.Context, configID string, version int) (*ConnectionVersion, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" || version < 1 {
		return nil, ErrInvalid
	}
	var record ConnectionVersion
	if err := txcontext.Gorm(ctx, repository.db).Where("connection_config_id = ? AND version = ?", configID, version).First(&record).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &record, nil
}

// LockCurrentForTask holds the config and its current immutable version under
// the caller's business transaction. Config writers follow this same lock
// order, preventing a task from binding a stale/disabled/probe-invalid version
// after it has passed service validation.
func (repository *GormRepository) LockCurrentForTask(ctx context.Context, configID string) (*ConnectionConfig, *ConnectionVersion, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" || !hasExplicitGormTransaction(ctx) {
		return nil, nil, ErrInvalid
	}
	database := txcontext.Gorm(ctx, repository.db)
	var config ConnectionConfig
	if err := database.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", configID).First(&config).Error; err != nil {
		return nil, nil, mapNotFound(err)
	}
	var version ConnectionVersion
	if err := database.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("connection_config_id = ? AND version = ?", config.ID, config.CurrentVersion).
		First(&version).Error; err != nil {
		return nil, nil, mapNotFound(err)
	}
	return &config, &version, nil
}

// ListConfigs 返回不含任何版本密文的配置元数据。服务层仍需逐项根据 Subject
// 执行 private/global 可见性过滤，仓储不能把数据库全表结果直接投影到浏览器。
func (repository *GormRepository) ListConfigs(ctx context.Context) ([]ConnectionConfig, error) {
	if repository == nil || repository.db == nil {
		return nil, ErrInvalid
	}
	var configs []ConnectionConfig
	err := txcontext.Gorm(ctx, repository.db).Order("created_at ASC, id ASC").Find(&configs).Error
	return configs, err
}

func (repository *GormRepository) ListVersions(ctx context.Context, configID string) ([]ConnectionVersion, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" {
		return nil, ErrInvalid
	}
	var versions []ConnectionVersion
	err := txcontext.Gorm(ctx, repository.db).Where("connection_config_id = ?", configID).Order("version ASC").Find(&versions).Error
	return versions, err
}

// CreateNextVersion 只追加新的连接材料，不修改既有版本行。调用方必须在密封前取得
// current version/resource revision 快照，并以该快照和明确的 next version 密封 AAD。
// 锁内会复核快照和版本，绝不在密封后重写 version.Version。
func (repository *GormRepository) CreateNextVersion(ctx context.Context, configID string, expectedCurrentVersion int, expectedResourceRevision string, version *ConnectionVersion) (*ConnectionConfig, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" || expectedCurrentVersion < 1 || strings.TrimSpace(expectedResourceRevision) == "" || !validVersionMaterial(version) || version.ConnectionConfigID != configID {
		return nil, ErrInvalid
	}
	storedVersion := cloneConnectionVersionRecord(version)
	var updated ConnectionConfig
	err := txcontext.Gorm(ctx, repository.db).Transaction(func(transaction *gorm.DB) error {
		var config ConnectionConfig
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", configID).First(&config).Error; err != nil {
			return mapNotFound(err)
		}
		if config.CurrentVersion != expectedCurrentVersion || config.ResourceRevision != expectedResourceRevision {
			return ErrConflict
		}
		if storedVersion.Version != config.CurrentVersion+1 {
			return ErrConflict
		}
		nextRevision, err := incrementRevision(config.ResourceRevision)
		if err != nil {
			return err
		}
		storedVersion.DetectedTransport = ""
		storedVersion.ProbeStatus = ProbeStatusNotTested
		if storedVersion.CreatedAt.IsZero() {
			storedVersion.CreatedAt = time.Now().UTC()
		}
		if err := transaction.Create(storedVersion).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		result := transaction.Model(&ConnectionConfig{}).Where("id = ?", configID).Updates(map[string]any{
			"current_version":   storedVersion.Version,
			"resource_revision": nextRevision,
			"enabled":           false,
			"updated_at":        now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
		config.CurrentVersion = storedVersion.Version
		config.ResourceRevision = nextRevision
		config.Enabled = false
		config.UpdatedAt = now
		updated = config
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// UpdateDisplayMetadata 仅变更可安全投影到浏览器的展示元数据；它绝不触碰连接
// 版本、密文字节、nonce 或 key ID，避免元数据编辑意外重写秘密材料。
func (repository *GormRepository) UpdateDisplayMetadata(ctx context.Context, configID, name, description string) (*ConnectionConfig, error) {
	name = strings.TrimSpace(name)
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" || name == "" {
		return nil, ErrInvalid
	}
	var updated ConnectionConfig
	err := txcontext.Gorm(ctx, repository.db).Transaction(func(transaction *gorm.DB) error {
		var config ConnectionConfig
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", configID).First(&config).Error; err != nil {
			return mapNotFound(err)
		}
		nextRevision, err := incrementRevision(config.ResourceRevision)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		result := transaction.Model(&ConnectionConfig{}).Where("id = ?", configID).Updates(map[string]any{
			"name":              name,
			"description":       description,
			"resource_revision": nextRevision,
			"updated_at":        now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
		config.Name = name
		config.Description = description
		config.ResourceRevision = nextRevision
		config.UpdatedAt = now
		updated = config
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// StartProbe 在真实网络操作前创建持久化的单调 attempt token。它按 config →
// current version 加锁，在锁内先核对快照和跨进程的 config-ID 限流，再将连接置为
// 不可用并清空旧结果；随后只有携带相同 token 的结果能够写回。
func (repository *GormRepository) StartProbe(ctx context.Context, configID string, expectedCurrentVersion int, expectedResourceRevision string, minimumInterval time.Duration) (*ProbeAttempt, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" || expectedCurrentVersion < 1 || strings.TrimSpace(expectedResourceRevision) == "" {
		return nil, ErrInvalid
	}
	minimumInterval = durableProbeInterval(minimumInterval)
	var attempt ProbeAttempt
	err := txcontext.Gorm(ctx, repository.db).Transaction(func(transaction *gorm.DB) error {
		var config ConnectionConfig
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", configID).First(&config).Error; err != nil {
			return mapNotFound(err)
		}
		if config.CurrentVersion != expectedCurrentVersion || config.ResourceRevision != expectedResourceRevision {
			return ErrConflict
		}
		var version ConnectionVersion
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("connection_config_id = ? AND version = ?", configID, config.CurrentVersion).First(&version).Error; err != nil {
			return mapNotFound(err)
		}
		if !configurableTransport(version.Transport) {
			return ErrInvalid
		}
		var now time.Time
		if err := transaction.Raw("SELECT clock_timestamp()").Scan(&now).Error; err != nil {
			return err
		}
		now = now.UTC()
		if config.LastProbeStartedAt != nil && now.Sub(*config.LastProbeStartedAt) < minimumInterval {
			return ErrProbeRateLimited
		}
		nextRevision, err := incrementRevision(config.ResourceRevision)
		if err != nil {
			return err
		}
		versionResult := transaction.Model(&ConnectionVersion{}).Where("connection_config_id = ? AND version = ?", configID, version.Version).
			Updates(map[string]any{"detected_transport": "", "probe_status": ProbeStatusNotTested})
		if versionResult.Error != nil {
			return versionResult.Error
		}
		if versionResult.RowsAffected != 1 {
			return ErrNotFound
		}
		configResult := transaction.Model(&ConnectionConfig{}).Where("id = ?", configID).Updates(map[string]any{
			"enabled":               false,
			"resource_revision":     nextRevision,
			"last_probe_started_at": now,
			"updated_at":            now,
		})
		if configResult.Error != nil {
			return configResult.Error
		}
		if configResult.RowsAffected != 1 {
			return ErrNotFound
		}
		attempt = ProbeAttempt{ConnectionConfigID: config.ID, Version: version.Version, Token: nextRevision}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &attempt, nil
}

// SetEnabled 在同一锁定事务内按 config → current version 的顺序复核资格，不能
// 只信任 Service 在锁外读取到的 passed 结果。若探测失败与启用交错，二者都会先
// 锁 config，因此最终状态不会保留 enabled + failed 的误导组合。
func (repository *GormRepository) SetEnabled(ctx context.Context, configID string, expectedCurrentVersion int, expectedResourceRevision string, enabled bool) (*ConnectionConfig, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" || expectedCurrentVersion < 1 || strings.TrimSpace(expectedResourceRevision) == "" {
		return nil, ErrInvalid
	}
	var updated ConnectionConfig
	unavailable := false
	err := txcontext.Gorm(ctx, repository.db).Transaction(func(transaction *gorm.DB) error {
		var config ConnectionConfig
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", configID).First(&config).Error; err != nil {
			return mapNotFound(err)
		}
		if config.CurrentVersion != expectedCurrentVersion || config.ResourceRevision != expectedResourceRevision {
			return ErrConflict
		}
		var version ConnectionVersion
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("connection_config_id = ? AND version = ?", configID, config.CurrentVersion).First(&version).Error; err != nil {
			return mapNotFound(err)
		}
		if enabled && (version.ProbeStatus != ProbeStatusPassed || !concreteProbeTransport(version.DetectedTransport)) {
			if err := disableUnavailableConfig(transaction, &config); err != nil {
				return err
			}
			// 不能在事务 closure 内返回 unavailable，否则上面的防御性 disable
			// 会被 GORM 回滚。先正常提交，事务外再返回安全的业务错误。
			unavailable = true
			updated = config
			return nil
		}
		nextRevision, err := incrementRevision(config.ResourceRevision)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		result := transaction.Model(&ConnectionConfig{}).Where("id = ?", configID).Updates(map[string]any{
			"enabled":           enabled,
			"resource_revision": nextRevision,
			"updated_at":        now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
		config.Enabled = enabled
		config.ResourceRevision = nextRevision
		config.UpdatedAt = now
		updated = config
		return nil
	})
	if err != nil {
		return nil, err
	}
	if unavailable {
		return nil, ErrTaskConnectionUnavailable
	}
	return &updated, nil
}

// RecordProbeResult 只接受与 StartProbe 返回 token 精确匹配的当前版本结果。它与
// SetEnabled 保持 config → version 的锁顺序，更新列刻意排除密文、nonce 和 key ID。
// 过期 Engine 的迟到成功或失败一律返回 ErrConflict，不能重写最新结论。
func (repository *GormRepository) RecordProbeResult(ctx context.Context, configID string, version int, attemptToken string, detectedTransport Transport, status ProbeStatus) error {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" || version < 1 || strings.TrimSpace(attemptToken) == "" || !validProbeResult(detectedTransport, status) {
		return ErrInvalid
	}
	return txcontext.Gorm(ctx, repository.db).Transaction(func(transaction *gorm.DB) error {
		var config ConnectionConfig
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", configID).First(&config).Error; err != nil {
			return mapNotFound(err)
		}
		if config.CurrentVersion != version || config.ResourceRevision != attemptToken {
			return ErrConflict
		}
		var storedVersion ConnectionVersion
		if err := transaction.Clauses(clause.Locking{Strength: "UPDATE"}).Where("connection_config_id = ? AND version = ?", configID, version).First(&storedVersion).Error; err != nil {
			return mapNotFound(err)
		}
		if storedVersion.ProbeStatus != ProbeStatusNotTested || storedVersion.DetectedTransport != "" {
			// 同一个 attempt 只能结算一次。开始新 attempt 会将状态重置为
			// not_tested，但也会换发新的 token；没有 token 的旧结果不能重放。
			return ErrConflict
		}
		nextRevision, err := incrementRevision(config.ResourceRevision)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		result := transaction.Model(&ConnectionVersion{}).Where("connection_config_id = ? AND version = ?", configID, version).
			Updates(map[string]any{"detected_transport": detectedTransport, "probe_status": status})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrNotFound
		}
		// 结果本身也是一次状态变更：无论成功或失败都推进 revision，使重复
		// delivery 不能再次匹配同一 attempt token；失败时同时撤销 enabled。
		configResult := transaction.Model(&ConnectionConfig{}).Where("id = ?", configID).Updates(map[string]any{
			"enabled":           status != ProbeStatusFailed && config.Enabled,
			"resource_revision": nextRevision,
			"updated_at":        now,
		})
		if configResult.Error != nil {
			return configResult.Error
		}
		if configResult.RowsAffected != 1 {
			return ErrNotFound
		}
		return nil
	})
}

func (repository *GormRepository) CreateTaskBinding(ctx context.Context, binding *TaskBinding) error {
	if repository == nil || repository.db == nil || !validTaskBinding(binding) {
		return ErrInvalid
	}
	stored := cloneTaskBindingRecord(binding)
	now := time.Now().UTC()
	if stored.CreatedAt.IsZero() {
		stored.CreatedAt = now
	}
	if stored.UpdatedAt.IsZero() {
		stored.UpdatedAt = stored.CreatedAt
	}
	return txcontext.Gorm(ctx, repository.db).Create(stored).Error
}

func (repository *GormRepository) GetTaskBinding(ctx context.Context, taskID string) (*TaskBinding, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(taskID) == "" {
		return nil, ErrInvalid
	}
	var binding TaskBinding
	if err := txcontext.Gorm(ctx, repository.db).Where("task_id = ?", taskID).First(&binding).Error; err != nil {
		return nil, mapNotFound(err)
	}
	return &binding, nil
}

func validConfig(config *ConnectionConfig) bool {
	if config == nil || strings.TrimSpace(config.ID) == "" || strings.TrimSpace(config.Name) == "" {
		return false
	}
	if config.Scope != ScopePrivate && config.Scope != ScopeGlobal {
		return false
	}
	return config.Scope != ScopePrivate || strings.TrimSpace(config.OwnerUserID) != ""
}

func validVersionMaterial(version *ConnectionVersion) bool {
	return version != nil && strings.TrimSpace(version.ID) != "" && strings.TrimSpace(version.ConnectionConfigID) != "" &&
		len(version.EncryptedPayload) > 0 && len(version.PayloadNonce) > 0 && strings.TrimSpace(version.KeyID) != "" && version.Transport != ""
}

func validProbeResult(detectedTransport Transport, status ProbeStatus) bool {
	switch status {
	case ProbeStatusPassed:
		return detectedTransport == TransportHTTP || detectedTransport == TransportSSE || detectedTransport == TransportStdio
	case ProbeStatusFailed:
		// failed 不应保留上一次成功的 transport，以免任何读取路径把过期成功
		// 当成可用性信号。调用方必须明确清空它。
		return detectedTransport == ""
	default:
		return false
	}
}

func disableUnavailableConfig(transaction *gorm.DB, config *ConnectionConfig) error {
	if transaction == nil || config == nil || !config.Enabled {
		return nil
	}
	nextRevision, err := incrementRevision(config.ResourceRevision)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	result := transaction.Model(&ConnectionConfig{}).Where("id = ?", config.ID).Updates(map[string]any{
		"enabled":           false,
		"resource_revision": nextRevision,
		"updated_at":        now,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	config.Enabled = false
	config.ResourceRevision = nextRevision
	config.UpdatedAt = now
	return nil
}

func validTaskBinding(binding *TaskBinding) bool {
	if binding == nil || strings.TrimSpace(binding.ID) == "" || strings.TrimSpace(binding.TaskID) == "" || strings.TrimSpace(binding.SourceKind) == "" {
		return false
	}
	hasConnectionConfigID := binding.ConnectionConfigID != nil && strings.TrimSpace(*binding.ConnectionConfigID) != ""
	hasConnectionConfigVersion := binding.ConnectionConfigVersion != nil && *binding.ConnectionConfigVersion > 0
	hasAnyConnectionReference := binding.ConnectionConfigID != nil || binding.ConnectionConfigVersion != nil
	_, repositoryKeyIDErr := bindingV2KeyID(binding.RepositoryURLKeyID)
	hasRepositoryMaterial := len(binding.EncryptedRepositoryURL) > 0 && len(binding.RepositoryURLNonce) > 0 && repositoryKeyIDErr == nil
	hasAnyRepositoryMaterial := len(binding.EncryptedRepositoryURL) > 0 || len(binding.RepositoryURLNonce) > 0 || strings.TrimSpace(binding.RepositoryURLKeyID) != ""

	// 来源类型决定哪一组秘密材料能够出现。拒绝混合形状可防止调用方把仓库
	// 密文重放为服务引用（或反向重放），并要求写入格式与 Open 的 v2 key-ID
	// 解析一致，使后续解密和授权只有唯一的输入路径。
	switch binding.SourceKind {
	case "service":
		return hasConnectionConfigID && hasConnectionConfigVersion && !hasAnyRepositoryMaterial
	case "repository":
		return hasRepositoryMaterial && !hasAnyConnectionReference
	default:
		return false
	}
}

func incrementRevision(revision string) (string, error) {
	if revision == "" {
		return "1", nil
	}
	value, err := strconv.ParseUint(revision, 10, 64)
	if err != nil || value == ^uint64(0) {
		return "", ErrInvalid
	}
	return strconv.FormatUint(value+1, 10), nil
}

func mapNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func hasExplicitGormTransaction(ctx context.Context) bool {
	database, carried := txcontext.FromGorm(ctx)
	if !carried || database == nil || database.Statement == nil || database.Statement.ConnPool == nil {
		return false
	}
	_, transactional := database.Statement.ConnPool.(interface {
		Commit() error
		Rollback() error
	})
	return transactional
}

func cloneConnectionConfig(config *ConnectionConfig) *ConnectionConfig {
	copy := *config
	if config.LastProbeStartedAt != nil {
		value := *config.LastProbeStartedAt
		copy.LastProbeStartedAt = &value
	}
	return &copy
}

func cloneConnectionVersionRecord(version *ConnectionVersion) *ConnectionVersion {
	copy := *version
	copy.EncryptedPayload = append([]byte(nil), version.EncryptedPayload...)
	copy.PayloadNonce = append([]byte(nil), version.PayloadNonce...)
	return &copy
}

func cloneTaskBindingRecord(binding *TaskBinding) *TaskBinding {
	copy := *binding
	copy.EncryptedRepositoryURL = append([]byte(nil), binding.EncryptedRepositoryURL...)
	copy.RepositoryURLNonce = append([]byte(nil), binding.RepositoryURLNonce...)
	if binding.ConnectionConfigID != nil {
		value := *binding.ConnectionConfigID
		copy.ConnectionConfigID = &value
	}
	if binding.ConnectionConfigVersion != nil {
		value := *binding.ConnectionConfigVersion
		copy.ConnectionConfigVersion = &value
	}
	return &copy
}
