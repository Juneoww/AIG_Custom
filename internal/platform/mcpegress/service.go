package mcpegress

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
)

const (
	defaultCapabilityTTL = 5 * time.Minute
	maxCapabilityTTL     = 15 * time.Minute
)

const archiveReferencePrefix = "archive:"

var (
	// ErrRuntimeUnavailable is intentionally nonspecific. It is safe for an
	// Agent dispatch error because it does not distinguish binding, policy,
	// decrypt, DNS, endpoint, header, or credential failures.
	ErrRuntimeUnavailable = errors.New("MCP 运行时不可用")
	ErrCapabilityDenied   = errors.New("MCP 运行时能力无效或已过期")
)

type TaskReader interface {
	Get(context.Context, string) (*tasks.Task, error)
}

type BindingReader interface {
	GetTaskBinding(context.Context, string) (*mcpconnections.TaskBinding, error)
	GetConfig(context.Context, string) (*mcpconnections.ConnectionConfig, error)
	GetVersion(context.Context, string, int) (*mcpconnections.ConnectionVersion, error)
}

// RepositoryFetcher is the only port that may consume a decrypted repository
// URL. A production implementation must use the controlled Git dialer and
// return an opaque platform archive reference, never a file path or URL.
type RepositoryFetcher interface {
	FetchRepository(context.Context, RepositoryFetchRequest) (string, error)
}

type ServiceDependencies struct {
	Tasks         TaskReader
	Bindings      BindingReader
	Keyring       *mcpconnections.Keyring
	Capabilities  CapabilityRepository
	Policy        *mcpconnections.OutboundPolicy
	Fetcher       RepositoryFetcher
	GatewayURL    string
	CapabilityTTL time.Duration
	Now           func() time.Time
	NewCapability func() (string, error)
}

// Service creates short-lived runtime assignments and validates them at the
// agent-only egress boundary. It keeps decrypted sources private to this
// package and returns only opaque runtime material to the scheduler.
type Service struct {
	tasks         TaskReader
	bindings      BindingReader
	keyring       *mcpconnections.Keyring
	capabilities  CapabilityRepository
	policy        *mcpconnections.OutboundPolicy
	fetcher       RepositoryFetcher
	gatewayURL    string
	capabilityTTL time.Duration
	now           func() time.Time
	newCapability func() (string, error)
}

var _ tasks.MCPRuntimeIssuer = (*Service)(nil)

func NewService(dependencies ServiceDependencies) *Service {
	ttl := dependencies.CapabilityTTL
	if ttl <= 0 {
		ttl = defaultCapabilityTTL
	}
	if ttl > maxCapabilityTTL {
		ttl = maxCapabilityTTL
	}
	now := dependencies.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	newCapability := dependencies.NewCapability
	if newCapability == nil {
		newCapability = randomCapability
	}
	return &Service{
		tasks: dependencies.Tasks, bindings: dependencies.Bindings, keyring: dependencies.Keyring,
		capabilities: dependencies.Capabilities, policy: dependencies.Policy, fetcher: dependencies.Fetcher,
		gatewayURL: strings.TrimRight(strings.TrimSpace(dependencies.GatewayURL), "/"), capabilityTTL: ttl,
		now: now, newCapability: newCapability,
	}
}

// IssueRuntime resolves the immutable task binding at assignment time. It
// rechecks egress policy instead of trusting a historical probe, then returns
// only a gateway URL/capability/transport or an opaque archive reference.
func (service *Service) IssueRuntime(ctx context.Context, taskID string) (Runtime, error) {
	bound, err := service.resolveBoundSource(ctx, taskID)
	if err != nil {
		return Runtime{}, ErrRuntimeUnavailable
	}
	switch bound.kind {
	case "service":
		proxyURL, err := service.proxyURLForTask(bound.task.ID)
		if err != nil {
			return Runtime{}, ErrRuntimeUnavailable
		}
		rawCapability, err := service.newCapability()
		if err != nil || !validRawCapability(rawCapability) {
			return Runtime{}, ErrRuntimeUnavailable
		}
		issuedAt := service.now().UTC()
		if _, err := service.capabilities.Issue(ctx, bound.task.ID, capabilityDigest(rawCapability), issuedAt, issuedAt.Add(service.capabilityTTL)); err != nil {
			return Runtime{}, ErrRuntimeUnavailable
		}
		return Runtime{
			MCPProxyURL: proxyURL, TaskCapability: rawCapability, EffectiveTransport: string(bound.transport),
		}, nil
	case "repository":
		if service.fetcher == nil {
			return Runtime{}, ErrRuntimeUnavailable
		}
		archiveRef, err := service.fetcher.FetchRepository(ctx, RepositoryFetchRequest{
			TaskID: bound.task.ID, OwnerUserID: bound.task.OwnerUserID, RepositoryURL: bound.repositoryURL,
		})
		if err != nil || !validArchiveReference(archiveRef) {
			return Runtime{}, ErrRuntimeUnavailable
		}
		return Runtime{ArchiveRef: archiveRef}, nil
	default:
		return Runtime{}, ErrRuntimeUnavailable
	}
}

