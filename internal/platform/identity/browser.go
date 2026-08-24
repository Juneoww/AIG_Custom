package identity

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type CurrentSubjectResponse struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	Role               Role   `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
}

type CSRFResponse struct {
	Token string `json:"csrf_token"`
}

func registerBrowserRoutes(group *gin.RouterGroup, service *Service, policy CookiePolicy) {
	group.GET("/csrf", func(c *gin.Context) {
		token, err := randomToken()
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		http.SetCookie(c.Writer, policy.CSRFCookie(token))
		c.JSON(http.StatusOK, CSRFResponse{Token: token})
	})

	group.GET("/me", Authenticate(service, policy), func(c *gin.Context) {
		subject, _ := CurrentSubject(c)
		c.JSON(http.StatusOK, CurrentSubjectResponse{
			ID:                 subject.UserID,
			Username:           subject.Username,
			Role:               subject.Role,
			MustChangePassword: subject.MustChangePassword,
		})
	})
}
