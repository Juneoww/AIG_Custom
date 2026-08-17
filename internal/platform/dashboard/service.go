package dashboard

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
)

var ErrForbidden = errors.New("无权访问治理总览")

const dashboardDays = 30

type reportReader interface {
	Dashboard(context.Context, identity.Subject, time.Time, time.Time, int) (reports.DashboardProjection, error)
}

type taskReader interface {
	Recent(context.Context, identity.Subject, int) ([]tasks.TaskSummary, error)
}

type reportTaskVerifierSetter interface {
	SetDashboardTaskVerifier(reports.DashboardTaskVerifier)
}

type dashboardTaskStatusReader interface {
	DashboardTaskSucceeded(context.Context, string, string) (bool, error)
}

type Service struct {
	reports reportReader
	tasks   taskReader
	now     func() time.Time
}

func NewService(reportReader reportReader, taskReader taskReader) *Service {
	if setter, ok := reportReader.(reportTaskVerifierSetter); ok {
		if statusReader, ok := taskReader.(dashboardTaskStatusReader); ok {
			setter.SetDashboardTaskVerifier(statusReader.DashboardTaskSucceeded)
		}
	}
	return &Service{reports: reportReader, tasks: taskReader, now: func() time.Time { return time.Now().UTC() }}
}

func (service *Service) Get(ctx context.Context, subject identity.Subject) (View, error) {
	if service == nil || service.reports == nil || service.tasks == nil {
		return View{}, errors.New("治理总览服务未配置")
	}
	today := utcDay(service.now())
	from := today.AddDate(0, 0, -(dashboardDays - 1))
	to := today.AddDate(0, 0, 1)
	projection, err := service.reports.Dashboard(ctx, subject, from, to, 5)
	if err != nil {
		return View{}, mapScopeError(err)
	}
	recent, err := service.tasks.Recent(ctx, subject, 5)
	if err != nil {
		return View{}, mapScopeError(err)
	}
	view := View{
		MappingVersions: stableVersions(projection.MappingVersions),
		Risk: reports.RiskSummary{
			High: projection.Risk.High, Medium: projection.Risk.Medium, Low: projection.Risk.Low,
		},
		Trend:       completeTrend(from, projection.Trend),
		RecentTasks: nonNilTasks(recent),
		Attention:   attentionItems(projection.Attention),
	}
	if projection.SnapshotCount > 0 {
		score := roundedAverage(projection.ScoreSum, projection.SnapshotCount)
		view.HasData = true
		view.SecurityScore = &score
		view.Risk.Score = score
	}
	return view, nil
}

func mapScopeError(err error) error {
	if errors.Is(err, reports.ErrForbidden) || errors.Is(err, tasks.ErrForbidden) {
		return ErrForbidden
	}
	return err
}

func completeTrend(from time.Time, records []reports.DashboardTrendPoint) []TrendPoint {
	byDay := make(map[time.Time]reports.DashboardTrendPoint, len(records))
	for _, record := range records {
		day := utcDay(record.Date)
		current := byDay[day]
		current.Date = day
		current.Completed += record.Completed
		current.ScoreSum += record.ScoreSum
		current.High += record.High
		current.Medium += record.Medium
		current.Low += record.Low
		byDay[day] = current
	}
	trend := make([]TrendPoint, 0, dashboardDays)
	for day := from; len(trend) < dashboardDays; day = day.AddDate(0, 0, 1) {
		record := byDay[day]
		point := TrendPoint{Date: day, Completed: record.Completed, High: record.High, Medium: record.Medium, Low: record.Low}
		if record.Completed > 0 {
			score := roundedAverage(record.ScoreSum, record.Completed)
			point.SecurityScore = &score
		}
		trend = append(trend, point)
	}
	return trend
}

func attentionItems(records []reports.DashboardAttention) []AttentionItem {
	items := make([]AttentionItem, 0, len(records))
	for _, record := range records {
		items = append(items, AttentionItem{
			ReportID: record.ReportID, TaskID: record.TaskID, TaskType: record.TaskType,
			CompletedAt: record.CompletedAt.UTC(), Score: record.Score, ProductName: record.ProductName,
			Risk: reports.RiskSummary{High: record.High, Medium: record.Medium, Low: record.Low, Score: record.Score},
		})
	}
	return items
}

func stableVersions(input []string) []string {
	unique := make(map[string]struct{}, len(input))
	for _, version := range input {
		version = strings.TrimSpace(version)
		if version != "" {
			unique[version] = struct{}{}
		}
	}
	versions := make([]string, 0, len(unique))
	for version := range unique {
		versions = append(versions, version)
	}
	sort.Strings(versions)
	return versions
}

func nonNilTasks(input []tasks.TaskSummary) []tasks.TaskSummary {
	if input == nil {
		return []tasks.TaskSummary{}
	}
	return input
}

func roundedAverage(sum, count int) int {
	if count <= 0 || sum <= 0 {
		return 0
	}
	quotient, remainder := sum/count, sum%count
	threshold := count/2 + count%2
	if remainder >= threshold {
		quotient++
	}
	return quotient
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
