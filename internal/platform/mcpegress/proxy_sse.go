package mcpegress

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

const (
	maxProxySSESessions        = 128
	maxProxySSESessionsPerTask = 4
	maxProxySSEEventBytes      = 64 << 10
	maxProxySSEEndpointBytes   = 4096
)

var errProxySSE = errors.New("invalid MCP stream")

// 上游会话地址只留在服务端；公开随机标识同时受任务、能力轮换及原连接版本约束。
type proxySSESession struct {
	taskID         string
	capabilityHash []byte
	bound          boundSource
	endpoint       *url.URL
	ctx            context.Context
	httpSessionID  string
	cancel         context.CancelFunc
}

func (proxy *Proxy) reserveSSESession(taskID, capability string, bound boundSource, ctx context.Context) (string, *proxySSESession, error) {
	proxy.sessionsMu.Lock()
	defer proxy.sessionsMu.Unlock()
	count := 0
	for id, session := range proxy.sessions {
		if session.ctx.Err() != nil {
			delete(proxy.sessions, id)
			continue
		}
		if session.taskID == taskID {
			count++
		}
	}
	if len(proxy.sessions) >= maxProxySSESessions || count >= maxProxySSESessionsPerTask {
		return "", nil, errProxySSE
	}
	id, err := randomCapability()
	if err != nil {
		return "", nil, errProxySSE
	}
	if _, exists := proxy.sessions[id]; exists {
		return "", nil, errProxySSE
	}
	session := &proxySSESession{taskID: taskID, capabilityHash: capabilityDigest(capability), bound: bound, ctx: ctx}
	proxy.sessions[id] = session
	return id, session, nil
}

func (proxy *Proxy) removeSSESession(id string) {
	proxy.sessionsMu.Lock()
	session := proxy.sessions[id]
	delete(proxy.sessions, id)
	proxy.sessionsMu.Unlock()
	if session != nil && session.cancel != nil {
		session.cancel()
	}
}

func (proxy *Proxy) findSSESession(id, taskID, capability string, bound boundSource) *proxySSESession {
	proxy.sessionsMu.Lock()
	defer proxy.sessionsMu.Unlock()
	session := proxy.sessions[id]
	if session == nil || session.endpoint == nil || session.ctx.Err() != nil || session.taskID != taskID ||
		subtle.ConstantTimeCompare(session.capabilityHash, capabilityDigest(capability)) != 1 || !sameProxyBinding(session.bound, bound) {
		return nil
	}
	return session
}

func validProxySessionID(value string) bool {
	if len(value) != 43 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func (proxy *Proxy) forwardLegacySSE(writer http.ResponseWriter, response *http.Response, base *url.URL, sessionID string, session *proxySSESession) {
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || contentType != "text/event-stream" || response.StatusCode != http.StatusOK {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return
	}
	parser := newProxySSEParser(response.Body)
	first, err := parser.next()
	preludeBytes := first.bytes
	for err == nil && first.event == "" && len(first.data) == 0 && preludeBytes <= maxProxySSEEventBytes {
		first, err = parser.next()
		preludeBytes += first.bytes
	}
	if err != nil || preludeBytes > maxProxySSEEventBytes || first.event != "endpoint" || len(first.data) != 1 {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return
	}
	endpoint, err := proxyMessageEndpoint(base, first.data[0])
	if err != nil || session.ctx.Err() != nil {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return
	}
	proxy.sessionsMu.Lock()
	session.endpoint = endpoint
	proxy.sessionsMu.Unlock()
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusOK)
	output := proxyFlushWriter{writer: writer}
	if _, err := io.WriteString(output, "event: endpoint\ndata: "+internalGatewayPathPrefix+session.taskID+"/sessions/"+sessionID+"\n\n"); err != nil {
		return
	}
	for {
		event, err := parser.next()
		if err != nil || event.event == "endpoint" || session.ctx.Err() != nil {
			return
		}
		event, err = sanitizeProxySSEEvent(event, proxySensitiveValues(session.bound, session))
		if err != nil {
			return
		}
		if _, err := io.WriteString(output, event.encode()); err != nil {
			return
		}
	}
}

