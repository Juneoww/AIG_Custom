package identity

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const subjectContextKey = "identity_subject"

var ErrInsecureCookieOutsideTest = &configurationError{message: "ALLOW_INSECURE_TEST_COOKIE 只能在 APP_ENV=test 使用"}

type configurationError struct{ message string }

func (e *configurationError) Error() string { return e.message }

func Authenticate(service *Service, policy CookiePolicy) gin.HandlerFunc {
	policy = policy.normalized()
	return func(c *gin.Context) {
		cookie, err := c.Request.Cookie(policy.SessionCookieName)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		subject, err := service.SubjectForToken(c.Request.Context(), cookie.Value)
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(subjectContextKey, subject)
		c.Set("username", subject.Username)
		c.Next()
	}
}
func CurrentSubject(c *gin.Context) (Subject, bool) {
	value, ok := c.Get(subjectContextKey)
	if !ok {
		return Subject{}, false
	}
	subject, ok := value.(Subject)
	return subject, ok
}
func RequireCSRF(policy CookiePolicy) gin.HandlerFunc {
	policy = policy.normalized()
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		cookie, err := c.Request.Cookie(policy.CSRFCookieName)
		header := c.GetHeader("X-CSRF-Token")
		if err != nil || cookie.Value == "" || header == "" || cookie.Value != header {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
func RequireRole(roles ...Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := CurrentSubject(c)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if !HasAnyRole(subject, roles...) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
func RequireOwnerOrRole(owner func(*gin.Context) string, write bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := CurrentSubject(c)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if !CanAccessOwnerOrRole(subject, owner(c), write) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
func RequirePasswordChangeCompleted() gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := CurrentSubject(c)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		if subject.MustChangePassword {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}
func HasAnyRole(subject Subject, roles ...Role) bool {
	for _, role := range roles {
		if subject.Role == role {
			return true
		}
	}
	return false
}
func CanAccessOwnerOrRole(subject Subject, ownerID string, write bool) bool {
	if subject.Role == RoleAdmin {
		return true
	}
	if subject.Role == RoleAuditor {
		return !write
	}
	return subject.Role == RoleUser && subject.UserID == strings.TrimSpace(ownerID)
}

func RegisterRoutes(group *gin.RouterGroup, service *Service, policy CookiePolicy) {
	policy = policy.normalized()
	group.POST("/login", func(c *gin.Context) {
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		login, err := service.Authenticate(c.Request.Context(), input.Username, input.Password)
		if err != nil {
			c.Status(http.StatusUnauthorized)
			return
		}
		csrfToken, err := randomToken()
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		http.SetCookie(c.Writer, policy.SessionCookie(login.Token))
		http.SetCookie(c.Writer, policy.CSRFCookie(csrfToken))
		c.JSON(http.StatusOK, gin.H{"must_change_password": login.MustChangePassword})
	})
	protected := group.Group("")
	protected.Use(Authenticate(service, policy))
	protected.POST("/logout", RequireCSRF(policy), func(c *gin.Context) {
		cookie, _ := c.Request.Cookie(policy.SessionCookieName)
		_ = service.RevokeSession(c.Request.Context(), cookie.Value)
		http.SetCookie(c.Writer, policy.ClearSessionCookie())
		http.SetCookie(c.Writer, policy.ClearCSRFCookie())
		c.Status(http.StatusNoContent)
	})
	protected.POST("/change-password", RequireCSRF(policy), func(c *gin.Context) {
		var input struct {
			OldPassword string `json:"old_password"`
			NewPassword string `json:"new_password"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		subject, _ := CurrentSubject(c)
		if err := service.ChangePassword(c.Request.Context(), subject.UserID, input.OldPassword, input.NewPassword); err != nil {
			c.Status(http.StatusUnauthorized)
			return
		}
		http.SetCookie(c.Writer, policy.ClearSessionCookie())
		c.Status(http.StatusNoContent)
	})
}
