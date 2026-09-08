package websocket

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"
	"github.com/gin-gonic/gin"
)

func registerModelProbeRoutes(group *gin.RouterGroup, service *platformmodels.Service) {
	handler := func(c *gin.Context) {
		subject, ok := identity.CurrentSubject(c)
		if !ok {
			c.Status(http.StatusUnauthorized)
			return
		}
		if subject.Role != identity.RoleAdmin && subject.Role != identity.RoleUser {
			c.Status(http.StatusForbidden)
			return
		}
		if len(c.Request.URL.RawQuery) > 0 {
			c.Status(http.StatusBadRequest)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 16385))
		if err != nil || len(raw) > 16384 {
			c.Status(http.StatusBadRequest)
			return
		}
		var input platformmodels.ProbeInput
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if decoder.Decode(&input) != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			c.Status(http.StatusBadRequest)
			return
		}
		result, err := service.Probe(c.Request.Context(), subject, c.Param("modelID"), input)
		if errors.Is(err, platformmodels.ErrProbeBusy) {
			c.Header("Retry-After", "5")
			c.JSON(http.StatusTooManyRequests, platformmodels.ProbeResult{Status: "error", Code: "busy", Message: "busy"})
			return
		}
		if err != nil {
			respondPlatformModelError(c, err)
			return
		}
		c.JSON(http.StatusOK, result)
	}
	group.POST("/test", handler)
	group.POST("/:modelID/test", handler)
}
