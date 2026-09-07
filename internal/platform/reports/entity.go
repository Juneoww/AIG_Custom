package reports

import (
	"encoding/json"
	"time"

	"github.com/Juneoww/AIG_Custom/common/portscan"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
)

type Snapshot struct {
	ID          string          `json:"-"`
	TaskID      string          `json:"-"`
	OwnerUserID string          `json:"-"`
	TaskType    string          `json:"-"`
	CompletedAt time.Time       `json:"-"`
	CreatedAt   time.Time       `json:"-"`
	RawResult   json.RawMessage `json:"-"`
	Risk        RiskSummary     `json:"-"`
	RenderData  json.RawMessage `json:"-"`
	Brand       brand.Config    `json:"-"`
}

// ReportSummary is the intentionally small list wire model. Stored snapshot
// source data and the immutable brand artifact remain server-side only.
type ReportSummary struct {
	ID               string      `json:"id"`
	TaskID           string      `json:"task_id"`
	TaskType         string      `json:"task_type"`
	CompletedAt      time.Time   `json:"completed_at"`
	CreatedAt        time.Time   `json:"created_at"`
	Risk             RiskSummary `json:"risk"`
	BrandProductName string      `json:"brand_product_name"`
}

type ReportListResponse struct {
	Items    []ReportSummary `json:"items"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// ReportDetail is the online immutable presentation contract. Raw engine
// results, stored render JSON and private brand fields are never wire values.
type ReportDetail struct {
	ID          string      `json:"id"`
	TaskID      string      `json:"task_id"`
	TaskType    string      `json:"task_type"`
	CompletedAt time.Time   `json:"completed_at"`
	CreatedAt   time.Time   `json:"created_at"`
	Risk        RiskSummary `json:"risk"`
	Render      RenderModel `json:"render"`
}

type RenderModel struct {
	RenderVersion     string             `json:"render_version"`
	MappingVersion    string             `json:"mapping_version"`
	GeneratedAt       time.Time          `json:"generated_at"`
	CompletedAt       time.Time          `json:"completed_at"`
	TaskID            string             `json:"task_id"`
	TaskType          string             `json:"task_type"`
	PortScanMode      string             `json:"port_scan_mode,omitempty"`
	PortSpec          string             `json:"port_spec,omitempty"`
	ProductName       string             `json:"product_name"`
	PrimaryColor      string             `json:"primary_color"`
	Watermark         string             `json:"watermark"`
	Risk              RiskSummary        `json:"risk"`
	ScoreExplanation  string             `json:"score_explanation"`
	RiskTrend         []TrendPoint       `json:"risk_trend"`
	RiskDistribution  RiskDistribution   `json:"risk_distribution"`
	TopRisks          []TopRisk          `json:"top_risks"`
	TechnicalFindings []TechnicalFinding `json:"technical_findings"`
	Recommendations   []string           `json:"recommendations"`
	Coverage          string             `json:"coverage"`
	Conclusion        string             `json:"conclusion"`
}

type RiskDistribution struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}
type TopRisk struct {
	Severity    string `json:"severity"`
	Count       int    `json:"count"`
	Impact      string `json:"impact"`
	Remediation string `json:"remediation"`
}
type TechnicalFinding struct {
	Title       string `json:"title"`
	Evidence    string `json:"evidence"`
	Impact      string `json:"impact"`
	Remediation string `json:"remediation"`
	Category    string `json:"category,omitempty"`
	Severity    string `json:"severity,omitempty"`
}

// MCPWorkbenchProjection is the intentionally narrow report read model used
// by the MCP workbench. It must never grow into a report or render-data DTO.
type MCPWorkbenchProjection struct {
	Completed30d int                `json:"completed_30d"`
	HighRisk     int                `json:"high_risk"`
	Highlights   []MCPRiskHighlight `json:"highlights"`
}

// MCPRiskHighlight contains only server-derived, safe-to-display MCP risk
// metadata. It deliberately excludes raw scan output and render artifacts.
type MCPRiskHighlight struct {
	ReportID    string    `json:"report_id"`
	TaskID      string    `json:"task_id"`
	Severity    string    `json:"severity"`
	Category    string    `json:"category"`
	Summary     string    `json:"summary"`
	CompletedAt time.Time `json:"completed_at"`
}

type TrendQuery struct {
	Now         time.Time
	Days        int
	OwnerUserID string
}

type ListQuery struct {
	OwnerUserID string
	Limit       int
	Offset      int
}

type TrendPoint struct {
	Date      time.Time `json:"date"`
	Completed int       `json:"completed"`
	High      int       `json:"high"`
	Medium    int       `json:"medium"`
	Low       int       `json:"low"`
}

// trustedInfrastructurePortScan accepts exactly the two platform-owned mode
// and TCP-port combinations. It returns only the approved port expression.
func trustedInfrastructurePortScan(taskType string, mode portscan.Mode, spec string) (portscan.Mode, string, bool) {
	if taskType != "ai_infra_scan" {
		return "", "", false
	}
	expected := portscan.PortSpec(mode)
	if expected == "" || spec != expected {
		return "", "", false
	}
	return mode, expected, true
}

func sanitizeRenderInfrastructurePortScan(taskType string, render *RenderModel) {
	if render == nil {
		return
	}
	if render.TaskType != taskType {
		render.PortScanMode = ""
		render.PortSpec = ""
		return
	}
	mode, spec, valid := trustedInfrastructurePortScan(taskType, portscan.Mode(render.PortScanMode), render.PortSpec)
	if !valid {
		render.PortScanMode = ""
		render.PortSpec = ""
		return
	}
	render.PortScanMode = string(mode)
	render.PortSpec = spec
}
