package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound        = errors.New("报告快照不存在")
	ErrSnapshotExists  = errors.New("任务报告快照已存在")
	ErrInvalidSnapshot = errors.New("报告快照无效")
	ErrForbidden       = errors.New("无权访问报告")
)

type Repository interface {
	Create(context.Context, *Snapshot) error
	Get(context.Context, string) (*Snapshot, error)
	GetByTaskID(context.Context, string) (*Snapshot, error)
	List(context.Context, ListQuery) ([]Snapshot, error)
	Trend(context.Context, TrendQuery) ([]TrendPoint, error)
}

type MemoryRepository struct {
	mu       sync.RWMutex
	byID     map[string]*Snapshot
	byTaskID map[string]string
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) TransactionDB() *gorm.DB { return repository.db }

func (repository *GormRepository) Init() error {
	if repository == nil || repository.db == nil || !repository.db.Migrator().HasTable("report_snapshots") {
		return errors.New("报告数据库尚未迁移，请先运行 aig migrate")
	}
	return nil
}

func (repository *GormRepository) Create(ctx context.Context, snapshot *Snapshot) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	record, err := snapshotRecordOf(snapshot)
	if err != nil {
		return ErrInvalidSnapshot
	}
	result := txcontext.Gorm(ctx, repository.db).
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "task_id"}}, DoNothing: true}).
		Create(&record)
	if result.Error != nil {
		return fmt.Errorf("保存报告快照失败: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrSnapshotExists
	}
	return nil
}

func (repository *GormRepository) Get(ctx context.Context, id string) (*Snapshot, error) {
	var record snapshotRecord
	if err := txcontext.Gorm(ctx, repository.db).Where("id = ?", id).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return record.snapshot()
}

func (repository *GormRepository) GetVisible(ctx context.Context, id, ownerUserID string) (*Snapshot, error) {
	var record snapshotRecord
	query := txcontext.Gorm(ctx, repository.db).Where("id = ?", id)
	if ownerUserID != "" {
		query = query.Where("owner_user_id = ?", ownerUserID)
	}
	if err := query.First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return record.snapshot()
}

func (repository *GormRepository) GetByTaskID(ctx context.Context, taskID string) (*Snapshot, error) {
	var record snapshotRecord
	if err := txcontext.Gorm(ctx, repository.db).Where("task_id = ?", taskID).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return record.snapshot()
}

func (repository *GormRepository) List(ctx context.Context, query ListQuery) ([]Snapshot, error) {
	queryDB := txcontext.Gorm(ctx, repository.db).Table("report_snapshots").
		Select("id, task_id, task_type, completed_at, created_at, risk_summary, COALESCE(brand_snapshot ->> 'product_name', '') AS brand_product_name").
		Order("created_at DESC, id DESC")
	if query.OwnerUserID != "" {
		queryDB = queryDB.Where("owner_user_id = ?", query.OwnerUserID)
	}
	if query.Limit > 0 {
		queryDB = queryDB.Limit(query.Limit)
	}
	if query.Offset > 0 {
		queryDB = queryDB.Offset(query.Offset)
	}
	var records []summaryRecord
	if err := queryDB.Find(&records).Error; err != nil {
		return nil, err
	}
	snapshots := make([]Snapshot, 0, len(records))
	for _, record := range records {
		var risk RiskSummary
		if json.Unmarshal(record.RiskSummary, &risk) != nil {
			return nil, ErrInvalidSnapshot
		}
		snapshots = append(snapshots, Snapshot{
			ID: record.ID, TaskID: record.TaskID, TaskType: record.TaskType,
			CompletedAt: record.CompletedAt, CreatedAt: record.CreatedAt, Risk: risk,
			Brand: brand.Config{ProductName: record.BrandProductName},
		})
	}
	return snapshots, nil
}

