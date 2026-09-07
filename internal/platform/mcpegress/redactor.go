package mcpegress

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/google/uuid"
)

var (
	mcpEventURLPattern   = regexp.MustCompile(`(?i)\b(?:https?|wss?)://[^\s"'<>]+`)
	mcpEventGitPattern   = regexp.MustCompile(`\bgit@[A-Za-z0-9.-]+:[^\s"'<>]+`)
	mcpEventPathPattern  = regexp.MustCompile(`(?i)(?:[a-z]:\\|/)[^\s"'<>]+`)
	mcpEventTokenPattern = regexp.MustCompile(`\b[A-Za-z0-9_-]{20,}\b`)
)

// RedactMCPEvent creates a fresh, safe event projection for one MCP task.
// It resolves the immutable binding only in memory, extracts literal sentinels
// from its encrypted source material, then recursively redacts arbitrary Agent
// event values. It deliberately does not require an active task/capability:
// terminal and cancelled events still need sanitising before any persistence.
func (service *Service) RedactMCPEvent(ctx context.Context, taskID string, event any) (any, error) {
	sensitive, err := service.eventRedactionValues(ctx, taskID)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, ErrRuntimeUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var copied any
	if decoder.Decode(&copied) != nil {
		return nil, ErrRuntimeUnavailable
	}
	return redactMCPEventValue(copied, sensitive), nil
}

func (service *Service) eventRedactionValues(ctx context.Context, taskID string) ([]string, error) {
	if service == nil || service.tasks == nil || service.bindings == nil || service.keyring == nil || strings.TrimSpace(taskID) == "" {
		return nil, ErrRuntimeUnavailable
	}
	task, err := service.tasks.Get(ctx, taskID)
	if err != nil || !redactableMCPTask(task) {
		return nil, ErrRuntimeUnavailable
	}
	binding, err := service.bindings.GetTaskBinding(ctx, task.ID)
	if err != nil || binding == nil || binding.TaskID != task.ID {
		return nil, ErrRuntimeUnavailable
	}
	values := []string{CapabilityHeader, "X-Internal-Agent-Token"}
	switch binding.SourceKind {
	case "service":
		if binding.ConnectionConfigID == nil || binding.ConnectionConfigVersion == nil || *binding.ConnectionConfigVersion < 1 {
			return nil, ErrRuntimeUnavailable
		}
		config, configErr := service.bindings.GetConfig(ctx, *binding.ConnectionConfigID)
		if configErr != nil || config == nil || config.ID != *binding.ConnectionConfigID {
			return nil, ErrRuntimeUnavailable
		}
		version, versionErr := service.bindings.GetVersion(ctx, config.ID, *binding.ConnectionConfigVersion)
		if versionErr != nil || version == nil || version.ConnectionConfigID != config.ID || version.Version != *binding.ConnectionConfigVersion {
			return nil, ErrRuntimeUnavailable
		}
		payload, payloadErr := service.keyring.OpenConnectionPayload(config, version)
		if payloadErr != nil {
			return nil, ErrRuntimeUnavailable
		}
		values = append(values, payload.Endpoint, payload.Authentication.HeaderName, payload.Authentication.Secret)
		for _, header := range payload.Headers {
			values = append(values, header.Name, header.Value)
		}
	case "repository":
		if binding.ConnectionConfigID != nil || binding.ConnectionConfigVersion != nil {
			return nil, ErrRuntimeUnavailable
		}
		snapshot, snapshotErr := service.keyring.OpenRepositorySource(binding, mcpconnections.BindingEncryptionContext{
			OwnerUserID: task.OwnerUserID, Scope: mcpconnections.ScopePrivate, Version: 1,
		})
		if snapshotErr != nil {
			return nil, ErrRuntimeUnavailable
		}
		values = append(values, snapshot.RepositoryURL)
	default:
		return nil, ErrRuntimeUnavailable
	}
	return normalizedRedactionValues(values), nil
}

func redactableMCPTask(task *tasks.Task) bool {
	return task != nil && (task.TaskType == "mcp_scan" || task.TaskType == "Mcp-Scan") &&
		strings.TrimSpace(task.ID) != "" && strings.TrimSpace(task.OwnerUserID) != ""
}

func normalizedRedactionValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(left, right int) bool { return len(result[left]) > len(result[right]) })
	return result
}

func redactMCPEventValue(value any, sensitive []string) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, nested := range typed {
			// Agent-defined object keys are data too. Omit sensitive keys instead
			// of renaming them into a colliding key or leaking credential names.
			if redactMCPEventString(key, sensitive) != key {
				continue
			}
			if sensitiveMCPEventKey(key) {
				redacted[key] = "[已脱敏]"
				continue
			}
			if safeMCPEventIdentity(key, nested, sensitive) {
				redacted[key] = nested
				continue
			}
			redacted[key] = redactMCPEventValue(nested, sensitive)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, nested := range typed {
			redacted[index] = redactMCPEventValue(nested, sensitive)
		}
		return redacted
	case string:
		return redactMCPEventString(typed, sensitive)
	case json.Number, bool, nil:
		encoded, _ := json.Marshal(typed)
		for _, literal := range sensitive {
			if string(encoded) == literal {
				return "[已脱敏]"
			}
		}
		return typed
	default:
		return value
	}
}

// Scanner tool/action correlation uses canonical UUIDs. Keep those structural
// identities distinct, but never exempt a UUID that matches bound credentials.
func safeMCPEventIdentity(key string, value any, sensitive []string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
	switch normalized {
	case "id", "toolid", "actionid", "stepid", "planstepid", "statusid", "sessionid":
	default:
		return false
	}
	text, ok := value.(string)
	if !ok {
		return false
	}
	identifier, err := uuid.Parse(text)
	if err != nil || identifier.String() != strings.ToLower(text) {
		return false
	}
	for _, literal := range sensitive {
		if strings.Contains(text, literal) {
			return false
		}
	}
	return true
}

func sensitiveMCPEventKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(key))
	for _, fragment := range []string{
		"authorization", "token", "secret", "password", "cookie", "capability", "header",
		"endpoint", "serverurl", "repositoryurl", "archive", "proxyurl", "filepath", "path",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return normalized == "url" || strings.HasSuffix(normalized, "url")
}

func redactMCPEventString(value string, sensitive []string) string {
	for _, literal := range sensitive {
		value = strings.ReplaceAll(value, literal, "[已脱敏]")
	}
	value = mcpEventURLPattern.ReplaceAllString(value, "[已脱敏地址]")
	value = mcpEventGitPattern.ReplaceAllString(value, "[已脱敏仓库]")
	value = mcpEventPathPattern.ReplaceAllString(value, "[已脱敏路径]")
	return mcpEventTokenPattern.ReplaceAllString(value, "[已脱敏]")
}