// 只允许原 HTTPS origin 的服务端消息路径，查询值只能是无结构的会话标识。
// URL 参数、控制字符、用户认证及片段均不可借助 endpoint 事件进入请求目标。
func proxyMessageEndpoint(base *url.URL, raw string) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > maxProxySSEEndpointBytes || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\\\r\n\t#") {
		return nil, errProxySSE
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery {
		return nil, errProxySSE
	}
	for _, character := range parsed.Path {
		if character < 0x20 || character == 0x7f || character == '%' || character == '\\' {
			return nil, errProxySSE
		}
	}
	endpoint := base.ResolveReference(parsed)
	if endpoint.Scheme != "https" || !strings.EqualFold(endpoint.Hostname(), base.Hostname()) || proxyHTTPSPort(endpoint) != proxyHTTPSPort(base) ||
		strings.ContainsAny(endpoint.Path, "\\\r\n\t\x00") || endpoint.Path == "" {
		return nil, errProxySSE
	}
	for _, part := range strings.Split(endpoint.Path, "/") {
		if part == "." || part == ".." {
			return nil, errProxySSE
		}
	}
	query, err := url.ParseQuery(endpoint.RawQuery)
	if err != nil || len(query) > 4 {
		return nil, errProxySSE
	}
	for key, values := range query {
		if !proxyOpaqueQuery(key) || len(values) != 1 || !proxyOpaqueQuery(values[0]) {
			return nil, errProxySSE
		}
	}
	return endpoint, nil
}

func proxyHTTPSPort(endpoint *url.URL) string {
	if endpoint.Port() == "" {
		return "443"
	}
	return endpoint.Port()
}

func proxyOpaqueQuery(value string) bool {
	if value == "" || len(value) > 2048 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-_.~+=", character) {
			continue
		}
		return false
	}
	return true
}

type proxySSEEvent struct {
	event, id, retry string
	data             []string
	bytes            int
}
type proxySSEParser struct{ scanner *bufio.Scanner }

func newProxySSEParser(reader io.Reader) *proxySSEParser {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxProxySSEEventBytes+1)
	scanner.Split(splitProxySSELine)
	return &proxySSEParser{scanner: scanner}
}

func splitProxySSELine(data []byte, atEOF bool) (int, []byte, error) {
	if index := bytes.IndexAny(data, "\r\n"); index >= 0 {
		if data[index] == '\r' && index+1 == len(data) && !atEOF {
			return 0, nil, nil
		}
		advance := index + 1
		if data[index] == '\r' && advance < len(data) && data[advance] == '\n' {
			advance++
		}
		return advance, data[:index], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (parser *proxySSEParser) next() (proxySSEEvent, error) {
	event := proxySSEEvent{}
	size := 0
	eventSet := false
	for parser.scanner.Scan() {
		line := parser.scanner.Text()
		size += len(line) + 1
		if size > maxProxySSEEventBytes || strings.ContainsRune(line, '\x00') {
			return proxySSEEvent{}, errProxySSE
		}
		if line == "" {
			event.bytes = size
			return event, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			if eventSet {
				return proxySSEEvent{}, errProxySSE
			}
			eventSet = true
			event.event = value
		case "data":
			event.data = append(event.data, value)
		case "id":
			event.id = value
		case "retry":
			event.retry = value
		}
	}
	if parser.scanner.Err() != nil || eventSet || len(event.data) > 0 {
		return proxySSEEvent{}, errProxySSE
	}
	return proxySSEEvent{}, io.EOF
}

func (event proxySSEEvent) encode() string {
	if event.event == "" && event.id == "" && event.retry == "" && len(event.data) == 0 {
		return ": keepalive\n\n"
	}
	var output strings.Builder
	if event.event != "" {
		output.WriteString("event: " + event.event + "\n")
	}
	if event.id != "" {
		output.WriteString("id: " + event.id + "\n")
	}
	if event.retry != "" {
		output.WriteString("retry: " + event.retry + "\n")
	}
	for _, data := range event.data {
		output.WriteString("data: " + data + "\n")
	}
	output.WriteByte('\n')
	return output.String()
}
