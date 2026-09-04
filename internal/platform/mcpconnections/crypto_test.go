package mcpconnections

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSealAndOpenConnectionPayloadKeepsAuthenticationMaterialEncrypted(t *testing.T) {
	key := bytes.Repeat([]byte{0x41}, 32)
	keyring, err := NewKeyring("active-sentinel", key, nil)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}

	config := &ConnectionConfig{
		ID:          "config-sentinel",
		OwnerUserID: "owner-sentinel",
		Scope:       ScopePrivate,
	}
	cases := []struct {
		name    string
		payload ConnectionPayload
		secret  string
	}{
		{
			name: "bearer",
			payload: ConnectionPayload{
				Endpoint:       "endpoint-bearer-sentinel",
				Authentication: Authentication{Kind: AuthenticationBearer, Secret: "bearer-secret-sentinel"},
			},
			secret: "bearer-secret-sentinel",
		},
		{
			name: "api key header",
			payload: ConnectionPayload{
				Endpoint:       "endpoint-api-key-sentinel",
				Authentication: Authentication{Kind: AuthenticationAPIKeyHeader, HeaderName: "X-Sentinel-Key", Secret: "api-key-secret-sentinel"},
			},
			secret: "api-key-secret-sentinel",
		},
		{
			name: "custom headers",
			payload: ConnectionPayload{
				Endpoint:       "endpoint-custom-sentinel",
				Authentication: Authentication{Kind: AuthenticationCustomHeaders},
				Headers:        []Header{{Name: "X-Sentinel-First", Value: "first-secret-sentinel"}, {Name: "X-Sentinel-Second", Value: "second-secret-sentinel"}},
			},
			secret: "first-secret-sentinel",
		},
		{
			name: "none",
			payload: ConnectionPayload{
				Endpoint:       "endpoint-none-sentinel",
				Authentication: Authentication{Kind: AuthenticationNone},
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			version := &ConnectionVersion{ID: "version-" + test.name, ConnectionConfigID: config.ID, Version: 1, Transport: TransportHTTP}
			if err := keyring.SealConnectionPayload(config, version, test.payload); err != nil {
				t.Fatalf("seal connection payload: %v", err)
			}

			encoded, err := json.Marshal(version)
			if err != nil {
				t.Fatalf("marshal sealed version: %v", err)
			}
			if bytes.Contains(encoded, []byte(test.payload.Endpoint)) {
				t.Fatalf("endpoint leaked into a stored plaintext field: %s", encoded)
			}
			if test.secret != "" && bytes.Contains(encoded, []byte(test.secret)) {
				t.Fatalf("authentication material leaked into a stored plaintext field: %s", encoded)
			}

			opened, err := keyring.OpenConnectionPayload(config, version)
			if err != nil {
				t.Fatalf("open connection payload: %v", err)
			}
			if !reflect.DeepEqual(test.payload, opened) {
				t.Fatalf("opened payload mismatch: got %#v want %#v", opened, test.payload)
			}
		})
	}
}

