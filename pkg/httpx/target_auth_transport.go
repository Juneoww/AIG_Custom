package httpx

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// targetAuthTransport 是认证注入的唯一入口；请求原件与返回对象均不持有密钥。
type targetAuthTransport struct {
	base *http.Transport
	auth *TargetAuth
}

func (t *targetAuthTransport) validateRequest(req *http.Request) error {
	if req == nil || req.URL == nil || req.URL.User != nil || req.URL.Opaque != "" {
		return errTargetAuthScope
	}
	if err := ValidateTargetURLs(t.auth.Origin, []string{req.URL.String()}, t.auth.AllowInsecureHTTP); err != nil {
		return errTargetAuthScope
	}
	if req.Host != "" {
		origin, err := NormalizeTargetOrigin(req.URL.Scheme+"://"+req.Host, t.auth.AllowInsecureHTTP)
		if err != nil || origin != t.auth.Origin {
			return errTargetAuthScope
		}
	}
	for name, values := range req.Header {
		if strings.EqualFold(name, "Host") {
			for _, value := range values {
				origin, err := NormalizeTargetOrigin(req.URL.Scheme+"://"+value, t.auth.AllowInsecureHTTP)
				if err != nil || origin != t.auth.Origin {
					return errTargetAuthScope
				}
			}
		}
		if strings.EqualFold(name, "Connection") || strings.EqualFold(name, "Upgrade") {
			return errTargetAuthScope
		}
	}
	return nil
}

func (t *targetAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.validateRequest(req); err != nil {
		return nil, err
	}
	transportRequest := req.Clone(req.Context())
	for name, value := range t.auth.Headers {
		for existing := range transportRequest.Header {
			if strings.EqualFold(existing, name) {
				delete(transportRequest.Header, existing)
			}
		}
		transportRequest.Header.Set(name, value)
	}
	resp, err := t.base.RoundTrip(transportRequest)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, errTargetAuthRequest
	}
	var data []byte
	if resp.StatusCode != http.StatusSwitchingProtocols {
		data, err = io.ReadAll(resp.Body)
	}
	closeErr := resp.Body.Close()
	if err != nil || closeErr != nil {
		return nil, errTargetAuthRequest
	}
	clean := *resp
	data = []byte(t.auth.Redact(string(data)))
	clean.Body = io.NopCloser(bytes.NewReader(data))
	clean.Header = t.redactHeaders(resp.Header)
	clean.Trailer = t.redactHeaders(resp.Trailer)
	clean.Status = t.auth.Redact(resp.Status)
	clean.ContentLength = int64(len(data))
	if clean.Header.Get("Content-Length") != "" {
		clean.Header.Set("Content-Length", strconv.Itoa(len(data)))
	}
	clean.Request = t.redactRequest(req)
	return &clean, nil
}

func (t *targetAuthTransport) redactHeaders(headers http.Header) http.Header {
	clean := make(http.Header, len(headers))
	for name, values := range headers {
		if strings.EqualFold(name, "Set-Cookie") || strings.EqualFold(name, "Set-Cookie2") {
			continue
		}
		for _, value := range values {
			clean.Add(t.auth.Redact(name), t.auth.Redact(value))
		}
	}
	return clean
}

func (t *targetAuthTransport) redactRequest(req *http.Request) *http.Request {
	clean := req.Clone(req.Context())
	clean.Header = t.redactHeaders(clean.Header)
	for name := range t.auth.Headers {
		for existing := range clean.Header {
			if strings.EqualFold(existing, name) {
				delete(clean.Header, existing)
			}
		}
	}
	clean.URL.Path = t.auth.Redact(clean.URL.Path)
	clean.URL.RawPath = ""
	clean.URL.RawQuery = t.auth.Redact(clean.URL.RawQuery)
	clean.Body, clean.GetBody = nil, nil
	clean.Form, clean.PostForm, clean.MultipartForm = nil, nil, nil
	clean.Response = nil
	clean.Trailer = t.redactHeaders(clean.Trailer)
	return clean
}

func (t *targetAuthTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
