package mcpconnections

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memoryConnectionRepository 保留真实仓储所需的状态语义，而将 PostgreSQL 留给
// 已有的 repository 集成测试；服务测试只替换持久化边界，不替换授权或安全投影。
type memoryConnectionRepository struct {
	configs  map[string]*ConnectionConfig
	versions map[string]map[int]*ConnectionVersion
	recorded int
}

func newMemoryConnectionRepository() *memoryConnectionRepository {
	return &memoryConnectionRepository{
		configs:  map[string]*ConnectionConfig{},
		versions: map[string]map[int]*ConnectionVersion{},
	}
}

func (repository *memoryConnectionRepository) Create(_ context.Context, config *ConnectionConfig, version *ConnectionVersion) error {
	if config == nil || version == nil {
		return ErrInvalid
	}
	if _, ok := repository.configs[config.ID]; ok {
		return ErrInvalid
	}
	storedConfig := cloneConnectionConfig(config)
	storedVersion := cloneConnectionVersionRecord(version)
	storedConfig.CurrentVersion = 1
	storedConfig.Enabled = false
	storedVersion.Version = 1
	storedVersion.ProbeStatus = ProbeStatusNotTested
	storedVersion.DetectedTransport = ""
	repository.configs[config.ID] = storedConfig
	repository.versions[config.ID] = map[int]*ConnectionVersion{1: storedVersion}
	return nil
}

func (repository *memoryConnectionRepository) GetConfig(_ context.Context, id string) (*ConnectionConfig, error) {
	config, ok := repository.configs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneConnectionConfig(config), nil
}

func (repository *memoryConnectionRepository) GetVersion(_ context.Context, configID string, version int) (*ConnectionVersion, error) {
	versions, ok := repository.versions[configID]
	if !ok {
		return nil, ErrNotFound
	}
	stored, ok := versions[version]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneConnectionVersionRecord(stored), nil
}

func (repository *memoryConnectionRepository) ListConfigs(_ context.Context) ([]ConnectionConfig, error) {
	configs := make([]ConnectionConfig, 0, len(repository.configs))
	for _, config := range repository.configs {
		configs = append(configs, *cloneConnectionConfig(config))
	}
	sort.Slice(configs, func(left, right int) bool { return configs[left].ID < configs[right].ID })
	return configs, nil
}

func (repository *memoryConnectionRepository) RecordProbeResult(_ context.Context, configID string, version int, transport Transport, status ProbeStatus) error {
	stored, err := repository.GetVersion(context.Background(), configID, version)
	if err != nil {
		return err
	}
	stored.DetectedTransport = transport
	stored.ProbeStatus = status
	repository.versions[configID][version] = stored
	repository.recorded++
	return nil
}

func (repository *memoryConnectionRepository) SetEnabled(_ context.Context, configID string, enabled bool) (*ConnectionConfig, error) {
	stored, ok := repository.configs[configID]
	if !ok {
		return nil, ErrNotFound
	}
	stored.Enabled = enabled
	return cloneConnectionConfig(stored), nil
}

type scriptedProbePort struct {
	attempts []Transport
	errors   map[Transport]error
}

func (port *scriptedProbePort) Initialize(_ context.Context, request ProbeRequest) error {
	port.attempts = append(port.attempts, request.Transport)
	return port.errors[request.Transport]
}

func testKeyring(t *testing.T) *Keyring {
	t.Helper()
	keyring, err := NewKeyring("test-key", []byte("01234567890123456789012345678901"), nil)
	require.NoError(t, err)
	return keyring
}

func testPolicy(t *testing.T, controlled bool) *OutboundPolicy {
	t.Helper()
	policy, err := NewOutboundPolicy(OutboundPolicyConfig{
		AllowedCIDRs: []string{"203.0.113.0/24"},
		Resolver: policyResolver(func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.44")}}, nil
		}),
		Dialer:                    &policyDialer{},
		ControlledDialerAvailable: controlled,
	})
	require.NoError(t, err)
	return policy
}