// IssueRuntimeParams adapts the opaque runtime result to the task-domain
// assignment interface. It deliberately returns a fresh map on every call so
// a retry cannot recover a token from a Session or mutable shared map.
func (service *Service) IssueRuntimeParams(ctx context.Context, taskID string) (map[string]any, error) {
	runtime, err := service.IssueRuntime(ctx, taskID)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	return runtime.TaskRuntimeParams(), nil
}

// VerifyCapability is used by the Agent-only gateway before every upstream
// request. Only the latest unexpired record for the exact task can authorize
// a proxy operation; an older assignment token is revoked by rotation.
func (service *Service) VerifyCapability(ctx context.Context, taskID, rawCapability string) error {
	_, err := service.verifiedCapability(ctx, taskID, rawCapability)
	return err
}

func (service *Service) verifiedCapability(ctx context.Context, taskID, rawCapability string) (*RuntimeCapability, error) {
	if service == nil || service.tasks == nil || service.capabilities == nil || strings.TrimSpace(taskID) == "" || !validRawCapability(rawCapability) {
		return nil, ErrCapabilityDenied
	}
	// A task can become cancelled or terminal after its Agent received a short-
	// lived token. Recheck its state for every upstream request so terminal work
	// loses egress immediately instead of waiting for the token TTL to expire.
	task, err := service.tasks.Get(ctx, taskID)
	if err != nil || !isMCPTask(task) {
		return nil, ErrCapabilityDenied
	}
	latest, err := service.capabilities.Latest(ctx, taskID)
	if err != nil || latest == nil || latest.TaskID != taskID || !latest.ExpiresAt.After(service.now().UTC()) {
		return nil, ErrCapabilityDenied
	}
	digest := capabilityDigest(rawCapability)
	if subtle.ConstantTimeCompare(latest.CapabilityHash, digest) != 1 {
		return nil, ErrCapabilityDenied
	}
	return latest, nil
}

type boundSource struct {
	expiresAt          time.Time
	capabilityRotation int
	configID           string
	configVersion      int
	task               *tasks.Task
	kind               string
	transport          mcpconnections.Transport
	repositoryURL      string
	payload            mcpconnections.ConnectionPayload
}

