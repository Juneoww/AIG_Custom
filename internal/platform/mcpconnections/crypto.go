package mcpconnections

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	EnvMasterKey          = "MCP_CONNECTION_MASTER_KEY"
	EnvMasterKeyID        = "MCP_CONNECTION_MASTER_KEY_ID"
	EnvPreviousMasterKeys = "MCP_CONNECTION_PREVIOUS_MASTER_KEYS"

	connectionPayloadFieldDomain = "connection_payload"
	repositorySourceFieldDomain  = "repository_source"
	keyringAADNamespace          = "mcp_connection_v1"
	bindingAADNamespaceV2        = "mcp_connection_binding_v2"
	bindingKeyIDV2Prefix         = bindingAADNamespaceV2 + ":"
)

var ErrLegacyBindingEncryptionFormat = errors.New("MCP 仓库来源使用不支持的旧绑定加密格式，需要重新创建任务绑定")

// Keyring 是 MCP 连接资源专用的密钥集合。它与模型密钥和模型 AAD 完全隔离，
// 以防一个资源域的密文被错误地当作另一个资源域的密文接受。
type Keyring struct {
	activeID string
	keys     map[string][]byte
}

func NewKeyring(activeID string, activeKey []byte, previous map[string][]byte) (*Keyring, error) {
	activeID = strings.TrimSpace(activeID)
	if activeID == "" {
		return nil, errors.New("MCP_CONNECTION_MASTER_KEY_ID 不能为空")
	}
	if err := validateKey(activeKey); err != nil {
		return nil, fmt.Errorf("活动 MCP 连接主密钥无效: %w", err)
	}

	keys := make(map[string][]byte, len(previous)+1)
	for id, key := range previous {
		id = strings.TrimSpace(id)
		if id == "" || id == activeID {
			return nil, errors.New("历史 MCP 连接主密钥 ID 无效或与活动 ID 重复")
		}
		if err := validateKey(key); err != nil {
			return nil, fmt.Errorf("历史 MCP 连接主密钥 %q 无效: %w", id, err)
		}
		keys[id] = append([]byte(nil), key...)
	}
	keys[activeID] = append([]byte(nil), activeKey...)
	return &Keyring{activeID: activeID, keys: keys}, nil
}

func LoadKeyringFromEnv() (*Keyring, error) {
	activeID := os.Getenv(EnvMasterKeyID)
	active, err := decodeKey(os.Getenv(EnvMasterKey))
	if err != nil {
		return nil, fmt.Errorf("读取活动 MCP 连接主密钥失败: %w", err)
	}

	previousEncoded := map[string]string{}
	if raw := strings.TrimSpace(os.Getenv(EnvPreviousMasterKeys)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &previousEncoded); err != nil {
			return nil, fmt.Errorf("MCP_CONNECTION_PREVIOUS_MASTER_KEYS 必须是 key-id 到 base64 密钥的 JSON 对象: %w", err)
		}
	}
	previous := make(map[string][]byte, len(previousEncoded))
	for id, encoded := range previousEncoded {
		key, err := decodeKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("读取历史 MCP 连接主密钥 %q 失败: %w", id, err)
		}
		previous[id] = key
	}
	return NewKeyring(activeID, active, previous)
}

func (keyring *Keyring) ActiveKeyID() string { return keyring.activeID }

// SealConnectionPayload 将整份连接材料作为一个版本化密文写入。AAD 绑定资源
// 身份、版本、字段域及归属范围，使替换任一关联字段都会导致认证失败。
func (keyring *Keyring) SealConnectionPayload(config *ConnectionConfig, version *ConnectionVersion, payload ConnectionPayload) error {
	if err := validateConfigVersion(config, version); err != nil {
		return err
	}
	plaintext, err := json.Marshal(connectionPayloadWireOf(payload))
	if err != nil {
		return fmt.Errorf("序列化 MCP 连接载荷失败: %w", err)
	}
	ciphertext, nonce, keyID, err := keyring.seal(plaintext, connectionPayloadAAD(config, version.Version, connectionPayloadFieldDomain))
	if err != nil {
		return err
	}
	version.EncryptedPayload = ciphertext
	version.PayloadNonce = nonce
	version.KeyID = keyID
	return nil
}

