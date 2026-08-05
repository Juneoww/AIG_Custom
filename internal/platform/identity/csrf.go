package identity

import (
	"net"
	"net/http"
	"os"
	"strings"
)

type CookieConfig struct {
	AppEnv, TrustedProxyCIDRs string
	AllowInsecureTestCookie   bool
}
type CookiePolicy struct {
	SessionCookieName, CSRFCookieName string
	Secure, HTTPOnly                  bool
	SameSite                          http.SameSite
	trustedProxies                    []*net.IPNet
}

func NewCookiePolicy(config CookieConfig) (CookiePolicy, error) {
	appEnv := strings.ToLower(strings.TrimSpace(config.AppEnv))
	if appEnv == "" {
		appEnv = "production"
	}
	if config.AllowInsecureTestCookie && appEnv != "test" {
		return CookiePolicy{}, ErrInsecureCookieOutsideTest
	}
	policy := CookiePolicy{SessionCookieName: "aig_session", CSRFCookieName: "aig_csrf", Secure: !config.AllowInsecureTestCookie, HTTPOnly: true, SameSite: http.SameSiteLaxMode}
	for _, cidr := range strings.Split(config.TrustedProxyCIDRs, ",") {
		if strings.TrimSpace(cidr) == "" {
			continue
		}
		_, parsed, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			return CookiePolicy{}, err
		}
		policy.trustedProxies = append(policy.trustedProxies, parsed)
	}
	return policy, nil
}
func CookiePolicyFromEnv() (CookiePolicy, error) {
	return NewCookiePolicy(CookieConfig{AppEnv: os.Getenv("APP_ENV"), TrustedProxyCIDRs: os.Getenv("TRUSTED_PROXY_CIDRS"), AllowInsecureTestCookie: strings.EqualFold(os.Getenv("ALLOW_INSECURE_TEST_COOKIE"), "true")})
}
func (p CookiePolicy) normalized() CookiePolicy {
	if p.SessionCookieName == "" {
		p.SessionCookieName = "aig_session"
	}
	if p.CSRFCookieName == "" {
		p.CSRFCookieName = "aig_csrf"
	}
	if p.SameSite == 0 {
		p.SameSite = http.SameSiteLaxMode
	}
	return p
}
func (p CookiePolicy) RequestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	for _, proxy := range p.trustedProxies {
		if proxy.Contains(ip) {
			return strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https")
		}
	}
	return false
}
func (p CookiePolicy) SessionCookie(token string) *http.Cookie {
	p = p.normalized()
	return &http.Cookie{Name: p.SessionCookieName, Value: token, Path: "/", HttpOnly: p.HTTPOnly, Secure: p.Secure, SameSite: p.SameSite}
}
func (p CookiePolicy) CSRFCookie(token string) *http.Cookie {
	p = p.normalized()
	return &http.Cookie{Name: p.CSRFCookieName, Value: token, Path: "/", HttpOnly: false, Secure: p.Secure, SameSite: p.SameSite}
}
func (p CookiePolicy) ClearSessionCookie() *http.Cookie {
	p = p.normalized()
	return &http.Cookie{Name: p.SessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: p.HTTPOnly, Secure: p.Secure, SameSite: p.SameSite}
}
func (p CookiePolicy) ClearCSRFCookie() *http.Cookie {
	p = p.normalized()
	return &http.Cookie{Name: p.CSRFCookieName, Value: "", Path: "/", MaxAge: -1, Secure: p.Secure, SameSite: p.SameSite}
}
