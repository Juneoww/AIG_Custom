package websocket

import (
	"errors"
	"net/http"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
)

var errResourceAccessDenied = errors.New("无权限访问资源")

func requestSubject(c *gin.Context) (identity.Subject, bool) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
	}
	return subject, ok
}

func authorizeResource(subject identity.Subject, owner string, write bool) error {
	if !identity.CanAccessOwnerOrRole(subject, owner, write) {
		return errResourceAccessDenied
	}
	return nil
}

func respondResourceAccessDenied(c *gin.Context, err error) bool {
	if !errors.Is(err, errResourceAccessDenied) {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{"status": 1, "message": "无权限访问", "data": nil})
	return true
}