func TestOpenConnectionPayloadAcceptsPreviousKeyAndRejectsTampering(t *testing.T) {
	oldKey := bytes.Repeat([]byte{0x31}, 32)
	oldKeyring, err := NewKeyring("old-sentinel", oldKey, nil)
	if err != nil {
		t.Fatalf("new old keyring: %v", err)
	}
	config := &ConnectionConfig{ID: "config-tamper-sentinel", OwnerUserID: "owner-tamper-sentinel", Scope: ScopePrivate}
	stored := &ConnectionVersion{ID: "version-tamper-sentinel", ConnectionConfigID: config.ID, Version: 7, Transport: TransportHTTP}
	if err := oldKeyring.SealConnectionPayload(config, stored, ConnectionPayload{
		Endpoint: "endpoint-tamper-sentinel", Authentication: Authentication{Kind: AuthenticationBearer, Secret: "secret-tamper-sentinel"},
	}); err != nil {
		t.Fatalf("seal old version: %v", err)
	}

	newKeyring, err := NewKeyring("new-sentinel", bytes.Repeat([]byte{0x32}, 32), map[string][]byte{"old-sentinel": oldKey})
	if err != nil {
		t.Fatalf("new rotated keyring: %v", err)
	}
	if _, err := newKeyring.OpenConnectionPayload(config, stored); err != nil {
		t.Fatalf("open with previous key: %v", err)
	}

	cases := []struct {
		name    string
		config  *ConnectionConfig
		version *ConnectionVersion
	}{
		{
			name:    "config id",
			config:  &ConnectionConfig{ID: "other-config-sentinel", OwnerUserID: config.OwnerUserID, Scope: config.Scope},
			version: cloneConnectionVersion(stored),
		},
		{
			name:   "version",
			config: config,
			version: func() *ConnectionVersion {
				version := cloneConnectionVersion(stored)
				version.Version++
				return version
			}(),
		},
		{
			name:   "nonce",
			config: config,
			version: func() *ConnectionVersion {
				version := cloneConnectionVersion(stored)
				version.PayloadNonce[0] ^= 0xff
				return version
			}(),
		},
		{
			name:   "ciphertext",
			config: config,
			version: func() *ConnectionVersion {
				version := cloneConnectionVersion(stored)
				version.EncryptedPayload[0] ^= 0xff
				return version
			}(),
		},
		{
			name:   "key id",
			config: config,
			version: func() *ConnectionVersion {
				version := cloneConnectionVersion(stored)
				version.KeyID = "new-sentinel"
				return version
			}(),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := newKeyring.OpenConnectionPayload(test.config, test.version); err == nil {
				t.Fatal("tampered encrypted connection payload opened successfully")
			}
		})
	}

	if _, err := newKeyring.open(stored.KeyID, stored.PayloadNonce, stored.EncryptedPayload, connectionPayloadAAD(config, stored.Version, "wrong-field-domain")); err == nil {
		t.Fatal("payload opened with a different field AAD domain")
	}
}

func TestSealAndOpenConnectionPayloadAllowsGlobalScopeWithoutOwner(t *testing.T) {
	keyring, err := NewKeyring("global-sentinel", bytes.Repeat([]byte{0x61}, 32), nil)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	config := &ConnectionConfig{ID: "global-config-sentinel", Scope: ScopeGlobal}
	version := &ConnectionVersion{ID: "global-version-sentinel", ConnectionConfigID: config.ID, Version: 1, Transport: TransportHTTP}
	payload := ConnectionPayload{Endpoint: "global-endpoint-sentinel", Authentication: Authentication{Kind: AuthenticationNone}}
	if err := keyring.SealConnectionPayload(config, version, payload); err != nil {
		t.Fatalf("seal global connection payload: %v", err)
	}
	opened, err := keyring.OpenConnectionPayload(config, version)
	if err != nil {
		t.Fatalf("open global connection payload: %v", err)
	}
	if !reflect.DeepEqual(payload, opened) {
		t.Fatalf("opened global payload mismatch: got %#v want %#v", opened, payload)
	}
}

func TestSealPayloadJSONMasksAllConnectionMaterial(t *testing.T) {
	payload := ConnectionPayload{
		Endpoint:       "endpoint-json-sentinel",
		Authentication: Authentication{Kind: AuthenticationAPIKeyHeader, HeaderName: "X-Sentinel-Key", Secret: "api-key-json-sentinel"},
		Headers:        []Header{{Name: "X-Sentinel-Custom", Value: "custom-header-json-sentinel"}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal connection payload: %v", err)
	}
	for _, secret := range []string{"endpoint-json-sentinel", "api-key-json-sentinel", "custom-header-json-sentinel"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("connection payload JSON leaked %q: %s", secret, encoded)
		}
	}
	for _, material := range []string{"X-Sentinel-Key", "X-Sentinel-Custom"} {
		if bytes.Contains(encoded, []byte(material)) {
			t.Fatalf("connection payload JSON leaked header material %q: %s", material, encoded)
		}
	}

	snapshot := RepositorySourceSnapshot{RepositoryURL: "repository-json-sentinel"}
	encoded, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal repository source: %v", err)
	}
	if bytes.Contains(encoded, []byte(snapshot.RepositoryURL)) {
		t.Fatalf("repository source JSON leaked plaintext: %s", encoded)
	}
}

