package brand

import (
	"errors"
	"net/http"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
)

const maxBrandRequestBytes = 2 << 20

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (handler *Handler) Register(group *gin.RouterGroup) {
	group.GET("/brand", handler.get)
	group.PUT("/brand", handler.update)
}

func (handler *Handler) get(c *gin.Context) {
	config, err := handler.service.Get(c.Request.Context())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, config)
}

func (handler *Handler) update(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBrandRequestBytes)
	var config Config
	if c.ShouldBindJSON(&config) != nil {
		respondError(c, ErrInvalid)
		return
	}
	updated, err := handler.service.Update(c.Request.Context(), subject, config)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	case errors.Is(err, ErrInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid brand"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "brand request failed"})
	}
}