func (repository *GormRepository) Trend(ctx context.Context, query TrendQuery) ([]TrendPoint, error) {
	if query.Now.IsZero() || query.Days < 0 {
		return nil, ErrInvalidSnapshot
	}
	query.Now = query.Now.UTC()
	lowerBound := utcDay(query.Now).AddDate(0, 0, -(query.Days - 1))
	upperBound := utcDay(query.Now).AddDate(0, 0, 1)
	queryDB := txcontext.Gorm(ctx, repository.db).Table("report_snapshots").
		Select(`date_trunc('day', completed_at AT TIME ZONE 'UTC') AS date,
			COUNT(*) AS completed,
			COALESCE(SUM((risk_summary ->> 'high')::integer), 0) AS high,
			COALESCE(SUM((risk_summary ->> 'medium')::integer), 0) AS medium,
			COALESCE(SUM((risk_summary ->> 'low')::integer), 0) AS low`).
		Where("completed_at >= ? AND completed_at < ?", lowerBound, upperBound)
	if query.OwnerUserID != "" {
		queryDB = queryDB.Where("owner_user_id = ?", query.OwnerUserID)
	}
	var records []TrendPoint
	if err := queryDB.Group("date_trunc('day', completed_at AT TIME ZONE 'UTC')").Order("date ASC").Scan(&records).Error; err != nil {
		return nil, err
	}
	return completeTrend(records, query), nil
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{byID: map[string]*Snapshot{}, byTaskID: map[string]string{}}
}

func (repository *MemoryRepository) Create(_ context.Context, snapshot *Snapshot) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.byTaskID[snapshot.TaskID]; exists {
		return ErrSnapshotExists
	}
	repository.byID[snapshot.ID] = cloneSnapshot(snapshot)
	repository.byTaskID[snapshot.TaskID] = snapshot.ID
	return nil
}

func (repository *MemoryRepository) Get(_ context.Context, id string) (*Snapshot, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	snapshot, exists := repository.byID[id]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneSnapshot(snapshot), nil
}

func (repository *MemoryRepository) GetVisible(_ context.Context, id, ownerUserID string) (*Snapshot, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	snapshot, exists := repository.byID[id]
	if !exists || ownerUserID != "" && snapshot.OwnerUserID != ownerUserID {
		return nil, ErrNotFound
	}
	return cloneSnapshot(snapshot), nil
}

func (repository *MemoryRepository) GetByTaskID(_ context.Context, taskID string) (*Snapshot, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	id, exists := repository.byTaskID[taskID]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneSnapshot(repository.byID[id]), nil
}

func (repository *MemoryRepository) List(_ context.Context, query ListQuery) ([]Snapshot, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	snapshots := make([]Snapshot, 0, len(repository.byID))
	for _, snapshot := range repository.byID {
		if query.OwnerUserID != "" && snapshot.OwnerUserID != query.OwnerUserID {
			continue
		}
		snapshots = append(snapshots, *cloneSnapshot(snapshot))
	}
	sort.Slice(snapshots, func(left, right int) bool {
		if snapshots[left].CreatedAt.Equal(snapshots[right].CreatedAt) {
			return snapshots[left].ID > snapshots[right].ID
		}
		return snapshots[left].CreatedAt.After(snapshots[right].CreatedAt)
	})
	if query.Offset >= len(snapshots) {
		return []Snapshot{}, nil
	}
	if query.Offset > 0 {
		snapshots = snapshots[query.Offset:]
	}
	if query.Limit > 0 && len(snapshots) > query.Limit {
		snapshots = snapshots[:query.Limit]
	}
	return snapshots, nil
}