func TestSealLeafJSONMasksConnectionMaterialWhileRoundTripKeepsPlaintext(t *testing.T) {
	header := Header{Name: "X-Leaf-Sentinel", Value: "header-value-leaf-sentinel"}
	authentication := Authentication{Kind: AuthenticationAPIKeyHeader, HeaderName: header.Name, Secret: "authentication-leaf-sentinel"}
	for _, value := range []any{header, authentication} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal leaf value: %v", err)
		}
		for _, material := range []string{"X-Leaf-Sentinel", "header-value-leaf-sentinel", "authentication-leaf-sentinel"} {
			if bytes.Contains(encoded, []byte(material)) {
				t.Fatalf("leaf JSON leaked %q: %s", material, encoded)
			}
		}
	}

	keyring, err := NewKeyring("leaf-sentinel", bytes.Repeat([]byte{0x71}, 32), nil)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	config := &ConnectionConfig{ID: "leaf-config-sentinel", OwnerUserID: "leaf-owner-sentinel", Scope: ScopePrivate}
	version := &ConnectionVersion{ID: "leaf-version-sentinel", ConnectionConfigID: config.ID, Version: 1, Transport: TransportHTTP}
	payload := ConnectionPayload{Endpoint: "leaf-endpoint-sentinel", Authentication: authentication, Headers: []Header{header}}
	if err := keyring.SealConnectionPayload(config, version, payload); err != nil {
		t.Fatalf("seal payload: %v", err)
	}
	opened, err := keyring.OpenConnectionPayload(config, version)
	if err != nil {
		t.Fatalf("open payload: %v", err)
	}
	if !reflect.DeepEqual(payload, opened) {
		t.Fatalf("leaf payload round-trip mismatch: got %#v want %#v", opened, payload)
	}
}

func TestCreateConnectionInputJSONIsSafeButStillDecodesCredentials(t *testing.T) {
	const endpoint = "https://mcp-input-sentinel.example.test/mcp"
	const headerName = "X-Input-Sentinel"
	const secret = "input-secret-sentinel"
	raw := `{"name":"安全连接","description":"","scope":"private","transport":"http","server_url":"` + endpoint + `","authentication":{"kind":"api_key_header","header_name":"` + headerName + `","secret":"` + secret + `"},"headers":[{"name":"X-Custom-Sentinel","value":"custom-secret-sentinel"}]}`
	var input CreateConnectionInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatalf("decode protected write input: %v", err)
	}
	if input.ServerURL != endpoint || input.Authentication.HeaderName != headerName || input.Authentication.Secret != secret {
		t.Fatalf("write input lost credential material after decode: %#v", input)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal write input: %v", err)
	}
	for _, material := range []string{endpoint, headerName, secret, "X-Custom-Sentinel", "custom-secret-sentinel"} {
		if bytes.Contains(encoded, []byte(material)) {
			t.Fatalf("write input JSON leaked %q: %s", material, encoded)
		}
	}
}

func TestSealAndOpenRepositorySourceKeepsSnapshotEncryptedAndBoundToBinding(t *testing.T) {
	keyring, err := NewKeyring("repository-sentinel", bytes.Repeat([]byte{0x51}, 32), nil)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	binding := &TaskBinding{ID: "binding-sentinel", TaskID: "task-sentinel", SourceKind: "repository"}
	context := BindingEncryptionContext{OwnerUserID: "owner-sentinel", Scope: ScopePrivate, Version: 1}
	snapshot := RepositorySourceSnapshot{RepositoryURL: "repository-source-sentinel"}
	if err := keyring.SealRepositorySource(binding, context, snapshot); err != nil {
		t.Fatalf("seal repository source: %v", err)
	}
	const bindingV2KeyIDPrefix = "mcp_connection_binding_v2:"
	if !strings.HasPrefix(binding.RepositoryURLKeyID, bindingV2KeyIDPrefix) {
		t.Fatalf("new repository source must record the binding-v2 encryption format in key ID %q", binding.RepositoryURLKeyID)
	}
	if !strings.HasPrefix(string(bindingAAD(binding, context, repositorySourceFieldDomain)), "mcp_connection_binding_v2\x00") {
		t.Fatal("new repository source must use a dedicated binding-v2 AAD namespace")
	}

	encoded, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal binding: %v", err)
	}
	if bytes.Contains(encoded, []byte(snapshot.RepositoryURL)) {
		t.Fatalf("repository source leaked into a stored plaintext field: %s", encoded)
	}
	opened, err := keyring.OpenRepositorySource(binding, context)
	if err != nil {
		t.Fatalf("open repository source: %v", err)
	}
	if !reflect.DeepEqual(snapshot, opened) {
		t.Fatalf("opened repository source mismatch: got %#v want %#v", opened, snapshot)
	}

	tamperedBinding := cloneTaskBinding(binding)
	tamperedBinding.ID = "other-binding-sentinel"
	if _, err := keyring.OpenRepositorySource(tamperedBinding, context); err == nil {
		t.Fatal("repository source opened after binding ID tampering")
	}
	tamperedTaskBinding := cloneTaskBinding(binding)
	tamperedTaskBinding.TaskID = "other-task-sentinel"
	if _, err := keyring.OpenRepositorySource(tamperedTaskBinding, context); err == nil {
		t.Fatal("repository source opened after task ID tampering")
	}
	tamperedSourceBinding := cloneTaskBinding(binding)
	tamperedSourceBinding.SourceKind = "service"
	if _, err := keyring.OpenRepositorySource(tamperedSourceBinding, context); err == nil {
		t.Fatal("repository source opened after source kind tampering")
	}
	tamperedContext := context
	tamperedContext.Version++
	if _, err := keyring.OpenRepositorySource(binding, tamperedContext); err == nil {
		t.Fatal("repository source opened after version tampering")
	}
	keyID, err := bindingV2KeyID(binding.RepositoryURLKeyID)
	if err != nil {
		t.Fatalf("parse binding-v2 key ID: %v", err)
	}
	if _, err := keyring.open(keyID, binding.RepositoryURLNonce, binding.EncryptedRepositoryURL, bindingAAD(binding, context, "wrong-field-domain")); err == nil {
		t.Fatal("repository source opened with a different field AAD domain")
	}
}