func testService(t *testing.T, repository *memoryConnectionRepository, controlled bool, port *scriptedProbePort) *Service {
	t.Helper()
	return NewService(repository, testKeyring(t), NewProbeEngine(port, ProbeOptions{Timeout: time.Second, MinimumInterval: time.Nanosecond}), testPolicy(t, controlled))
}

func serviceInput(name string, scope Scope, transport Transport) CreateConnectionInput {
	return CreateConnectionInput{
		Name:        name,
		Description: "safe description",
		Scope:       scope,
		Transport:   transport,
		ServerURL:   "https://safe.example.test/mcp",
		Authentication: Authentication{
			Kind:       AuthenticationAPIKeyHeader,
			HeaderName: "X-Private-Header-Name",
			Secret:     "private-token-value",
		},
		Headers: []Header{{Name: "X-Custom-Secret-Header", Value: "custom-secret-value"}},
	}
}

func TestServiceEnforcesVisibilityAndSafeConnectionProjections(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryConnectionRepository()
	service := testService(t, repository, true, &scriptedProbePort{})
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	bob := identity.Subject{UserID: "bob", Role: identity.RoleUser}
	auditor := identity.Subject{UserID: "auditor", Role: identity.RoleAuditor}
	admin := identity.Subject{UserID: "admin", Role: identity.RoleAdmin}

	private, err := service.Create(ctx, alice, serviceInput("alice private", ScopePrivate, TransportHTTP))
	require.NoError(t, err)
	global, err := service.Create(ctx, admin, serviceInput("global", ScopeGlobal, TransportHTTP))
	require.NoError(t, err)

	for _, expectation := range []struct {
		name    string
		subject identity.Subject
		count   int
	}{
		{name: "owner sees private and global summaries", subject: alice, count: 2},
		{name: "other user sees global summary only", subject: bob, count: 1},
		{name: "auditor sees global read-only summary", subject: auditor, count: 1},
		{name: "administrator sees all summaries", subject: admin, count: 2},
	} {
		t.Run(expectation.name, func(t *testing.T) {
			items, err := service.List(ctx, expectation.subject)
			require.NoError(t, err)
			assert.Len(t, items, expectation.count)
		})
	}

	_, err = service.GetSummary(ctx, bob, private.ID)
	require.ErrorIs(t, err, ErrNotFound, "private resources must be invisible rather than merely forbidden")
	_, err = service.GetSummary(ctx, auditor, private.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = service.GetManagementDetail(ctx, auditor, global.ID)
	require.ErrorIs(t, err, ErrForbidden, "visible global summary does not grant management detail")
	_, err = service.GetManagementDetail(ctx, bob, global.ID)
	require.ErrorIs(t, err, ErrForbidden)

	detail, err := service.GetManagementDetail(ctx, alice, private.ID)
	require.NoError(t, err)
	assert.True(t, detail.EndpointConfigured)
	assert.True(t, detail.AuthenticationConfigured)
	assert.True(t, detail.CustomHeadersConfigured)
	_, err = service.GetManagementDetail(ctx, admin, private.ID)
	require.NoError(t, err)
	_, err = service.Create(ctx, alice, serviceInput("not allowed global", ScopeGlobal, TransportHTTP))
	require.ErrorIs(t, err, ErrForbidden)

	summary, err := service.GetSummary(ctx, alice, private.ID)
	require.NoError(t, err)
	options, err := service.TaskOptions(ctx, alice)
	require.NoError(t, err)
	for _, projection := range []any{summary, detail, options} {
		encoded, err := json.Marshal(projection)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "safe.example.test")
		assert.NotContains(t, string(encoded), "X-Private-Header-Name")
		assert.NotContains(t, string(encoded), "X-Custom-Secret-Header")
		assert.NotContains(t, string(encoded), "private-token-value")
		assert.NotContains(t, string(encoded), "custom-secret-value")
	}
}

