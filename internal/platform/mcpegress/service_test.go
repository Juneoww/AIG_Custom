package mcpegress

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memoryTaskReader struct{ records map[string]*tasks.Task }

func (reader *memoryTaskReader) Get(_ context.Context, taskID string) (*tasks.Task, error) {
	task := reader.records[taskID]
	if task == nil {
		return nil, tasks.ErrNotFound
	}
	copy := *task
	return &copy, nil
}

type memoryBindingReader struct {
	bindings map[string]*mcpconnections.TaskBinding
	configs  map[string]*mcpconnections.ConnectionConfig
	versions map[string]*mcpconnections.ConnectionVersion
}

func (reader *memoryBindingReader) GetTaskBinding(_ context.Context, taskID string) (*mcpconnections.TaskBinding, error) {
	binding := reader.bindings[taskID]
	if binding == nil {
		return nil, mcpconnections.ErrNotFound
	}
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
	return &copy, nil
}

func (reader *memoryBindingReader) GetConfig(_ context.Context, configID string) (*mcpconnections.ConnectionConfig, error) {
	config := reader.configs[configID]
	if config == nil {
		return nil, mcpconnections.ErrNotFound
	}
	copy := *config
	return &copy, nil
}

func (reader *memoryBindingReader) GetVersion(_ context.Context, configID string, version int) (*mcpconnections.ConnectionVersion, error) {
	stored := reader.versions[connectionVersionKey(configID, version)]
	if stored == nil {
		return nil, mcpconnections.ErrNotFound
	}
	copy := *stored
	copy.EncryptedPayload = append([]byte(nil), stored.EncryptedPayload...)
	copy.PayloadNonce = append([]byte(nil), stored.PayloadNonce...)
	return &copy, nil
}

func connectionVersionKey(configID string, version int) string {
	return configID + "/" + string(rune(version))
}

type recordingRepositoryFetcher struct {
	requests []RepositoryFetchRequest
	archive  string
	err      error
}

func (fetcher *recordingRepositoryFetcher) FetchRepository(_ context.Context, request RepositoryFetchRequest) (string, error) {
	fetcher.requests = append(fetcher.requests, request)
	if fetcher.err != nil {
		return "", fetcher.err
	}
	return fetcher.archive, nil
}

type egressPolicyResolver func(context.Context, string) ([]net.IPAddr, error)

func (resolver egressPolicyResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return resolver(ctx, host)
}

type egressPolicyDialer struct{}

func (egressPolicyDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("test policy must not dial")
}

func TestIssueRuntimeForServiceKeepsTargetOnlyInGateway(t *testing.T) {
	ctx := context.Background()
	const (
		taskID       = "runtime-service-task"
		configID     = "runtime-service-config"
		endpoint     = "https://mcp.allowed.example.test/private-endpoint"
		secret       = "runtime-service-secret"
		headerName   = "X-Private-MCP-Header"
		gatewayURL   = "https://platform.internal.example.test"
		capabilityID = "runtime-capability-id"
	)
	keyring := testEgressKeyring(t)
	config := &mcpconnections.ConnectionConfig{ID: configID, OwnerUserID: "runtime-owner", Scope: mcpconnections.ScopePrivate, Enabled: true}
	version := &mcpconnections.ConnectionVersion{
		ID: "runtime-service-version", ConnectionConfigID: configID, Version: 3, Transport: mcpconnections.TransportHTTP,
		DetectedTransport: mcpconnections.TransportHTTP, ProbeStatus: mcpconnections.ProbeStatusPassed,
	}
	configIDValue := configID
	require.NoError(t, keyring.SealConnectionPayload(config, version, mcpconnections.ConnectionPayload{
		Endpoint:       endpoint,
		Authentication: mcpconnections.Authentication{Kind: mcpconnections.AuthenticationBearer, Secret: secret},
		Headers:        []mcpconnections.Header{{Name: headerName, Value: "runtime-header-secret"}},
	}))
	versionValue := version.Version
	service := NewService(ServiceDependencies{
		Tasks: &memoryTaskReader{records: map[string]*tasks.Task{
			taskID: {ID: taskID, OwnerUserID: config.OwnerUserID, TaskType: "mcp_scan", Status: tasks.StatusPending},
		}},
		Bindings: &memoryBindingReader{
			bindings: map[string]*mcpconnections.TaskBinding{
				taskID: {ID: "runtime-service-binding", TaskID: taskID, SourceKind: "service", ConnectionConfigID: &configIDValue, ConnectionConfigVersion: &versionValue},
			},
			configs:  map[string]*mcpconnections.ConnectionConfig{configID: config},
			versions: map[string]*mcpconnections.ConnectionVersion{connectionVersionKey(configID, version.Version): version},
		},
		Keyring:      keyring,
		Capabilities: NewMemoryCapabilityRepository(),
		Policy:       testEgressPolicy(t),
		GatewayURL:   gatewayURL,
		NewCapability: func() (string, error) {
			return capabilityID, nil
		},
	})

	runtime, err := service.IssueRuntime(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, gatewayURL+"/api/internal/mcp-egress/"+taskID, runtime.MCPProxyURL)
	assert.Equal(t, capabilityID, runtime.TaskCapability)
	assert.Equal(t, "http", runtime.EffectiveTransport)
	assert.Empty(t, runtime.ArchiveRef)
	require.NoError(t, service.VerifyCapability(ctx, taskID, capabilityID))
	assert.Error(t, service.VerifyCapability(ctx, "another-task", capabilityID))

	encoded, marshalErr := json.Marshal(runtime)
	require.NoError(t, marshalErr)
	for _, sentinel := range []string{endpoint, secret, headerName, "runtime-header-secret"} {
		assert.NotContains(t, string(encoded), sentinel)
	}
}

