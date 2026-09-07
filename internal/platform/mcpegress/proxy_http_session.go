package mcpegress

import (
	"context"
	"crypto/subtle"
)

// Streamable HTTP 会话与 SSE 共用有上限的服务端注册表，但生命周期独立于单次 HTTP 响应。
func (proxy *Proxy) createHTTPSession(taskID, capability string, bound boundSource, upstreamID string) (string, *proxySSESession, error) {
	ctx, cancel := context.WithDeadline(context.Background(), bound.expiresAt)
	id, session, err := proxy.reserveSSESession(taskID, capability, bound, ctx)
	if err != nil {
		cancel()
		return "", nil, err
	}
	proxy.sessionsMu.Lock()
	session.httpSessionID = upstreamID
	session.cancel = cancel
	proxy.sessionsMu.Unlock()
	context.AfterFunc(ctx, func() { proxy.removeSSESession(id) })
	go proxy.watchBinding(ctx, cancel, taskID, capability, bound)
	return id, session, nil
}

func (proxy *Proxy) findHTTPSession(id, taskID, capability string, bound boundSource) *proxySSESession {
	proxy.sessionsMu.Lock()
	defer proxy.sessionsMu.Unlock()
	session := proxy.sessions[id]
	if session == nil || session.httpSessionID == "" || session.ctx.Err() != nil || session.taskID != taskID ||
		subtle.ConstantTimeCompare(session.capabilityHash, capabilityDigest(capability)) != 1 || !sameProxyBinding(session.bound, bound) {
		return nil
	}
	return session
}

func validUpstreamHTTPSessionID(value string) bool {
	if len(value) == 0 || len(value) > 512 {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
