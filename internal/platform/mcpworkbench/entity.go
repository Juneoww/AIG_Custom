package mcpworkbench

import (
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
)

// Metrics are the four fixed MCP workbench counters. Each uses the shared
// thirty UTC-day window selected by Service.
type Metrics struct {
	Running      int `json:"running"`
	Pending      int `json:"pending"`
	HighRisk     int `json:"high_risk"`
	Completed30d int `json:"completed_30d"`
}

// ActiveTask is the browser-safe representation of an in-progress MCP task.
// Phase remains nil until the platform has a trusted stage-event source.
type ActiveTask struct {
	TaskID     string       `json:"task_id"`
	Label      string       `json:"label"`
	SourceKind string       `json:"source_kind"`
	Phase      *string      `json:"phase"`
	Status     tasks.Status `json:"status"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

// RecentRisk is an immutable safe report highlight. It intentionally repeats
// only the report-domain projection fields allowed to cross the browser
// boundary.
type RecentRisk struct {
	ReportID    string    `json:"report_id"`
	TaskID      string    `json:"task_id"`
	Severity    string    `json:"severity"`
	Category    string    `json:"category"`
	Summary     string    `json:"summary"`
	CompletedAt time.Time `json:"completed_at"`
}

// View is the complete, deliberately small response for GET /mcp-workbench.
type View struct {
	Metrics     Metrics      `json:"metrics"`
	ActiveTasks []ActiveTask `json:"active_tasks"`
	RecentRisks []RecentRisk `json:"recent_risks"`
}
