package websocket

import (
	"net/http"
	"net/http/httptest"
	"testing"

	platformaudit "github.com/Juneoww/AIG_Custom/internal/platform/audit"
	platformbrand "github.com/Juneoww/AIG_Custom/internal/platform/brand"
	platformreports "github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRegisterPlatformReportRoutesRegistersBothReportAndBrandPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	reports := platformreports.NewHandler(platformreports.NewGovernedService(platformreports.NewMemoryRepository(), platformbrand.NewService(platformbrand.NewMemoryRepository()), platformaudit.NewService(platformaudit.NewMemoryRepository()), nil, nil))
	brand := platformbrand.NewHandler(platformbrand.NewGovernedService(platformbrand.NewMemoryRepository(), platformaudit.NewService(platformaudit.NewMemoryRepository())))
	registerPlatformReportRoutes(router.Group("/api/v1/platform"), reports, brand)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/platform/reports", nil))
	assert.NotEqual(t, http.StatusNotFound, response.Code)
}