func TestIssueRuntimeRotatesThePriorCapability(t *testing.T) {
	ctx := context.Background()
	service, taskID := newServiceRuntimeFixture(t)
	issued := []string{"first-runtime-capability", "second-runtime-capability"}
	service.newCapability = func() (string, error) {
		value := issued[0]
		issued = issued[1:]
		return value, nil
	}

	first, err := service.IssueRuntime(ctx, taskID)
	require.NoError(t, err)
	second, err := service.IssueRuntime(ctx, taskID)
	require.NoError(t, err)
	assert.NotEqual(t, first.TaskCapability, second.TaskCapability)
	assert.Error(t, service.VerifyCapability(ctx, taskID, first.TaskCapability))
	require.NoError(t, service.VerifyCapability(ctx, taskID, second.TaskCapability))
}

func TestIssueRuntimeRefusesTerminalTask(t *testing.T) {
	ctx := context.Background()
	service, taskID := newServiceRuntimeFixture(t)
	service.tasks.(*memoryTaskReader).records[taskID].Status = tasks.StatusSucceeded

	_, err := service.IssueRuntime(ctx, taskID)
	assert.ErrorIs(t, err, ErrRuntimeUnavailable)
	_, latestErr := service.capabilities.Latest(ctx, taskID)
	assert.ErrorIs(t, latestErr, ErrCapabilityNotFound)
}

func TestIssueRuntimeForRepositoryReturnsOnlyOpaqueArchiveReference(t *testing.T) {
	ctx := context.Background()
	const (
		taskID = "runtime-repository-task"
		url    = "https://git.allowed.example.test/team/private-repository.git"
	)
	keyring := testEgressKeyring(t)
	binding := &mcpconnections.TaskBinding{ID: "runtime-repository-binding", TaskID: taskID, SourceKind: "repository"}
	require.NoError(t, keyring.SealRepositorySource(binding, mcpconnections.BindingEncryptionContext{
		OwnerUserID: "repository-owner", Scope: mcpconnections.ScopePrivate, Version: 1,
	}, mcpconnections.RepositorySourceSnapshot{RepositoryURL: url}))
	fetcher := &recordingRepositoryFetcher{archive: "archive:opaque-runtime-reference"}
	service := NewService(ServiceDependencies{
		Tasks: &memoryTaskReader{records: map[string]*tasks.Task{
			taskID: {ID: taskID, OwnerUserID: "repository-owner", TaskType: "mcp_scan", Status: tasks.StatusPending},
		}},
		Bindings: &memoryBindingReader{bindings: map[string]*mcpconnections.TaskBinding{taskID: binding}},
		Keyring:  keyring, Capabilities: NewMemoryCapabilityRepository(), Policy: testEgressPolicy(t), Fetcher: fetcher,
	})

	runtime, err := service.IssueRuntime(ctx, taskID)
	require.NoError(t, err)
	assert.Equal(t, fetcher.archive, runtime.ArchiveRef)
	assert.Empty(t, runtime.MCPProxyURL)
	assert.Empty(t, runtime.TaskCapability)
	require.Len(t, fetcher.requests, 1)
	assert.Equal(t, taskID, fetcher.requests[0].TaskID)
	assert.Equal(t, url, fetcher.requests[0].RepositoryURL)
	encoded, marshalErr := json.Marshal(runtime)
	require.NoError(t, marshalErr)
	assert.NotContains(t, string(encoded), url)
}