func TestServiceFailsClosedWithoutControlledGateway(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryConnectionRepository()
	port := &scriptedProbePort{errors: map[Transport]error{}}
	service := testService(t, repository, false, port)
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	created, err := service.Create(ctx, alice, serviceInput("private", ScopePrivate, TransportHTTP))
	require.NoError(t, err)
	assert.False(t, created.Enabled)
	assert.Equal(t, ProbeStatusNotTested, created.ProbeStatus)

	_, err = service.Probe(ctx, alice, created.ID)
	require.ErrorIs(t, err, ErrControlledEgressRequired)
	assert.Empty(t, port.attempts)
	_, err = service.SetEnabled(ctx, alice, created.ID, true)
	require.ErrorIs(t, err, ErrControlledEgressRequired)
	options, err := service.TaskOptions(ctx, alice)
	require.NoError(t, err)
	assert.Empty(t, options)
	require.ErrorIs(t, service.ValidateTaskConnection(ctx, alice, created.ID), ErrControlledEgressRequired)
}

func TestServiceStoresOnlySuccessfulProbeAndNeverReturnsProbeFailureDetail(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryConnectionRepository()
	port := &scriptedProbePort{errors: map[Transport]error{
		TransportHTTP: errors.New("upstream said https://safe.example.test/mcp X-Private-Header-Name private-token-value"),
		TransportSSE:  errors.New("second upstream failure"),
	}}
	service := testService(t, repository, true, port)
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	created, err := service.Create(ctx, alice, serviceInput("auto", ScopePrivate, TransportAuto))
	require.NoError(t, err)

	_, err = service.Probe(ctx, alice, created.ID)
	require.ErrorIs(t, err, ErrProbeFailed)
	assert.NotContains(t, err.Error(), "safe.example.test")
	assert.NotContains(t, err.Error(), "X-Private-Header-Name")
	assert.NotContains(t, err.Error(), "private-token-value")
	assert.Equal(t, []Transport{TransportHTTP, TransportSSE}, port.attempts)
	assert.Zero(t, repository.recorded, "failed probe bodies and errors must not be persisted")
	version, err := repository.GetVersion(ctx, created.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, ProbeStatusNotTested, version.ProbeStatus)

	port.errors = map[Transport]error{TransportHTTP: nil}
	port.attempts = nil
	probed, err := service.Probe(ctx, alice, created.ID)
	require.NoError(t, err)
	assert.Equal(t, ProbeStatusPassed, probed.ProbeStatus)
	assert.Equal(t, TransportHTTP, probed.DetectedTransport)
	assert.Equal(t, 1, repository.recorded)
	_, err = service.SetEnabled(ctx, alice, created.ID, true)
	require.NoError(t, err)
	options, err := service.TaskOptions(ctx, alice)
	require.NoError(t, err)
	require.Len(t, options, 1)
	assert.Equal(t, created.ID, options[0].ConnectionID)
	assert.Equal(t, 1, options[0].ConnectionVersion)
}

func TestServiceRestrictsTaskUseToUsersAndAdministrators(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryConnectionRepository()
	service := testService(t, repository, true, &scriptedProbePort{errors: map[Transport]error{}})
	admin := identity.Subject{UserID: "admin", Role: identity.RoleAdmin}
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	auditor := identity.Subject{UserID: "auditor", Role: identity.RoleAuditor}
	created, err := service.Create(ctx, admin, serviceInput("global", ScopeGlobal, TransportHTTP))
	require.NoError(t, err)
	_, err = service.Probe(ctx, admin, created.ID)
	require.NoError(t, err)
	_, err = service.SetEnabled(ctx, admin, created.ID, true)
	require.NoError(t, err)

	options, err := service.TaskOptions(ctx, alice)
	require.NoError(t, err)
	require.Len(t, options, 1, "an ordinary user can use a visible global connection")
	_, err = service.TaskOptions(ctx, auditor)
	require.ErrorIs(t, err, ErrForbidden)
	require.ErrorIs(t, service.ValidateTaskConnection(ctx, auditor, created.ID), ErrForbidden)
}