func (keyring *Keyring) OpenConnectionPayload(config *ConnectionConfig, version *ConnectionVersion) (ConnectionPayload, error) {
	if err := validateConfigVersion(config, version); err != nil {
		return ConnectionPayload{}, err
	}
	plaintext, err := keyring.open(version.KeyID, version.PayloadNonce, version.EncryptedPayload, connectionPayloadAAD(config, version.Version, connectionPayloadFieldDomain))
	if err != nil {
		return ConnectionPayload{}, errors.New("MCP 连接载荷认证解密失败")
	}
	var payload ConnectionPayload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return ConnectionPayload{}, errors.New("MCP 连接载荷格式无效")
	}
	return payload, nil
}

// SealRepositorySource 将仓库来源快照写入任务绑定的专用密文字段。该密文使用
// task-binding AAD，不能被替换为任意连接配置或模型资源中的密文。
func (keyring *Keyring) SealRepositorySource(binding *TaskBinding, context BindingEncryptionContext, snapshot RepositorySourceSnapshot) error {
	if err := validateBindingContext(binding, context); err != nil {
		return err
	}
	plaintext, err := json.Marshal(repositorySourceSnapshotWire(snapshot))
	if err != nil {
		return fmt.Errorf("序列化 MCP 仓库来源失败: %w", err)
	}
	ciphertext, nonce, keyID, err := keyring.seal(plaintext, bindingAAD(binding, context, repositorySourceFieldDomain))
	if err != nil {
		return err
	}
	binding.EncryptedRepositoryURL = ciphertext
	binding.RepositoryURLNonce = nonce
	binding.RepositoryURLKeyID = bindingKeyIDV2Prefix + keyID
	return nil
}

func (keyring *Keyring) OpenRepositorySource(binding *TaskBinding, context BindingEncryptionContext) (RepositorySourceSnapshot, error) {
	if err := validateBindingContext(binding, context); err != nil {
		return RepositorySourceSnapshot{}, err
	}
	keyID, err := bindingV2KeyID(binding.RepositoryURLKeyID)
	if err != nil {
		return RepositorySourceSnapshot{}, err
	}
	plaintext, err := keyring.open(keyID, binding.RepositoryURLNonce, binding.EncryptedRepositoryURL, bindingAAD(binding, context, repositorySourceFieldDomain))
	if err != nil {
		return RepositorySourceSnapshot{}, errors.New("MCP 仓库来源认证解密失败")
	}
	var snapshot RepositorySourceSnapshot
	if err := json.Unmarshal(plaintext, &snapshot); err != nil {
		return RepositorySourceSnapshot{}, errors.New("MCP 仓库来源格式无效")
	}
	return snapshot, nil
}

func (keyring *Keyring) seal(plaintext, aad []byte) ([]byte, []byte, string, error) {
	if keyring == nil {
		return nil, nil, "", errors.New("MCP 连接密钥环不能为空")
	}
	key, ok := keyring.keys[keyring.activeID]
	if !ok {
		return nil, nil, "", errors.New("活动 MCP 连接主密钥不可用")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, nil, "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, "", fmt.Errorf("生成 MCP 连接 nonce 失败: %w", err)
	}
	return aead.Seal(nil, nonce, plaintext, aad), nonce, keyring.activeID, nil
}

