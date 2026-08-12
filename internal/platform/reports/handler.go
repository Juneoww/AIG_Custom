package reports

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
)

type Handler struct{ service *Service }

const (
	defaultReportPageSize = 20
	maxReportPageSize     = 100
	maxReportPage         = 1000
	maxBackfillTaskIDLen  = 128
)

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (handler *Handler) Register(group *gin.RouterGroup) {
	group.GET("/reports", handler.list)
	group.GET("/reports/trends", handler.trend)
	group.GET("/reports/:reportID", handler.get)
	group.POST("/reports/:reportID/exports/pdf", handler.exportPDF)
	group.POST("/admin/reports/backfill", handler.backfill)
}

func (handler *Handler) list(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	page, pageSize, err := reportPage(c)
	if err != nil {
		respondError(c, ErrInvalidSnapshot)
		return
	}
	snapshots, err := handler.service.List(c.Request.Context(), subject, page, pageSize)
	if err != nil {
		respondError(c, err)
		return
	}
	responses := make([]ReportSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		responses = append(responses, summaryOf(&snapshot))
	}
	c.JSON(http.StatusOK, responses)
}

func (handler *Handler) trend(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	days := 30
	if raw := c.Query("days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 30 {
			respondError(c, ErrInvalidSnapshot)
			return
		}
		days = value
	}
	points, err := handler.service.Trend(c.Request.Context(), subject, days, time.Now().UTC())
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, points)
}

func (handler *Handler) get(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	snapshot, err := handler.service.Get(c.Request.Context(), subject, c.Param("reportID"))
	if err != nil {
		respondError(c, err)
		return
	}
	detail, err := detailOf(snapshot)
	if err != nil {
		respondError(c, ErrInvalidSnapshot)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (handler *Handler) exportPDF(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	pdf, err := handler.service.ExportPDF(c.Request.Context(), subject, c.Param("reportID"))
	if err != nil {
		respondError(c, err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename=report.pdf")
	c.Data(http.StatusOK, "application/pdf", pdf)
}

func (handler *Handler) backfill(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	var request struct {
		TaskID string `json:"task_id"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
	if c.ShouldBindJSON(&request) != nil || strings.TrimSpace(request.TaskID) == "" || len(request.TaskID) > maxBackfillTaskIDLen {
		respondError(c, ErrInvalidSnapshot)
		return
	}
	snapshot, err := handler.service.Backfill(c.Request.Context(), subject, strings.TrimSpace(request.TaskID))
	if err != nil {
		respondError(c, err)
		return
	}
	detail, err := detailOf(snapshot)
	if err != nil {
		respondError(c, ErrInvalidSnapshot)
		return
	}
	c.JSON(http.StatusCreated, detail)
}

func reportPage(c *gin.Context) (int, int, error) {
	page, pageSize := 1, defaultReportPageSize
	if raw := c.Query("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxReportPage {
			return 0, 0, ErrInvalidSnapshot
		}
		page = value
	}
	if raw := c.Query("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return 0, 0, ErrInvalidSnapshot
		}
		if value > maxReportPageSize {
			value = maxReportPageSize
		}
		pageSize = value
	}
	return page, pageSize, nil
}

func summaryOf(snapshot *Snapshot) ReportSummary {
	return ReportSummary{ID: snapshot.ID, TaskID: snapshot.TaskID, TaskType: snapshot.TaskType, CompletedAt: snapshot.CompletedAt, CreatedAt: snapshot.CreatedAt, Risk: snapshot.Risk, BrandProductName: snapshot.Brand.ProductName}
}

func detailOf(snapshot *Snapshot) (ReportDetail, error) {
	var render RenderModel
	if snapshot == nil || json.Unmarshal(snapshot.RenderData, &render) != nil || render.RenderVersion == "" {
		return ReportDetail{}, ErrInvalidSnapshot
	}
	return ReportDetail{ID: snapshot.ID, TaskID: snapshot.TaskID, TaskType: snapshot.TaskType, CompletedAt: snapshot.CompletedAt, CreatedAt: snapshot.CreatedAt, Risk: snapshot.Risk, Render: render}, nil
}

func respondError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	case errors.Is(err, ErrInvalidSnapshot):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid report"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "report request failed"})
	}
}
