package websocket

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"

	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"gopkg.in/yaml.v3"
)

var portableProviderNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
var sexagesimalProviderScalar = regexp.MustCompile(`^[+-]?[0-9][0-9_:]*(\.[0-9_]*)?$`)

// validateAgentWorkflowProvider 在保存与下发时校验同一配置，错误不携带配置原文。
// 平台只调度明确支持的单目标网络适配器，避免未知 provider 退回本地伪验证。
func validateAgentWorkflowProvider(data []byte) error {
	if len(data) == 0 || len(data) > 1024*1024 {
		return platformtasks.ErrInvalid
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if decoder.Decode(&document) != nil || unsafeProviderYAML(&document) {
		return platformtasks.ErrInvalid
	}
	var trailing yaml.Node
	if decoder.Decode(&trailing) != io.EOF {
		return platformtasks.ErrInvalid
	}
	var raw any
	if document.Decode(&raw) != nil {
		return platformtasks.ErrInvalid
	}
	if root, ok := raw.(map[string]any); ok {
		providers, hasProviders := root["providers"]
		targets, hasTargets := root["targets"]
		if hasProviders == hasTargets {
			return platformtasks.ErrInvalid
		}
		if hasProviders {
			raw = providers
		} else {
			raw = targets
		}
	}
	items, ok := raw.([]any)
	if !ok || len(items) != 1 {
		return platformtasks.ErrInvalid
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		return platformtasks.ErrInvalid
	}
	id, hasID := item["id"].(string)
	if !hasID && len(item) == 1 {
		for key, value := range item {
			id = key
			item, ok = value.(map[string]any)
		}
		if !ok {
			return platformtasks.ErrInvalid
		}
		if label, exists := item["label"]; exists {
			if _, ok := label.(string); !ok {
				return platformtasks.ErrInvalid
			}
		}
		if delay, exists := item["delay"]; exists {
			if number, ok := delay.(int); !ok || number < 0 {
				return platformtasks.ErrInvalid
			}
		}
	}
	config, ok := item["config"].(map[string]any)
	if !ok || !validProviderFields(config) {
		return platformtasks.ErrInvalid
	}
	family := strings.SplitN(id, ":", 2)[0]
	switch family {
	case "http", "https", "websocket":
		endpoint, _ := config["url"].(string)
		if !validProviderURL(endpoint, true) {
			return platformtasks.ErrInvalid
		}
		if family == "websocket" && !strings.HasPrefix(endpoint, "ws://") && !strings.HasPrefix(endpoint, "wss://") {
			return platformtasks.ErrInvalid
		}
	case "dify":
		// Python 会优先按 config.url 选择 WebSocket，Dify 不能带有冲突的路由字段。
		for _, field := range []string{"url", "endpoint", "method", "body", "headers", "message_template"} {
			if _, exists := config[field]; exists {
				return platformtasks.ErrInvalid
			}
		}
		key, _ := config["apiKey"].(string)
		endpoint, _ := config["apiBaseUrl"].(string)
		extra, extraOK := providerObject(config["extra"])
		if strings.TrimSpace(key) == "" || !validProviderURL(endpoint, false) || !extraOK {
			return platformtasks.ErrInvalid
		}
		kind, _ := extra["dify_type"].(string)
		if kind != "chat" && kind != "workflow" {
			return platformtasks.ErrInvalid
		}
		if inputs, exists := extra["inputs"]; exists {
			if _, ok := inputs.(map[string]any); !ok {
				return platformtasks.ErrInvalid
			}
		}
	default:
		return platformtasks.ErrInvalid
	}
	return nil
}

// 拒绝 YAML 1.1/1.2 有歧义的隐式标量，保持 Go 校验与 Python 执行的类型一致。
func unsafeProviderYAML(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode {
		return true
	}
	switch node.Tag {
	case "", "!!map", "!!seq", "!!str", "!!int", "!!float", "!!bool", "!!null":
	default:
		return true
	}
	if node.Kind == yaml.ScalarNode {
		switch node.Tag {
		case "!!str":
			if node.Style == 0 {
				if strings.Contains(node.Value, ":") && sexagesimalProviderScalar.MatchString(node.Value) {
					return true
				}
				switch strings.ToLower(node.Value) {
				case "yes", "no", "on", "off":
					return true
				}
			}
		case "!!int", "!!float":
			if !portableProviderNumber.MatchString(node.Value) {
				return true
			}
		case "!!bool", "!!null":
		default:
			return true
		}
	}
	if node.Kind == yaml.MappingNode {
		keys := make(map[string]bool)
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Tag != "!!str" || keys[key.Value] {
				return true
			}
			keys[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if unsafeProviderYAML(child) {
			return true
		}
	}
	return false
}

func validProviderURL(value string, websocket bool) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || strings.ContainsAny(value, "\r\n\t \\{}") {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https" || websocket && (parsed.Scheme == "ws" || parsed.Scheme == "wss")
}

// providerObject 兼容现有适配器接受的 YAML 对象和 JSON 对象字符串。
func providerObject(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok {
		return object, true
	}
	if raw, ok := value.(string); ok {
		var object map[string]any
		if json.Unmarshal([]byte(raw), &object) == nil && object != nil {
			return object, true
		}
	}
	return nil, false
}

func validProviderFields(config map[string]any) bool {
	for key, value := range config {
		switch key {
		case "url", "endpoint", "method", "type", "message_template", "transform_response", "model", "apiKey", "apiBaseUrl", "label":
			if _, ok := value.(string); !ok {
				return false
			}
		case "headers", "extra":
			object, ok := providerObject(value)
			if !ok {
				return false
			}
			if key == "headers" {
				for _, header := range object {
					if _, ok := header.(string); !ok {
						return false
					}
				}
			}
		case "body":
			switch value.(type) {
			case string, map[string]any:
			default:
				return false
			}
		case "timeout_ms", "max_tokens", "delay":
			if number, ok := value.(int); !ok || number < 0 {
				return false
			}
		case "temperature":
			switch value.(type) {
			case int, float64:
			default:
				return false
			}
		default:
			return false
		}
	}
	if method, exists := config["method"]; exists {
		switch strings.ToUpper(method.(string)) {
		case "GET", "POST", "PUT", "PATCH", "DELETE":
		default:
			return false
		}
	}
	if endpoint, exists := config["endpoint"]; exists {
		part := endpoint.(string)
		if part != "" && (!strings.HasPrefix(part, "/") || strings.HasPrefix(part, "//") || strings.ContainsAny(part, "\r\n\\")) {
			return false
		}
	}
	return true
}