func (repository *MemoryRepository) Trend(_ context.Context, query TrendQuery) ([]TrendPoint, error) {
	if query.Now.IsZero() || query.Days < 0 {
		return nil, ErrInvalidSnapshot
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	return trendOfSnapshots(repository.byID, query)
}

func trendOfSnapshots(snapshots map[string]*Snapshot, query TrendQuery) ([]TrendPoint, error) {
	query.Now = query.Now.UTC()
	lowerBound := utcDay(query.Now).AddDate(0, 0, -(query.Days - 1))
	upperBound := utcDay(query.Now).AddDate(0, 0, 1)
	points := map[time.Time]TrendPoint{}
	for _, snapshot := range snapshots {
		if snapshot.CompletedAt.Before(lowerBound) || !snapshot.CompletedAt.Before(upperBound) ||
			query.OwnerUserID != "" && snapshot.OwnerUserID != query.OwnerUserID {
			continue
		}
		date := time.Date(snapshot.CompletedAt.UTC().Year(), snapshot.CompletedAt.UTC().Month(), snapshot.CompletedAt.UTC().Day(), 0, 0, 0, 0, time.UTC)
		point := points[date]
		point.Date = date
		point.Completed++
		point.High += snapshot.Risk.High
		point.Medium += snapshot.Risk.Medium
		point.Low += snapshot.Risk.Low
		points[date] = point
	}
	trend := make([]TrendPoint, 0, query.Days)
	for day := lowerBound; day.Before(upperBound); day = day.AddDate(0, 0, 1) {
		point := points[day]
		point.Date = day
		trend = append(trend, point)
	}
	return trend, nil
}

func completeTrend(records []TrendPoint, query TrendQuery) []TrendPoint {
	query.Now = query.Now.UTC()
	lowerBound := utcDay(query.Now).AddDate(0, 0, -(query.Days - 1))
	upperBound := utcDay(query.Now).AddDate(0, 0, 1)
	points := make(map[time.Time]TrendPoint, len(records))
	for _, record := range records {
		day := utcDay(record.Date)
		record.Date = day
		points[day] = record
	}
	trend := make([]TrendPoint, 0, query.Days)
	for day := lowerBound; day.Before(upperBound); day = day.AddDate(0, 0, 1) {
		point := points[day]
		point.Date = day
		trend = append(trend, point)
	}
	return trend
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func trendOf(records []snapshotRecord, query TrendQuery) ([]TrendPoint, error) {
	snapshots := make(map[string]*Snapshot, len(records))
	for _, record := range records {
		snapshot, err := record.snapshot()
		if err != nil {
			return nil, err
		}
		snapshots[snapshot.ID] = snapshot
	}
	return trendOfSnapshots(snapshots, query)
}

func BuildSnapshot(taskID, ownerUserID, taskType string, raw json.RawMessage, branding brand.Config, now time.Time) (*Snapshot, error) {
	return BuildSnapshotAt(taskID, ownerUserID, taskType, raw, branding, now, now)
}

func BuildSnapshotAt(taskID, ownerUserID, taskType string, raw json.RawMessage, branding brand.Config, completedAt, generatedAt time.Time) (*Snapshot, error) {
	return buildSnapshotAt(taskID, ownerUserID, taskType, raw, branding, completedAt, generatedAt, nil)
}

func buildSnapshotAt(taskID, ownerUserID, taskType string, raw json.RawMessage, branding brand.Config, completedAt, generatedAt time.Time, history []TrendPoint) (*Snapshot, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(ownerUserID) == "" || strings.TrimSpace(taskType) == "" || completedAt.IsZero() || generatedAt.IsZero() || !json.Valid(raw) {
		return nil, ErrInvalidSnapshot
	}
	risk, err := MapRisk(taskType, raw)
	if err != nil {
		return nil, ErrInvalidSnapshot
	}
	technicalFindings, totalTechnicalFindings, err := technicalFindingsOf(taskType, raw)
	if err != nil {
		return nil, ErrInvalidSnapshot
	}
	completedAt = completedAt.UTC()
	generatedAt = generatedAt.UTC()
	trend := immutableTrend(history, completedAt, generatedAt, risk)
	render := RenderModel{
		RenderVersion: "report-render-v2", MappingVersion: risk.MappingVersion, GeneratedAt: generatedAt, CompletedAt: completedAt,
		TaskID: taskID, TaskType: taskType, ProductName: branding.ProductName, PrimaryColor: branding.PrimaryColor, Watermark: branding.Watermark,
		Risk: risk, ScoreExplanation: scoreExplanation(taskType, risk), RiskTrend: trend,
		RiskDistribution: RiskDistribution{High: risk.High, Medium: risk.Medium, Low: risk.Low},
		TopRisks:         topRisksOf(risk), TechnicalFindings: technicalFindings, Recommendations: recommendationsOf(risk),
		Coverage: technicalCoverage(len(technicalFindings), totalTechnicalFindings), Conclusion: "请根据风险摘要安排修复与复核。",
	}
	renderData, err := json.Marshal(render)
	if err != nil {
		return nil, ErrInvalidSnapshot
	}
	return &Snapshot{
		ID: uuid.NewString(), TaskID: taskID, OwnerUserID: ownerUserID, TaskType: taskType,
		CompletedAt: completedAt, CreatedAt: generatedAt, RawResult: append(json.RawMessage(nil), raw...),
		Risk: risk, RenderData: renderData, Brand: cloneBrand(branding),
	}, nil
}

func immutableTrend(history []TrendPoint, completedAt, generatedAt time.Time, risk RiskSummary) []TrendPoint {
	lastDay := utcDay(generatedAt)
	firstDay := lastDay.AddDate(0, 0, -29)
	byDay := make(map[time.Time]TrendPoint, len(history))
	for _, point := range history {
		day := utcDay(point.Date)
		if day.Before(firstDay) || day.After(lastDay) {
			continue
		}
		point.Date = day
		byDay[day] = point
	}
	trend := make([]TrendPoint, 0, 30)
	for day := firstDay; !day.After(lastDay); day = day.AddDate(0, 0, 1) {
		point := byDay[day]
		point.Date = day
		if day.Equal(utcDay(completedAt)) && !completedAt.Before(firstDay) && completedAt.Before(lastDay.AddDate(0, 0, 1)) {
			point.Completed++
			point.High += risk.High
			point.Medium += risk.Medium
			point.Low += risk.Low
		}
		trend = append(trend, point)
	}
	return trend
}

func scoreExplanation(taskType string, risk RiskSummary) string {
	kind, _ := riskTaskType(taskType)
	if kind == "prompt" {
		return risk.MappingVersion + "：按可信评测结果中的总用例数与越狱数计算通过率；越狱项计为高风险。"
	}
	return risk.MappingVersion + "：保留扫描引擎评分，并将引擎风险等级标准化为高、中、低三档。"
}

func topRisksOf(risk RiskSummary) []TopRisk {
	top := make([]TopRisk, 0, 3)
	for _, item := range []struct {
		severity            string
		count               int
		impact, remediation string
	}{
		{"high", risk.High, "高风险发现可能需要优先处置。", "优先修复高风险发现并复测。"},
		{"medium", risk.Medium, "中风险发现需要纳入修复计划。", "安排修复并验证控制措施。"},
		{"low", risk.Low, "低风险发现应持续跟踪。", "纳入常规改进与复查。"},
	} {
		if item.count > 0 {
			top = append(top, TopRisk{Severity: item.severity, Count: item.count, Impact: item.impact, Remediation: item.remediation})
		}
	}
	return top
}

func recommendationsOf(risk RiskSummary) []string {
	recommendations := []string{"持续监控扫描覆盖范围和风险趋势。"}
	if risk.High > 0 {
		return append([]string{"优先修复高风险发现并复测。"}, recommendations...)
	}
	return append([]string{"根据风险分布安排修复与复核。"}, recommendations...)
}

func cloneSnapshot(snapshot *Snapshot) *Snapshot {
	copy := *snapshot
	copy.RawResult = append(json.RawMessage(nil), snapshot.RawResult...)
	copy.RenderData = append(json.RawMessage(nil), snapshot.RenderData...)
	copy.Brand = cloneBrand(snapshot.Brand)
	return &copy
}

func cloneBrand(config brand.Config) brand.Config {
	config.Logo = append([]byte(nil), config.Logo...)
	return config
}

type snapshotRecord struct {
	ID            string          `gorm:"primaryKey;column:id"`
	TaskID        string          `gorm:"column:task_id"`
	OwnerUserID   string          `gorm:"column:owner_user_id"`
	TaskType      string          `gorm:"column:task_type"`
	CompletedAt   time.Time       `gorm:"column:completed_at"`
	CreatedAt     time.Time       `gorm:"column:created_at"`
	RawResult     json.RawMessage `gorm:"type:jsonb;column:raw_result"`
	RiskSummary   json.RawMessage `gorm:"type:jsonb;column:risk_summary"`
	RenderData    json.RawMessage `gorm:"type:jsonb;column:render_data"`
	BrandSnapshot json.RawMessage `gorm:"type:jsonb;column:brand_snapshot"`
}

type summaryRecord struct {
	ID               string          `gorm:"column:id"`
	TaskID           string          `gorm:"column:task_id"`
	TaskType         string          `gorm:"column:task_type"`
	CompletedAt      time.Time       `gorm:"column:completed_at"`
	CreatedAt        time.Time       `gorm:"column:created_at"`
	RiskSummary      json.RawMessage `gorm:"column:risk_summary"`
	BrandProductName string          `gorm:"column:brand_product_name"`
}

func (snapshotRecord) TableName() string { return "report_snapshots" }

func snapshotRecordOf(snapshot *Snapshot) (snapshotRecord, error) {
	risk, err := json.Marshal(snapshot.Risk)
	if err != nil {
		return snapshotRecord{}, err
	}
	branding, err := json.Marshal(snapshot.Brand)
	if err != nil {
		return snapshotRecord{}, err
	}
	return snapshotRecord{ID: snapshot.ID, TaskID: snapshot.TaskID, OwnerUserID: snapshot.OwnerUserID, TaskType: snapshot.TaskType,
		CompletedAt: snapshot.CompletedAt, CreatedAt: snapshot.CreatedAt, RawResult: append(json.RawMessage(nil), snapshot.RawResult...),
		RiskSummary: risk, RenderData: append(json.RawMessage(nil), snapshot.RenderData...), BrandSnapshot: branding}, nil
}

func (record snapshotRecord) snapshot() (*Snapshot, error) {
	var risk RiskSummary
	var branding brand.Config
	if json.Unmarshal(record.RiskSummary, &risk) != nil || json.Unmarshal(record.BrandSnapshot, &branding) != nil {
		return nil, ErrInvalidSnapshot
	}
	return &Snapshot{ID: record.ID, TaskID: record.TaskID, OwnerUserID: record.OwnerUserID, TaskType: record.TaskType,
		CompletedAt: record.CompletedAt, CreatedAt: record.CreatedAt, RawResult: append(json.RawMessage(nil), record.RawResult...),
		Risk: risk, RenderData: append(json.RawMessage(nil), record.RenderData...), Brand: cloneBrand(branding)}, nil
}

func validateSnapshot(snapshot *Snapshot) error {
	if snapshot == nil || strings.TrimSpace(snapshot.ID) == "" || strings.TrimSpace(snapshot.TaskID) == "" ||
		strings.TrimSpace(snapshot.OwnerUserID) == "" || strings.TrimSpace(snapshot.TaskType) == "" ||
		snapshot.CompletedAt.IsZero() || snapshot.CreatedAt.IsZero() || !json.Valid(snapshot.RawResult) || !json.Valid(snapshot.RenderData) {
		return ErrInvalidSnapshot
	}
	return nil
}
