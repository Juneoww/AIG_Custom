package mcpconnections

import (
	"errors"
	"net/http"
)

// ErrRuntimePayloadInvalid deliberately carries no endpoint, header, or
// credential detail. Callers may return a fixed unavailable result without
// creating a disclosure path for malformed historical ciphertext.
var ErrRuntimePayloadInvalid = errors.New("MCP 连接运行时载荷无效")

// ApplyRuntimeAuthentication applies the exact, canonical connection payload
// to an already target-bound request. It is shared by probing and the egress
// gateway so both paths accept the same safe credential combinations: custom
// headers first, followed by a managed Bearer or API-key header which wins any
// same-name conflict. The caller must never use it with a browser-supplied
// target or header map.
func ApplyRuntimeAuthentication(header http.Header, payload ConnectionPayload) error {
	canonical, valid := canonicalConnectionPayload(payload)
	if !valid || !sameCanonicalConnectionPayload(canonical, payload) {
		return ErrRuntimePayloadInvalid
	}
	for _, configured := range payload.Headers {
		header.Set(configured.Name, configured.Value)
	}
	switch payload.Authentication.Kind {
	case AuthenticationNone, AuthenticationCustomHeaders:
		return nil
	case AuthenticationBearer:
		header.Set("Authorization", "Bearer "+payload.Authentication.Secret)
		return nil
	case AuthenticationAPIKeyHeader:
		header.Set(payload.Authentication.HeaderName, payload.Authentication.Secret)
		return nil
	default:
		return ErrRuntimePayloadInvalid
	}
}

func sameCanonicalConnectionPayload(left, right ConnectionPayload) bool {
	if left.Endpoint != right.Endpoint || left.Authentication != right.Authentication || len(left.Headers) != len(right.Headers) {
		return false
	}
	for index := range left.Headers {
		if left.Headers[index] != right.Headers[index] {
			return false
		}
	}
	return true
}