func (keyring *Keyring) open(keyID string, nonce, ciphertext, aad []byte) ([]byte, error) {
	if keyring == nil {
		return nil, errors.New("MCP 连接密钥环不能为空")
	}
	key, ok := keyring.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("MCP 连接载荷使用未知主密钥 ID %q", keyID)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("MCP 连接 nonce 无效")
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func validateConfigVersion(config *ConnectionConfig, version *ConnectionVersion) error {
	if config == nil || strings.TrimSpace(config.ID) == "" {
		return errors.New("MCP 连接配置不能为空")
	}
	if version == nil || strings.TrimSpace(version.ConnectionConfigID) == "" || version.ConnectionConfigID != config.ID || version.Version < 1 {
		return errors.New("MCP 连接版本无效")
	}
	if config.Scope != ScopePrivate && config.Scope != ScopeGlobal {
		return errors.New("MCP 连接归属范围无效")
	}
	if config.Scope == ScopePrivate && strings.TrimSpace(config.OwnerUserID) == "" {
		return errors.New("MCP 连接归属范围无效")
	}
	return nil
}

func validateBindingContext(binding *TaskBinding, context BindingEncryptionContext) error {
	if binding == nil || strings.TrimSpace(binding.ID) == "" {
		return errors.New("MCP 任务绑定不能为空")
	}
	if context.Scope != ScopePrivate && context.Scope != ScopeGlobal || context.Version < 1 {
		return errors.New("MCP 任务绑定归属范围无效")
	}
	if context.Scope == ScopePrivate && strings.TrimSpace(context.OwnerUserID) == "" {
		return errors.New("MCP 任务绑定归属范围无效")
	}
	return nil
}

func connectionPayloadAAD(config *ConnectionConfig, version int, fieldDomain string) []byte {
	return aad(config.ID, version, fieldDomain, config.OwnerUserID, config.Scope)
}

func bindingAAD(binding *TaskBinding, context BindingEncryptionContext, fieldDomain string) []byte {
	// 任务 ID 和来源类型同样决定密文的业务归属。把它们放入 AAD 后，即使攻击者
	// 能修改绑定行的外键或来源字段，也不能将仓库快照重放到另一项任务/来源。
	return []byte(strings.Join([]string{
		bindingAADNamespaceV2,
		binding.ID,
		binding.TaskID,
		binding.SourceKind,
		strconv.Itoa(context.Version),
		fieldDomain,
		context.OwnerUserID,
		string(context.Scope),
	}, "\x00"))
}

// bindingV2KeyID 用持久化 key-ID 封套识别新格式。MCP 绑定尚未有已发布写入路径；
// 无封套的旧行必须明确拒绝，且不能根据可篡改的 task_id/source_kind 选择 v1 AAD
// 回退解密。仓储写入也复用该解析，避免持久化必然无法打开的绑定。
func bindingV2KeyID(storedKeyID string) (string, error) {
	if !strings.HasPrefix(storedKeyID, bindingKeyIDV2Prefix) {
		return "", ErrLegacyBindingEncryptionFormat
	}
	keyID := strings.TrimPrefix(storedKeyID, bindingKeyIDV2Prefix)
	if strings.TrimSpace(keyID) == "" {
		return "", errors.New("MCP 仓库来源 binding-v2 密钥标识无效")
	}
	return keyID, nil
}

// 以下 wire 类型不携带面向日志/响应的脱敏方法，只在进出 AES-GCM 前处理完整明文。
// 它们不能映射 GORM 字段，因此不会成为持久化明文字段。
type connectionPayloadWire struct {
	Endpoint       string             `json:"endpoint"`
	Authentication authenticationWire `json:"authentication"`
	Headers        []headerWire       `json:"headers,omitempty"`
}

type authenticationWire struct {
	Kind       AuthenticationKind `json:"kind"`
	HeaderName string             `json:"header_name,omitempty"`
	Secret     string             `json:"secret,omitempty"`
}

type headerWire struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func connectionPayloadWireOf(payload ConnectionPayload) connectionPayloadWire {
	headers := make([]headerWire, len(payload.Headers))
	for index, header := range payload.Headers {
		headers[index] = headerWire{Name: header.Name, Value: header.Value}
	}
	return connectionPayloadWire{
		Endpoint: payload.Endpoint,
		Authentication: authenticationWire{
			Kind: payload.Authentication.Kind, HeaderName: payload.Authentication.HeaderName, Secret: payload.Authentication.Secret,
		},
		Headers: headers,
	}
}

type repositorySourceSnapshotWire RepositorySourceSnapshot

func aad(resourceID string, version int, fieldDomain, ownerUserID string, scope Scope) []byte {
	return []byte(strings.Join([]string{
		keyringAADNamespace,
		resourceID,
		strconv.Itoa(version),
		fieldDomain,
		ownerUserID,
		string(scope),
	}, "\x00"))
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func decodeKey(encoded string) ([]byte, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, errors.New("MCP_CONNECTION_MASTER_KEY 不能为空")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("MCP 连接主密钥必须使用 base64 编码")
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	return key, nil
}

func validateKey(key []byte) error {
	if len(key) != 32 {
		return errors.New("MCP 连接主密钥必须恰好为 32 字节")
	}
	return nil
}
