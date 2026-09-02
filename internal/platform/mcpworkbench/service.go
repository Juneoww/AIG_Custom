package mcpworkbench

import (
	"context"
	"errors"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
)

var ErrForbidden = errors.New("无权访问 MCP 安全扫描工作台")

const (
	maxActiveTasks = 10
	maxRecentRisks = 5
)

type reportReader interface {
	MCPWorkbench(context.Context, identity.Subject, time.Time) (reports.MCPWorkbenchProjection, error)
}

type taskReader interface {
	MCPWorkbench(context.Context, identity.Subject, time.Time) (tasks.MCPWorkbenchProjection, error)
}

// Service composes two already-safe domain read models. It does not read task
// bodies, stored params, attachments, raw reports, or render artifacts.
type Service struct {
	reports reportReader
	tasks   taskReader
	now     func() time.Time
}

func NewService(reportReader reportReader, taskReader taskReader) *Service {
	return &Service{reports: reportReader, tasks: taskReader, now: func() time.Time { return time.Now().UTC() }}
}

func (service *Service) Get(ctx context.Context, subject identity.Subject) (View, error) {
	if service == nil || service.reports == nil || service.tasks == nil {
		return View{}, errors.New("MCP 工作台服务未配置")
	}
	now := service.now()
	taskProjection, err := service.tasks.MCPWorkbench(ctx, subject, now)
	if err != nil {
		return View{}, mapScopeError(err)
	}
	reportProjection, err := service.reports.MCPWorkbench(ctx, subject, now)
	if err != nil {
		return View{}, mapScopeError(err)
	}
	return View{
		Metrics: Metrics{
			Running:      nonNegative(taskProjection.Running),
			Pending:      nonNegative(taskProjection.Pending),
			HighRisk:     nonNegative(reportProjection.HighRisk),
			Completed30d: nonNegative(reportProjection.Completed30d),
		},
		ActiveTasks: activeTasks(taskProjection.ActiveTasks),
		RecentRisks: recentRisks(reportProjection.Highlights),
	}, nil
}

func mapScopeError(err error) error {
	if errors.Is(err, tasks.ErrForbidden) || errors.Is(err, reports.ErrForbidden) {
		return ErrForbidden
	}
	return err
}

func activeTasks(input []tasks.MCPWorkbenchTask) []ActiveTask {
	items := make([]ActiveTask, 0, min(len(input), maxActiveTasks))
	for _, task := range input {
		if len(items) == maxActiveTasks {
			break
		}
		items = append(items, ActiveTask{
			TaskID: task.TaskID, Label: "MCP 扫描 · " + opaquePrefix(task.TaskID), SourceKind: safeSourceKind(task.SourceKind),
			Phase: nil, Status: task.Status, UpdatedAt: task.UpdatedAt.UTC(),
		})
	}
	return items
}

func recentRisks(input []reports.MCPRiskHighlight) []RecentRisk {
	items := make([]RecentRisk, 0, min(len(input), maxRecentRisks))
	for _, highlight := range input {
		if len(items) == maxRecentRisks {
			break
		}
		if !safeRiskSeverity(highlight.Severity) || !safeRiskCategory(highlight.Category) {
			continue
		}
		items = append(items, RecentRisk{
			ReportID: highlight.ReportID, TaskID: highlight.TaskID, Severity: highlight.Severity, Category: highlight.Category,
			Summary: highlight.Summary, CompletedAt: highlight.CompletedAt.UTC(),
		})
	}
	return items
}

func safeSourceKind(value string) string {
	if value == "repository" || value == "service" {
		return value
	}
	return "legacy_unknown"
}

func safeRiskSeverity(value string) bool {
	return value == "high" || value == "medium" || value == "low"
}

func safeRiskCategory(value string) bool {
	switch value {
	case "dangerous_tool", "command_file", "authorization", "data_leakage", "tool_poisoning", "skill_mismatch", "other":
		return true
	default:
		return false
	}
}

func opaquePrefix(value string) string {
	if len(value) <= 8 {
		return value
	}
	return value[:8]
}

func nonNegative(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