func TestOpenRepositorySourceRejectsLegacyBindingEncryptionFormat(t *testing.T) {
	keyring, err := NewKeyring("legacy-repository-sentinel", bytes.Repeat([]byte{0x52}, 32), nil)
	if err != nil {
		t.Fatalf("new keyring: %v", err)
	}
	binding := &TaskBinding{ID: "legacy-binding-sentinel", TaskID: "legacy-task-sentinel", SourceKind: "repository"}
	context := BindingEncryptionContext{OwnerUserID: "legacy-owner-sentinel", Scope: ScopePrivate, Version: 1}
	snapshot := RepositorySourceSnapshot{RepositoryURL: "legacy-repository-source-sentinel"}
	plaintext, err := json.Marshal(repositorySourceSnapshotWire(snapshot))
	if err != nil {
		t.Fatalf("marshal legacy snapshot: %v", err)
	}
	// 此处刻意构造上一个提交写出的 v1 AAD 密文；即使其认证可通过旧格式，
	// OpenRepositorySource 也绝不能尝试旧 AAD，否则篡改 task_id 会重新打开密文。
	ciphertext, nonce, keyID, err := keyring.seal(plaintext, aad(binding.ID, context.Version, repositorySourceFieldDomain, context.OwnerUserID, context.Scope))
	if err != nil {
		t.Fatalf("seal legacy snapshot: %v", err)
	}
	binding.EncryptedRepositoryURL = ciphertext
	binding.RepositoryURLNonce = nonce
	binding.RepositoryURLKeyID = keyID

	for _, candidate := range []*TaskBinding{
		binding,
		func() *TaskBinding {
			tampered := cloneTaskBinding(binding)
			tampered.TaskID = "legacy-other-task-sentinel"
			return tampered
		}(),
	} {
		if _, err := keyring.OpenRepositorySource(candidate, context); err == nil || !strings.Contains(err.Error(), "重新创建任务绑定") {
			t.Fatalf("legacy binding encryption must fail closed with a recreate error, got %v", err)
		}
	}
}

func cloneConnectionVersion(version *ConnectionVersion) *ConnectionVersion {
	copy := *version
	copy.EncryptedPayload = append([]byte(nil), version.EncryptedPayload...)
	copy.PayloadNonce = append([]byte(nil), version.PayloadNonce...)
	return &copy
}

func cloneTaskBinding(binding *TaskBinding) *TaskBinding {
	copy := *binding
	copy.EncryptedRepositoryURL = append([]byte(nil), binding.EncryptedRepositoryURL...)
	copy.RepositoryURLNonce = append([]byte(nil), binding.RepositoryURLNonce...)
	return &copy
}