func TestServiceRejectsUnsafeDisplayTextAndOmitsDescriptionFromTaskOptions(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryConnectionRepository()
	service := testService(t, repository, true, &scriptedProbePort{errors: map[Transport]error{}})
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}

	for _, unsafeText := range []string{
		"连接 https://example.invalid/mcp",
		"Git 来源 https://git.example.invalid/org/repo.git",
		"token 标记",
		"cookie 标记",
		"authorization 标记",
		"header 标记",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef0123456789",
		"控制字符\x00",
		"行尾换行\n",
		strings.Repeat("过长", 200),
	} {
		for _, field := range []struct {
			name string
			set  func(*CreateConnectionInput, string)
		}{
			{name: "name", set: func(input *CreateConnectionInput, value string) { input.Name = value }},
			{name: "description", set: func(input *CreateConnectionInput, value string) { input.Description = value }},
		} {
			t.Run(field.name+"/"+unsafeText, func(t *testing.T) {
				input := serviceInput("正常连接", ScopePrivate, TransportHTTP)
				field.set(&input, unsafeText)
				_, err := service.Create(ctx, alice, input)
				require.ErrorIs(t, err, ErrInvalid)
			})
		}
	}

	input := serviceInput("生产环境 MCP", ScopePrivate, TransportHTTP)
	input.Description = "用于业务流程安全扫描"
	created, err := service.Create(ctx, alice, input)
	require.NoError(t, err)
	_, err = service.Probe(ctx, alice, created.ID)
	require.NoError(t, err)
	_, err = service.SetEnabled(ctx, alice, created.ID, true)
	require.NoError(t, err)
	options, err := service.TaskOptions(ctx, alice)
	require.NoError(t, err)
	require.Len(t, options, 1)
	encoded, err := json.Marshal(options[0])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "description")
	summary, err := service.GetSummary(ctx, alice, created.ID)
	require.NoError(t, err)
	encoded, err = json.Marshal(summary)
	require.NoError(t, err)
	for _, forbidden := range []string{"safe.example.test", "X-Private-Header-Name", "X-Custom-Secret-Header", "private-token-value", "custom-secret-value"} {
		assert.NotContains(t, string(encoded), forbidden)
	}
}

func TestServiceDoesNotProjectUnsafeLegacyDisplayText(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryConnectionRepository()
	service := testService(t, repository, true, &scriptedProbePort{errors: map[Transport]error{}})
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	created, err := service.Create(ctx, alice, serviceInput("安全连接", ScopePrivate, TransportHTTP))
	require.NoError(t, err)

	// 模拟版本升级前已落库的自由文本；读取与任务选择仍不能把它投影给浏览器。
	repository.configs[created.ID].Name = "https://legacy.example.invalid/mcp"
	repository.configs[created.ID].Description = "header reference"
	repository.configs[created.ID].Enabled = true
	repository.versions[created.ID][1].ProbeStatus = ProbeStatusPassed
	repository.versions[created.ID][1].DetectedTransport = TransportHTTP

	summary, err := service.GetSummary(ctx, alice, created.ID)
	require.NoError(t, err)
	encoded, err := json.Marshal(summary)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "legacy.example.invalid")
	assert.NotContains(t, string(encoded), "header")
	options, err := service.TaskOptions(ctx, alice)
	require.NoError(t, err)
	assert.Empty(t, options, "unsafe legacy display text must not be task-selectable")
}

