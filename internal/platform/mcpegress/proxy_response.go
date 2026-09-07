package mcpegress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

const maxProxyResponseBytes = 4 << 20

var errProxyResponse = errors.New("invalid MCP response")

// 按完整 JSON 或有上限的 SSE 事件脱敏，避免读缓冲区边界及 JSON 转义绕过。
// 只处理当前绑定的字面值，保留正常协议标识；未知内容类型和解析失败返回固定错误。
func (proxy *Proxy) forwardProtocolResponse(writer http.ResponseWriter, response *http.Response, bound boundSource, session *proxySSESession, ctx context.Context) bool {
	sensitive := proxySensitiveValues(bound, session)
	contentType, _, typeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if typeErr == nil && contentType == "text/event-stream" {
		parser := newProxySSEParser(response.Body)
		if ctx.Err() != nil {
			return false
		}
		copySafeProxyResponseHeaders(writer.Header(), response.Header, sensitive)
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(response.StatusCode)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		for {
			event, err := parser.next()
			if err != nil {
				return true
			}
			event, err = sanitizeProxySSEEvent(event, sensitive)
			if err != nil {
				return true
			}
			if ctx.Err() != nil {
				return true
			}
			if _, err := io.WriteString(proxyFlushWriter{writer: writer}, event.encode()); err != nil {
				return true
			}
		}
	}
	if contentType != "application/json" && response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusNoContent {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return false
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxProxyResponseBytes+1))
	if err != nil || len(content) > maxProxyResponseBytes {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if len(bytes.TrimSpace(content)) == 0 && (response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusNoContent) {
		copySafeProxyResponseHeaders(writer.Header(), response.Header, sensitive)
		writer.WriteHeader(response.StatusCode)
		return true
	}
	if contentType != "application/json" {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return false
	}
	content, err = sanitizeProxyJSON(content, sensitive)
	if err != nil {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return false
	}
	copySafeProxyResponseHeaders(writer.Header(), response.Header, sensitive)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(response.StatusCode)
	_, err = writer.Write(content)
	return err == nil
}

func proxySensitiveValues(bound boundSource, session *proxySSESession) []string {
	values := []string{bound.payload.Endpoint, bound.payload.Authentication.HeaderName, bound.payload.Authentication.Secret}
	if session != nil && session.httpSessionID != "" {
		values = append(values, session.httpSessionID)
	}
	for _, header := range bound.payload.Headers {
		values = append(values, header.Name, header.Value)
	}
	if session != nil && session.endpoint != nil {
		values = append(values, session.endpoint.String(), session.endpoint.RequestURI())
		for _, valuesForKey := range session.endpoint.Query() {
			values = append(values, valuesForKey...)
		}
	}
	for _, raw := range []string{bound.payload.Endpoint} {
		if endpoint, err := url.Parse(raw); err == nil && endpoint != nil {
			values = append(values, endpoint.Host)
			if endpoint.Path != "" && endpoint.Path != "/" {
				values = append(values, endpoint.Path)
			}
		}
	}
	return normalizedRedactionValues(values)
}

func replaceProxySensitive(value string, sensitive []string) string {
	for _, literal := range sensitive {
		value = strings.ReplaceAll(value, literal, "[REDACTED]")
	}
	return value
}

func copySafeProxyResponseHeaders(destination, source http.Header, sensitive []string) {
	for _, name := range proxyResponseHeaderNames {
		for _, value := range source.Values(name) {
			if replaceProxySensitive(value, sensitive) == value {
				destination.Add(name, value)
			}
		}
	}
}

func sanitizeProxySSEEvent(event proxySSEEvent, sensitive []string) (proxySSEEvent, error) {
	if event.event != "" && event.event != "message" || replaceProxySensitive(event.id, sensitive) != event.id || replaceProxySensitive(event.retry, sensitive) != event.retry {
		return proxySSEEvent{}, errProxyResponse
	}
	if len(event.data) > 0 {
		content, err := sanitizeProxyJSON([]byte(strings.Join(event.data, "\n")), sensitive)
		if err != nil {
			return proxySSEEvent{}, err
		}
		event.data = []string{string(content)}
	}
	return event, nil
}

func sanitizeProxyJSON(content []byte, sensitive []string) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, errProxyResponse
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errProxyResponse
	}
	result, err := sanitizeProxyValue(value, sensitive, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func sanitizeProxyValue(value any, sensitive []string, depth int) (any, error) {
	if depth > 128 {
		return nil, errProxyResponse
	}
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, nested := range typed {
			key = replaceProxySensitive(key, sensitive)
			if _, exists := result[key]; exists {
				return nil, errProxyResponse
			}
			redacted, err := sanitizeProxyValue(nested, sensitive, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = redacted
		}
		return result, nil
	case []any:
		for index, nested := range typed {
			redacted, err := sanitizeProxyValue(nested, sensitive, depth+1)
			if err != nil {
				return nil, err
			}
			typed[index] = redacted
		}
		return typed, nil
	case string:
		return replaceProxySensitive(typed, sensitive), nil
	case json.Number, bool, nil:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, errProxyResponse
		}
		for _, literal := range sensitive {
			if string(encoded) == literal {
				return "[REDACTED]", nil
			}
		}
		return typed, nil
	default:
		return value, nil
	}
}
