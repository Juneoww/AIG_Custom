package dashboard

import (
	"errors"
	"net/http"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (handler *Handler) Register(group *gin.RouterGroup) {
	group.GET("/dashboard", handler.get)
}

func (handler *Handler) get(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	view, err := handler.service.Get(c.Request.Context(), subject)
	if errors.Is(err, ErrForbidden) {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "dashboard request failed"})
		return
	}
	c.JSON(http.StatusOK, view)
}
