package mcpconnections

import (
	"bytes"
	"encoding/json"
	"reflect"
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

func TestSealPayloadJSONMasksSensitiveFields(t *testing.T) {
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
	if !bytes.Contains(encoded, []byte("X-Sentinel-Key")) || !bytes.Contains(encoded, []byte("X-Sentinel-Custom")) {
		t.Fatalf("connection payload JSON must retain safe header-name metadata: %s", encoded)
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
	tamperedContext := context
	tamperedContext.Version++
	if _, err := keyring.OpenRepositorySource(binding, tamperedContext); err == nil {
		t.Fatal("repository source opened after version tampering")
	}
	if _, err := keyring.open(binding.RepositoryURLKeyID, binding.RepositoryURLNonce, binding.EncryptedRepositoryURL, bindingAAD(binding, context, "wrong-field-domain")); err == nil {
		t.Fatal("repository source opened with a different field AAD domain")
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