func TestServiceRejectsDisplayTextBypassesAndConnectionMaterialEchoes(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryConnectionRepository()
	service := testService(t, repository, true, &scriptedProbePort{errors: map[Transport]error{}})
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	fields := []struct {
		name string
		set  func(*CreateConnectionInput, string)
	}{
		{name: "name", set: func(input *CreateConnectionInput, value string) { input.Name = value }},
		{name: "description", set: func(input *CreateConnectionInput, value string) { input.Description = value }},
	}

	for _, unsafeText := range []string{
		"mcp.internal.example",
		"X-Env",
		"github.internal/team/repo.git",
		strings.Repeat("a", 40),
	} {
		for _, field := range fields {
			t.Run("bypass/"+field.name+"/"+unsafeText, func(t *testing.T) {
				input := serviceInput("正常连接", ScopePrivate, TransportHTTP)
				field.set(&input, unsafeText)
				_, err := service.Create(ctx, alice, input)
				require.ErrorIs(t, err, ErrInvalid)
			})
		}
	}

	for _, material := range []string{
		"https://safe.example.test/mcp",
		"safe.example.test",
		"X-Env",
		"X-Trace",
		"v4lue92",
		"other88",
	} {
		for _, field := range fields {
			t.Run("connection-material/"+field.name+"/"+material, func(t *testing.T) {
				input := serviceInput("正常连接", ScopePrivate, TransportHTTP)
				input.Authentication = Authentication{Kind: AuthenticationAPIKeyHeader, HeaderName: "X-Env", Secret: "v4lue92"}
				input.Headers = []Header{{Name: "X-Trace", Value: "other88"}}
				field.set(&input, material)
				_, err := service.Create(ctx, alice, input)
				require.ErrorIs(t, err, ErrInvalid)
			})
		}
	}

	normal := serviceInput("生产环境 MCP", ScopePrivate, TransportHTTP)
	normal.Description = "仅用于业务流程中的安全扫描"
	_, err := service.Create(ctx, alice, normal)
	require.NoError(t, err, "ordinary Chinese connection labels and descriptions remain supported")

	legacy, err := service.Create(ctx, alice, serviceInput("安全连接", ScopePrivate, TransportHTTP))
	require.NoError(t, err)
	repository.configs[legacy.ID].Name = "mcp.internal.example"
	repository.configs[legacy.ID].Description = "X-Env"
	repository.configs[legacy.ID].Enabled = true
	repository.versions[legacy.ID][1].ProbeStatus = ProbeStatusPassed
	repository.versions[legacy.ID][1].DetectedTransport = TransportHTTP

	summary, err := service.GetSummary(ctx, alice, legacy.ID)
	require.NoError(t, err)
	assert.Empty(t, summary.Name)
	assert.Empty(t, summary.Description)
	options, err := service.TaskOptions(ctx, alice)
	require.NoError(t, err)
	assert.Empty(t, options, "unsafe legacy text must not be task-selectable")
}