func (service *Service) resolveBoundSource(ctx context.Context, taskID string) (boundSource, error) {
	if service == nil || service.tasks == nil || service.bindings == nil || service.keyring == nil || service.capabilities == nil || service.policy == nil || strings.TrimSpace(taskID) == "" {
		return boundSource{}, ErrRuntimeUnavailable
	}
	task, err := service.tasks.Get(ctx, taskID)
	if err != nil || !isMCPTask(task) {
		return boundSource{}, ErrRuntimeUnavailable
	}
	binding, err := service.bindings.GetTaskBinding(ctx, task.ID)
	if err != nil || binding == nil || binding.TaskID != task.ID {
		return boundSource{}, ErrRuntimeUnavailable
	}
	switch binding.SourceKind {
	case "service":
		if binding.ConnectionConfigID == nil || binding.ConnectionConfigVersion == nil || *binding.ConnectionConfigVersion < 1 {
			return boundSource{}, ErrRuntimeUnavailable
		}
		config, err := service.bindings.GetConfig(ctx, *binding.ConnectionConfigID)
		if err != nil || config == nil || !config.Enabled {
			return boundSource{}, ErrRuntimeUnavailable
		}
		version, err := service.bindings.GetVersion(ctx, *binding.ConnectionConfigID, *binding.ConnectionConfigVersion)
		if err != nil || version == nil || version.ConnectionConfigID != config.ID || version.Version != *binding.ConnectionConfigVersion ||
			version.ProbeStatus != mcpconnections.ProbeStatusPassed || !concreteTransport(version.DetectedTransport) {
			return boundSource{}, ErrRuntimeUnavailable
		}
		if service.policy.RequireControlledDialer() != nil {
			return boundSource{}, ErrRuntimeUnavailable
		}
		payload, err := service.keyring.OpenConnectionPayload(config, version)
		if err != nil || service.policy.ValidateServerURL(ctx, payload.Endpoint) != nil {
			return boundSource{}, ErrRuntimeUnavailable
		}
		return boundSource{task: task, kind: "service", transport: version.DetectedTransport, payload: payload,
			configID: config.ID, configVersion: version.Version}, nil
	case "repository":
		if binding.ConnectionConfigID != nil || binding.ConnectionConfigVersion != nil || service.policy.RequireControlledDialer() != nil {
			return boundSource{}, ErrRuntimeUnavailable
		}
		snapshot, err := service.keyring.OpenRepositorySource(binding, mcpconnections.BindingEncryptionContext{
			OwnerUserID: task.OwnerUserID, Scope: mcpconnections.ScopePrivate, Version: 1,
		})
		if err != nil {
			return boundSource{}, ErrRuntimeUnavailable
		}
		var attachmentIDs []string
		if len(task.AttachmentRefs) > tasks.MaxTaskParamsLength || (len(task.AttachmentRefs) > 0 && json.Unmarshal(task.AttachmentRefs, &attachmentIDs) != nil) {
			return boundSource{}, ErrRuntimeUnavailable
		}
		if snapshot.RepositoryURL != "" {
			if len(attachmentIDs) > 0 || service.policy.ValidateGitURL(ctx, snapshot.RepositoryURL) != nil {
				return boundSource{}, ErrRuntimeUnavailable
			}
		} else if len(attachmentIDs) == 0 || len(attachmentIDs) > tasks.MaxTaskAttachmentCount {
			return boundSource{}, ErrRuntimeUnavailable
		}
		return boundSource{task: task, kind: "repository", repositoryURL: snapshot.RepositoryURL}, nil
	default:
		return boundSource{}, ErrRuntimeUnavailable
	}
}

// resolveServiceProxyTarget performs a fresh capability, task, binding,
// configuration, decrypt, and egress-policy check for every gateway request.
// Its decrypted payload is package-private and must never cross into an Agent
// frame, task parameter, browser response, log, or error message.
func (service *Service) resolveServiceProxyTarget(ctx context.Context, taskID, rawCapability string) (boundSource, error) {
	capability, err := service.verifiedCapability(ctx, taskID, rawCapability)
	if err != nil {
		return boundSource{}, ErrCapabilityDenied
	}
	bound, err := service.resolveBoundSource(ctx, taskID)
	if err != nil || bound.kind != "service" {
		return boundSource{}, ErrCapabilityDenied
	}
	bound.expiresAt = capability.ExpiresAt
	bound.capabilityRotation = capability.Rotation
	return bound, nil
}

func (service *Service) proxyURLForTask(taskID string) (string, error) {
	if service == nil || strings.TrimSpace(taskID) == "" || service.gatewayURL == "" {
		return "", ErrRuntimeUnavailable
	}
	parsed, err := url.Parse(service.gatewayURL)
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrRuntimeUnavailable
	}
	return service.gatewayURL + internalGatewayPathPrefix + taskID, nil
}

func randomCapability() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func capabilityDigest(raw string) []byte {
	digest := sha256.Sum256([]byte(raw))
	return digest[:]
}

func validRawCapability(raw string) bool {
	return raw != "" && raw == strings.TrimSpace(raw) && len(raw) <= 512
}

func validArchiveReference(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || !strings.HasPrefix(value, archiveReferencePrefix) {
		return false
	}
	identifier := strings.TrimPrefix(value, archiveReferencePrefix)
	if identifier == "" || len(identifier) > 128 {
		return false
	}
	for _, character := range identifier {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func isMCPTask(task *tasks.Task) bool {
	return task != nil && (task.TaskType == "mcp_scan" || task.TaskType == "Mcp-Scan") &&
		strings.TrimSpace(task.ID) != "" && strings.TrimSpace(task.OwnerUserID) != "" && activeRuntimeStatus(task.Status)
}

func activeRuntimeStatus(status tasks.Status) bool {
	switch status {
	case tasks.StatusPending, tasks.StatusDispatching, tasks.StatusDispatchUnknown, tasks.StatusRunning:
		return true
	default:
		return false
	}
}

func concreteTransport(transport mcpconnections.Transport) bool {
	return transport == mcpconnections.TransportHTTP || transport == mcpconnections.TransportSSE
}
