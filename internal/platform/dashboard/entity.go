package dashboard

import (
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
)

type View struct {
	HasData         bool                `json:"has_data"`
	SecurityScore   *int                `json:"security_score"`
	MappingVersions []string            `json:"mapping_versions"`
	Risk            reports.RiskSummary `json:"risk"`
	Trend           []TrendPoint        `json:"trend"`
	RecentTasks     []tasks.TaskSummary `json:"recent_tasks"`
	Attention       []AttentionItem     `json:"attention"`
}

type TrendPoint struct {
	Date          time.Time `json:"date"`
	Completed     int       `json:"completed"`
	SecurityScore *int      `json:"security_score"`
	High          int       `json:"high"`
	Medium        int       `json:"medium"`
	Low           int       `json:"low"`
}

type AttentionItem struct {
	ReportID    string              `json:"report_id"`
	TaskID      string              `json:"task_id"`
	TaskType    string              `json:"task_type"`
	CompletedAt time.Time           `json:"completed_at"`
	Score       int                 `json:"score"`
	Risk        reports.RiskSummary `json:"risk"`
	ProductName string              `json:"product_name"`
}
