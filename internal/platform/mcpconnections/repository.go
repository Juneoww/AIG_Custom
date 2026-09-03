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
)

// GormRepository 只操作已由 v10 迁移创建的 MCP 专用表；它不会自动建表，
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
	return nil
}

// Create 在同一事务中写入初始配置及其不可变版本。调用方不能借由输入字段伪造
// 已信任的测试结论：无论输入为何，v1 都必须是 disabled 和 not_tested。
func (repository *GormRepository) Create(ctx context.Context, config *ConnectionConfig, version *ConnectionVersion) error {
	if repository == nil || repository.db == nil || !validConfig(config) || !validVersionMaterial(version) || version.ConnectionConfigID != config.ID {
		return ErrInvalid
	}
	storedConfig := cloneConnectionConfig(config)
	storedVersion := cloneConnectionVersionRecord(version)
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
	storedVersion.Version = 1
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

func (repository *GormRepository) ListVersions(ctx context.Context, configID string) ([]ConnectionVersion, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" {
		return nil, ErrInvalid
	}
	var versions []ConnectionVersion
	err := txcontext.Gorm(ctx, repository.db).Where("connection_config_id = ?", configID).Order("version ASC").Find(&versions).Error
	return versions, err
}

// CreateNextVersion 只追加新的连接材料，不修改既有版本行。对配置行加锁可串行化
// 并发写入，使版本号在事务内单调递增，避免两个调用写入同一版本。
func (repository *GormRepository) CreateNextVersion(ctx context.Context, configID string, version *ConnectionVersion) (*ConnectionConfig, error) {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" || !validVersionMaterial(version) || version.ConnectionConfigID != configID {
		return nil, ErrInvalid
	}
	storedVersion := cloneConnectionVersionRecord(version)
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
		storedVersion.Version = config.CurrentVersion + 1
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

// RecordProbeResult 只修改启动探测时所指定版本的结果投影。更新列刻意排除连接
// 密文、nonce 和 key ID，从而确保完成的探测不能覆盖秘密材料或后续版本。
func (repository *GormRepository) RecordProbeResult(ctx context.Context, configID string, version int, detectedTransport Transport, status ProbeStatus) error {
	if repository == nil || repository.db == nil || strings.TrimSpace(configID) == "" || version < 1 || !validProbeResult(detectedTransport, status) {
		return ErrInvalid
	}
	result := txcontext.Gorm(ctx, repository.db).Model(&ConnectionVersion{}).
		Where("connection_config_id = ? AND version = ?", configID, version).
		Updates(map[string]any{"detected_transport": detectedTransport, "probe_status": status})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	return nil
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
	if detectedTransport != TransportHTTP && detectedTransport != TransportSSE && detectedTransport != TransportStdio {
		return false
	}
	return status == ProbeStatusPassed || status == ProbeStatusFailed
}

func validTaskBinding(binding *TaskBinding) bool {
	if binding == nil || strings.TrimSpace(binding.ID) == "" || strings.TrimSpace(binding.TaskID) == "" || strings.TrimSpace(binding.SourceKind) == "" {
		return false
	}
	hasConnectionConfigID := binding.ConnectionConfigID != nil && strings.TrimSpace(*binding.ConnectionConfigID) != ""
	hasConnectionConfigVersion := binding.ConnectionConfigVersion != nil && *binding.ConnectionConfigVersion > 0
	hasAnyConnectionReference := binding.ConnectionConfigID != nil || binding.ConnectionConfigVersion != nil
	hasRepositoryMaterial := len(binding.EncryptedRepositoryURL) > 0 && len(binding.RepositoryURLNonce) > 0 && strings.TrimSpace(binding.RepositoryURLKeyID) != ""
	hasAnyRepositoryMaterial := len(binding.EncryptedRepositoryURL) > 0 || len(binding.RepositoryURLNonce) > 0 || strings.TrimSpace(binding.RepositoryURLKeyID) != ""

	// 来源类型决定哪一组秘密材料能够出现。拒绝混合形状可防止调用方把仓库
	// 密文重放为服务引用（或反向重放），使后续解密和授权只有唯一的输入路径。
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

func cloneConnectionConfig(config *ConnectionConfig) *ConnectionConfig {
	copy := *config
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