func TestServiceRejectsEmbeddedConnectionMaterialAndFailsClosedForLegacyText(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryConnectionRepository()
	service := testService(t, repository, true, &scriptedProbePort{errors: map[Transport]error{}})
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	fields := []struct {
		name string
		set  func(*CreateConnectionInput, string)
	}{
		{name: "name", set: func(input *CreateConnectionInput, value string) { input.Name = value }},
		{name: "description", set: func(input *CreateConnectionInput, value string) { input.Description = value }},
	}

	newInput := func() CreateConnectionInput {
		return CreateConnectionInput{
			Name:        "生产安全扫描",
			Description: "用于业务流程验证",
			Scope:       ScopePrivate,
			Transport:   TransportHTTP,
			ServerURL:   "https://mcp.internal.example/mcp",
			Authentication: Authentication{
				Kind:       AuthenticationAPIKeyHeader,
				HeaderName: "X-Env",
				Secret:     "opaqueauth4729",
			},
			Headers: []Header{{Name: "Content-Type", Value: "orchid938"}},
		}
	}

	for _, unsafeText := range []string{
		"生产 MCP mcp.internal.example",
		"连接 X-Env",
		"Content-Type",
		"Trace-Route",
		"github.internal/team/repo.git",
		strings.Repeat("a", 31),
		"目标 mcp.internal.example",
		"通道 X-Env",
		"标识 opaqueauth4729",
		"花园 orchid938",
	} {
		for _, field := range fields {
			t.Run(field.name+"/"+unsafeText, func(t *testing.T) {
				input := newInput()
				field.set(&input, unsafeText)
				_, err := service.Create(ctx, alice, input)
				require.ErrorIs(t, err, ErrInvalid)
			})
		}
	}

	created, err := service.Create(ctx, alice, newInput())
	require.NoError(t, err)
	repository.configs[created.ID].Name = "标识 opaqueauth4729"
	repository.configs[created.ID].Description = "花园 orchid938"

	list, err := service.List(ctx, alice)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Empty(t, list[0].Name)
	assert.Empty(t, list[0].Description)

	summary, err := service.GetSummary(ctx, alice, created.ID)
	require.NoError(t, err)
	assert.Empty(t, summary.Name)
	assert.Empty(t, summary.Description)

	detail, err := service.GetManagementDetail(ctx, alice, created.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.Name)
	assert.Empty(t, detail.Description)

	probed, err := service.Probe(ctx, alice, created.ID)
	require.NoError(t, err)
	assert.Empty(t, probed.Name)
	assert.Empty(t, probed.Description)

	enabled, err := service.SetEnabled(ctx, alice, created.ID, true)
	require.NoError(t, err)
	assert.Empty(t, enabled.Name)
	assert.Empty(t, enabled.Description)

	options, err := service.TaskOptions(ctx, alice)
	require.NoError(t, err)
	assert.Empty(t, options, "unsafe legacy display text must not be task-selectable")
}

func TestServiceRejectsBlankIdentityAcrossAuthorizationPaths(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryConnectionRepository()
	service := testService(t, repository, true, &scriptedProbePort{errors: map[Transport]error{}})
	alice := identity.Subject{UserID: "alice", Role: identity.RoleUser}
	created, err := service.Create(ctx, alice, serviceInput("安全连接", ScopePrivate, TransportHTTP))
	require.NoError(t, err)
	config := repository.configs[created.ID]

	for _, subject := range []identity.Subject{
		{Role: identity.RoleAdmin},
		{UserID: " \t", Role: identity.RoleAdmin},
		{Role: identity.RoleAuditor},
		{UserID: " \t", Role: identity.RoleAuditor},
	} {
		t.Run(string(subject.Role)+"/blank-user-id", func(t *testing.T) {
			assert.False(t, validReader(subject))
			assert.False(t, canUseForTask(subject))
			assert.False(t, canRead(subject, config))
			assert.False(t, canManage(subject, config))
			assert.False(t, canCreate(subject, ScopePrivate))

			_, err := service.Create(ctx, subject, serviceInput("无效身份", ScopePrivate, TransportHTTP))
			require.ErrorIs(t, err, ErrForbidden)
			_, err = service.TaskOptions(ctx, subject)
			require.ErrorIs(t, err, ErrForbidden)
			require.ErrorIs(t, service.ValidateTaskConnection(ctx, subject, created.ID), ErrForbidden)
			_, err = service.GetSummary(ctx, subject, created.ID)
			require.ErrorIs(t, err, ErrNotFound)
			_, err = service.GetManagementDetail(ctx, subject, created.ID)
			require.ErrorIs(t, err, ErrNotFound)
			_, err = service.SetEnabled(ctx, subject, created.ID, false)
			require.ErrorIs(t, err, ErrNotFound)
		})
	}

	admin := identity.Subject{UserID: "admin", Role: identity.RoleAdmin}
	assert.True(t, validReader(admin))
	assert.True(t, canUseForTask(admin))
	assert.True(t, canRead(admin, config))
	assert.True(t, canManage(admin, config))
	assert.True(t, canCreate(admin, ScopeGlobal))
	assert.True(t, validReader(alice))
	assert.True(t, canUseForTask(alice))
	assert.True(t, canRead(alice, config))
	assert.True(t, canManage(alice, config))
	assert.True(t, canCreate(alice, ScopePrivate))
}
