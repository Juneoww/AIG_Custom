package models

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
	"strings"
)

const (
	EnvMasterKey          = "MODEL_MASTER_KEY"
	EnvMasterKeyID        = "MODEL_MASTER_KEY_ID"
	EnvPreviousMasterKeys = "MODEL_PREVIOUS_MASTER_KEYS"
)

type Keyring struct {
	activeID string
	keys     map[string][]byte
}

func NewKeyring(activeID string, activeKey []byte, previous map[string][]byte) (*Keyring, error) {
	activeID = strings.TrimSpace(activeID)
	if activeID == "" {
		return nil, errors.New("MODEL_MASTER_KEY_ID 不能为空")
	}
	if err := validateKey(activeKey); err != nil {
		return nil, fmt.Errorf("活动模型主密钥无效: %w", err)
	}
	keys := make(map[string][]byte, len(previous)+1)
	for id, key := range previous {
		id = strings.TrimSpace(id)
		if id == "" || id == activeID {
			return nil, errors.New("历史模型主密钥 ID 无效或与活动 ID 重复")
		}
		if err := validateKey(key); err != nil {
			return nil, fmt.Errorf("历史模型主密钥 %q 无效: %w", id, err)
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
		return nil, fmt.Errorf("读取活动模型主密钥失败: %w", err)
	}
	previousEncoded := map[string]string{}
	if raw := strings.TrimSpace(os.Getenv(EnvPreviousMasterKeys)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &previousEncoded); err != nil {
			return nil, fmt.Errorf("MODEL_PREVIOUS_MASTER_KEYS 必须是 key-id 到 base64 密钥的 JSON 对象: %w", err)
		}
	}
	previous := make(map[string][]byte, len(previousEncoded))
	for id, encoded := range previousEncoded {
		key, err := decodeKey(encoded)
		if err != nil {
			return nil, fmt.Errorf("读取历史模型主密钥 %q 失败: %w", id, err)
		}
		previous[id] = key
	}
	return NewKeyring(activeID, active, previous)
}

func (keyring *Keyring) ActiveKeyID() string { return keyring.activeID }

func (keyring *Keyring) SealToken(model *Model, plaintext string) error {
	if model == nil {
		return errors.New("模型不能为空")
	}
	if plaintext == "" {
		return errors.New("模型 token 不能为空")
	}
	key, ok := keyring.keys[keyring.activeID]
	if !ok {
		return errors.New("活动模型主密钥不可用")
	}
	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("生成模型 token nonce 失败: %w", err)
	}
	model.EncryptedToken = aead.Seal(nil, nonce, []byte(plaintext), tokenAAD(model))
	model.TokenNonce = nonce
	model.KeyID = keyring.activeID
	return nil
}

func (keyring *Keyring) OpenToken(model *Model) (string, error) {
	if model == nil {
		return "", errors.New("模型不能为空")
	}
	key, ok := keyring.keys[model.KeyID]
	if !ok {
		return "", fmt.Errorf("模型 token 使用未知主密钥 ID %q", model.KeyID)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return "", err
	}
	if len(model.TokenNonce) != aead.NonceSize() {
		return "", errors.New("模型 token nonce 无效")
	}
	plaintext, err := aead.Open(nil, model.TokenNonce, model.EncryptedToken, tokenAAD(model))
	if err != nil {
		return "", errors.New("模型 token 认证解密失败")
	}
	return string(plaintext), nil
}

func tokenAAD(model *Model) []byte {
	return []byte(model.ID + "\x00" + string(model.Scope) + "\x00" + model.OwnerUserID)
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
		return nil, errors.New("MODEL_MASTER_KEY 不能为空")
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("模型主密钥必须使用 base64 编码")
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}
	return key, nil
}

func validateKey(key []byte) error {
	if len(key) != 32 {
		return errors.New("模型主密钥必须恰好为 32 字节")
	}
	return nil
}