func TestRuntimeAndRepositoryFetchRequestRedactFormattedOutput(t *testing.T) {
	runtime := Runtime{
		MCPProxyURL:    "https://platform.internal.example.test/api/internal/mcp-egress/task-sentinel",
		TaskCapability: "runtime-capability-secret", EffectiveTransport: "http", ArchiveRef: "archive:opaque-sentinel",
	}
	request := RepositoryFetchRequest{
		TaskID: "task-sentinel", OwnerUserID: "owner-sentinel", RepositoryURL: "https://git.allowed.example.test/private.git",
	}
	for _, sentinel := range []string{"runtime-capability-secret", "private.git", "owner-sentinel"} {
		assert.NotContains(t, fmt.Sprintf("%+v", runtime), sentinel)
		assert.NotContains(t, fmt.Sprintf("%+v", request), sentinel)
	}
}

func newServiceRuntimeFixture(t *testing.T) (*Service, string) {
	t.Helper()
	const taskID = "runtime-rotation-task"
	keyring := testEgressKeyring(t)
	configID := "runtime-rotation-config"
	config := &mcpconnections.ConnectionConfig{ID: configID, OwnerUserID: "rotation-owner", Scope: mcpconnections.ScopePrivate, Enabled: true}
	version := &mcpconnections.ConnectionVersion{
		ID: "runtime-rotation-version", ConnectionConfigID: configID, Version: 1, Transport: mcpconnections.TransportHTTP,
		DetectedTransport: mcpconnections.TransportHTTP, ProbeStatus: mcpconnections.ProbeStatusPassed,
	}
	require.NoError(t, keyring.SealConnectionPayload(config, version, mcpconnections.ConnectionPayload{
		Endpoint: "https://mcp.allowed.example.test/runtime-rotation", Authentication: mcpconnections.Authentication{Kind: mcpconnections.AuthenticationNone},
	}))
	versionValue := version.Version
	return NewService(ServiceDependencies{
		Tasks: &memoryTaskReader{records: map[string]*tasks.Task{
			taskID: {ID: taskID, OwnerUserID: config.OwnerUserID, TaskType: "mcp_scan", Status: tasks.StatusPending},
		}},
		Bindings: &memoryBindingReader{
			bindings: map[string]*mcpconnections.TaskBinding{
				taskID: {ID: "runtime-rotation-binding", TaskID: taskID, SourceKind: "service", ConnectionConfigID: &configID, ConnectionConfigVersion: &versionValue},
			},
			configs:  map[string]*mcpconnections.ConnectionConfig{configID: config},
			versions: map[string]*mcpconnections.ConnectionVersion{connectionVersionKey(configID, version.Version): version},
		},
		Keyring: keyring, Capabilities: NewMemoryCapabilityRepository(), Policy: testEgressPolicy(t), GatewayURL: "https://platform.internal.example.test",
	}), taskID
}

func testEgressKeyring(t *testing.T) *mcpconnections.Keyring {
	t.Helper()
	keyring, err := mcpconnections.NewKeyring("egress-test-key", []byte("01234567890123456789012345678901"), nil)
	require.NoError(t, err)
	return keyring
}

func testEgressPolicy(t *testing.T) *mcpconnections.OutboundPolicy {
	t.Helper()
	policy, err := mcpconnections.NewOutboundPolicy(mcpconnections.OutboundPolicyConfig{
		AllowedCIDRs: []string{"203.0.113.0/24"}, GitAllowedCIDRs: []string{"198.51.100.0/24"},
		GitAllowedHosts: []string{"git.allowed.example.test"}, ControlledDialerAvailable: true, Dialer: egressPolicyDialer{},
		Resolver: egressPolicyResolver(func(_ context.Context, host string) ([]net.IPAddr, error) {
			switch host {
			case "mcp.allowed.example.test":
				return []net.IPAddr{{IP: net.ParseIP("203.0.113.44")}}, nil
			case "git.allowed.example.test":
				return []net.IPAddr{{IP: net.ParseIP("198.51.100.44")}}, nil
			default:
				return nil, errors.New("unexpected test host")
			}
		}),
	})
	require.NoError(t, err)
	return policy
}
