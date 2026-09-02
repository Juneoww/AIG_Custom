package reports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/common/portscan"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
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

type pageRepository interface {
	ListPage(context.Context, ListQuery) ([]Snapshot, int64, error)
}

type DashboardQuery struct {
	OwnerUserID    string
	From           time.Time
	To             time.Time
	AttentionLimit int
}

type DashboardProjection struct {
	SnapshotCount   int
	ScoreSum        int
	MappingVersions []string
	Risk            RiskSummary
	Trend           []DashboardTrendPoint
	Attention       []DashboardAttention
}

type DashboardTrendPoint struct {
	Date      time.Time
	Completed int
	ScoreSum  int
	High      int
	Medium    int
	Low       int
}

type DashboardAttention struct {
	ReportID    string
	TaskID      string
	TaskType    string
	CompletedAt time.Time
	Score       int
	High        int
	Medium      int
	Low         int
}

// DashboardRepository is an optional bounded read model. Keeping it separate
// avoids expanding the persistence contract required by task/report writers.
type DashboardRepository interface {
	Dashboard(context.Context, DashboardQuery) (DashboardProjection, error)
}

type MCPWorkbenchQuery struct {
	OwnerUserID string
	Now         time.Time
}

// MCPWorkbenchRepository is a separate bounded projection contract so report
// writers do not need MCP workbench-specific persistence knowledge.
type MCPWorkbenchRepository interface {
	MCPWorkbench(context.Context, MCPWorkbenchQuery) (MCPWorkbenchProjection, error)
}

type DashboardTaskVerifier func(context.Context, string, string) (bool, error)

const (
	dashboardMaxRiskCount     = 1<<31 - 1
	mcpWorkbenchMaxHighlights = 5
	mcpWorkbenchBatchSize     = 64
	mcpWorkbenchSummaryRunes  = 160
	mcpWorkbenchSummaryBytes  = 512
	// This is Unicode White_Space, the same character set used by strings.TrimSpace.
	dashboardTrimCharacters    = " \t\n\v\f\r\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"
	dashboardMappingVersionSQL = "btrim(reports.risk_summary ->> 'mapping_version', ?)"
)

type MemoryRepository struct {
	mu                     sync.RWMutex
	byID                   map[string]*Snapshot
	byTaskID               map[string]string
	dashboardTaskSucceeded DashboardTaskVerifier
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

func (repository *GormRepository) ListPage(ctx context.Context, query ListQuery) ([]Snapshot, int64, error) {
	filtered := txcontext.Gorm(ctx, repository.db).Table("report_snapshots")
	if query.OwnerUserID != "" {
		filtered = filtered.Where("owner_user_id = ?", query.OwnerUserID)
	}
	var total int64
	if err := filtered.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	listed := filtered.
		Select("id, task_id, task_type, completed_at, created_at, risk_summary, COALESCE(brand_snapshot ->> 'product_name', '') AS brand_product_name").
		Order("created_at DESC, id DESC").
		Limit(query.Limit).
		Offset(query.Offset)
	var records []summaryRecord
	if err := listed.Find(&records).Error; err != nil {
		return nil, 0, err
	}
	snapshots := make([]Snapshot, 0, len(records))
	for _, record := range records {
		var risk RiskSummary
		if json.Unmarshal(record.RiskSummary, &risk) != nil {
			return nil, 0, ErrInvalidSnapshot
		}
		snapshots = append(snapshots, Snapshot{
			ID: record.ID, TaskID: record.TaskID, TaskType: record.TaskType,
			CompletedAt: record.CompletedAt, CreatedAt: record.CreatedAt, Risk: risk,
			Brand: brand.Config{ProductName: record.BrandProductName},
		})
	}
	return snapshots, total, nil
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

func (repository *GormRepository) Dashboard(ctx context.Context, query DashboardQuery) (DashboardProjection, error) {
	if err := validateDashboardQuery(query); err != nil {
		return DashboardProjection{}, err
	}
	if transaction, ok := txcontext.FromGorm(ctx); ok {
		return repository.dashboardWithDB(transaction.WithContext(ctx), query)
	}
	var projection DashboardProjection
	err := repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		var err error
		projection, err = repository.dashboardWithDB(transaction, query)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return DashboardProjection{}, err
	}
	return projection, nil
}

func (repository *GormRepository) MCPWorkbench(ctx context.Context, query MCPWorkbenchQuery) (MCPWorkbenchProjection, error) {
	if err := validateMCPWorkbenchQuery(query); err != nil {
		return MCPWorkbenchProjection{}, err
	}
	if transaction, ok := txcontext.FromGorm(ctx); ok {
		return repository.mcpWorkbenchWithDB(transaction.WithContext(ctx), query)
	}
	var projection MCPWorkbenchProjection
	err := repository.db.WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		var err error
		projection, err = repository.mcpWorkbenchWithDB(transaction, query)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return MCPWorkbenchProjection{}, err
	}
	return projection, nil
}

func (repository *GormRepository) mcpWorkbenchWithDB(db *gorm.DB, query MCPWorkbenchQuery) (MCPWorkbenchProjection, error) {
	lowerBound, upperBound := mcpWorkbenchWindow(query.Now)
	base := db.Table("report_snapshots AS reports").
		Select("reports.id AS report_id, reports.task_id, reports.task_type, reports.completed_at, reports.risk_summary, reports.render_data").
		Where("reports.task_type IN ?", mcpTaskTypeAliases()).
		Where("reports.completed_at >= ? AND reports.completed_at < ?", lowerBound, upperBound)
	if query.OwnerUserID != "" {
		base = base.Where("reports.owner_user_id = ?", query.OwnerUserID)
	}
	accumulator := newMCPWorkbenchAccumulator()
	lastReportID := ""
	for {
		records := make([]mcpWorkbenchRecord, 0, mcpWorkbenchBatchSize)
		batch := base.Session(&gorm.Session{}).Order("reports.id ASC").Limit(mcpWorkbenchBatchSize)
		if lastReportID != "" {
			batch = batch.Where("reports.id > ?", lastReportID)
		}
		if err := batch.Find(&records).Error; err != nil {
			return MCPWorkbenchProjection{}, err
		}
		for index := range records {
			if err := accumulator.addRecord(records[index]); err != nil {
				return MCPWorkbenchProjection{}, err
			}
		}
		if len(records) < mcpWorkbenchBatchSize {
			break
		}
		lastReportID = records[len(records)-1].ReportID
	}
	return accumulator.result(), nil
}

func (repository *GormRepository) dashboardWithDB(db *gorm.DB, query DashboardQuery) (DashboardProjection, error) {
	base := repository.dashboardBase(db, query)
	type aggregateRecord struct {
		SnapshotCount   int64           `gorm:"column:snapshot_count"`
		ScoreSum        int64           `gorm:"column:score_sum"`
		High            int64           `gorm:"column:high"`
		Medium          int64           `gorm:"column:medium"`
		Low             int64           `gorm:"column:low"`
		MappingVersions json.RawMessage `gorm:"column:mapping_versions"`
	}
	var aggregate aggregateRecord
	if err := base.Session(&gorm.Session{}).Select(`
		COUNT(*) AS snapshot_count,
		COALESCE(SUM((reports.risk_summary ->> 'score')::integer), 0) AS score_sum,
		COALESCE(SUM((reports.risk_summary ->> 'high')::integer), 0) AS high,
		COALESCE(SUM((reports.risk_summary ->> 'medium')::integer), 0) AS medium,
		COALESCE(SUM((reports.risk_summary ->> 'low')::integer), 0) AS low,
		COALESCE(jsonb_agg(DISTINCT `+dashboardMappingVersionSQL+`), '[]'::jsonb) AS mapping_versions`, dashboardTrimCharacters).
		Scan(&aggregate).Error; err != nil {
		return DashboardProjection{}, err
	}
	aggregateValues, err := dashboardInts(aggregate.SnapshotCount, aggregate.ScoreSum, aggregate.High, aggregate.Medium, aggregate.Low)
	if err != nil {
		return DashboardProjection{}, err
	}
	projection := DashboardProjection{
		SnapshotCount: aggregateValues[0],
		ScoreSum:      aggregateValues[1],
		Risk:          RiskSummary{High: aggregateValues[2], Medium: aggregateValues[3], Low: aggregateValues[4]},
	}
	if len(aggregate.MappingVersions) > 0 && json.Unmarshal(aggregate.MappingVersions, &projection.MappingVersions) != nil {
		return DashboardProjection{}, ErrInvalidSnapshot
	}
	sort.Strings(projection.MappingVersions)

	type trendRecord struct {
		Date      time.Time `gorm:"column:date"`
		Completed int64     `gorm:"column:completed"`
		ScoreSum  int64     `gorm:"column:score_sum"`
		High      int64     `gorm:"column:high"`
		Medium    int64     `gorm:"column:medium"`
		Low       int64     `gorm:"column:low"`
	}
	var trendRecords []trendRecord
	if err := base.Session(&gorm.Session{}).Select(`
		date_trunc('day', reports.completed_at AT TIME ZONE 'UTC') AS date,
		COUNT(*) AS completed,
		COALESCE(SUM((reports.risk_summary ->> 'score')::integer), 0) AS score_sum,
		COALESCE(SUM((reports.risk_summary ->> 'high')::integer), 0) AS high,
		COALESCE(SUM((reports.risk_summary ->> 'medium')::integer), 0) AS medium,
		COALESCE(SUM((reports.risk_summary ->> 'low')::integer), 0) AS low`).
		Group("date_trunc('day', reports.completed_at AT TIME ZONE 'UTC')").
		Order("date ASC").Scan(&trendRecords).Error; err != nil {
		return DashboardProjection{}, err
	}
	projection.Trend = make([]DashboardTrendPoint, 0, len(trendRecords))
	for _, record := range trendRecords {
		values, err := dashboardInts(record.Completed, record.ScoreSum, record.High, record.Medium, record.Low)
		if err != nil {
			return DashboardProjection{}, err
		}
		projection.Trend = append(projection.Trend, DashboardTrendPoint{
			Date: record.Date, Completed: values[0], ScoreSum: values[1],
			High: values[2], Medium: values[3], Low: values[4],
		})
	}

	attention := base.Session(&gorm.Session{}).
		Select(`reports.id AS report_id, reports.task_id, reports.task_type, reports.completed_at,
			(reports.risk_summary ->> 'score')::integer AS score,
			(reports.risk_summary ->> 'high')::integer AS high,
			(reports.risk_summary ->> 'medium')::integer AS medium,
			(reports.risk_summary ->> 'low')::integer AS low`).
		Where("((reports.risk_summary ->> 'high')::integer > 0 OR (reports.risk_summary ->> 'score')::integer < 60)").
		Order("high DESC, score ASC, completed_at DESC, report_id DESC").
		Limit(query.AttentionLimit)
	if err := attention.Scan(&projection.Attention).Error; err != nil {
		return DashboardProjection{}, err
	}
	return projection, nil
}

func (repository *GormRepository) dashboardBase(db *gorm.DB, query DashboardQuery) *gorm.DB {
	db = db.Table("report_snapshots AS reports").
		Joins("JOIN platform_tasks AS tasks ON tasks.id = reports.task_id AND tasks.owner_user_id = reports.owner_user_id").
		Where("tasks.status = ?", "succeeded").
		Where("reports.completed_at >= ? AND reports.completed_at < ?", query.From.UTC(), query.To.UTC())
	db = db.Where("jsonb_typeof(reports.risk_summary -> 'mapping_version') = 'string'").
		Where(dashboardMappingVersionSQL+" <> ''", dashboardTrimCharacters)
	for field, maximum := range map[string]int{"score": 100, "high": dashboardMaxRiskCount, "medium": dashboardMaxRiskCount, "low": dashboardMaxRiskCount} {
		db = db.Where(dashboardIntegerIsValid(field, maximum))
	}
	if query.OwnerUserID != "" {
		db = db.Where("reports.owner_user_id = ?", query.OwnerUserID)
	}
	return db
}

func dashboardIntegerIsValid(field string, maximum int) string {
	expression := fmt.Sprintf("CASE WHEN jsonb_typeof(reports.risk_summary -> '%s') = 'number' THEN (reports.risk_summary ->> '%s')::numeric END", field, field)
	return fmt.Sprintf("(%s BETWEEN 0 AND %d AND mod(%s, 1) = 0)", expression, maximum, expression)
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{byID: map[string]*Snapshot{}, byTaskID: map[string]string{}}
}

func (repository *MemoryRepository) SetDashboardTaskVerifier(verifier DashboardTaskVerifier) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.dashboardTaskSucceeded = verifier
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

func (repository *MemoryRepository) ListPage(_ context.Context, query ListQuery) ([]Snapshot, int64, error) {
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	snapshots := make([]Snapshot, 0, len(repository.byID))
	for _, snapshot := range repository.byID {
		if query.OwnerUserID != "" && snapshot.OwnerUserID != query.OwnerUserID {
			continue
		}
		snapshots = append(snapshots, Snapshot{
			ID: snapshot.ID, TaskID: snapshot.TaskID, TaskType: snapshot.TaskType,
			CompletedAt: snapshot.CompletedAt, CreatedAt: snapshot.CreatedAt, Risk: snapshot.Risk,
			Brand: brand.Config{ProductName: snapshot.Brand.ProductName},
		})
	}
	sort.Slice(snapshots, func(left, right int) bool {
		if snapshots[left].CreatedAt.Equal(snapshots[right].CreatedAt) {
			return snapshots[left].ID > snapshots[right].ID
		}
		return snapshots[left].CreatedAt.After(snapshots[right].CreatedAt)
	})
	total := int64(len(snapshots))
	if query.Offset >= len(snapshots) {
		return []Snapshot{}, total, nil
	}
	if query.Offset > 0 {
		snapshots = snapshots[query.Offset:]
	}
	if query.Limit > 0 && len(snapshots) > query.Limit {
		snapshots = snapshots[:query.Limit]
	}
	return snapshots, total, nil
}

func (service *Service) ListPage(ctx context.Context, subject identity.Subject, page, pageSize int) ([]Snapshot, int64, error) {
	query, err := listQueryFor(subject)
	if err != nil {
		return nil, 0, err
	}
	if page < 1 || page > maxReportPage || pageSize < 1 || pageSize > maxReportPageSize {
		return nil, 0, ErrInvalidSnapshot
	}
	repository, ok := service.repository.(pageRepository)
	if !ok {
		return nil, 0, ErrInvalidSnapshot
	}
	query.Limit = pageSize
	query.Offset = (page - 1) * pageSize
	return repository.ListPage(ctx, query)
}

func (repository *MemoryRepository) Trend(_ context.Context, query TrendQuery) ([]TrendPoint, error) {
	if query.Now.IsZero() || query.Days < 0 {
		return nil, ErrInvalidSnapshot
	}
	repository.mu.RLock()
	defer repository.mu.RUnlock()
	return trendOfSnapshots(repository.byID, query)
}

func (repository *MemoryRepository) Dashboard(ctx context.Context, query DashboardQuery) (DashboardProjection, error) {
	if err := validateDashboardQuery(query); err != nil {
		return DashboardProjection{}, err
	}
	repository.mu.RLock()
	verifier := repository.dashboardTaskSucceeded
	type safeSnapshot struct {
		ID, TaskID, OwnerUserID, TaskType string
		CompletedAt                       time.Time
		Risk                              RiskSummary
	}
	snapshots := make([]safeSnapshot, 0, len(repository.byID))
	for _, snapshot := range repository.byID {
		snapshots = append(snapshots, safeSnapshot{
			ID: snapshot.ID, TaskID: snapshot.TaskID, OwnerUserID: snapshot.OwnerUserID, TaskType: snapshot.TaskType,
			CompletedAt: snapshot.CompletedAt, Risk: snapshot.Risk,
		})
	}
	repository.mu.RUnlock()
	projection := DashboardProjection{}
	if verifier == nil {
		return projection, nil
	}
	versions := map[string]struct{}{}
	trend := map[time.Time]DashboardTrendPoint{}
	for _, snapshot := range snapshots {
		if query.OwnerUserID != "" && snapshot.OwnerUserID != query.OwnerUserID ||
			snapshot.CompletedAt.Before(query.From) || !snapshot.CompletedAt.Before(query.To) || !validDashboardRisk(snapshot.Risk) {
			continue
		}
		succeeded, err := verifier(ctx, snapshot.TaskID, snapshot.OwnerUserID)
		if err != nil {
			return DashboardProjection{}, err
		}
		if !succeeded {
			continue
		}
		if err := dashboardAccumulate(&projection.SnapshotCount, 1); err != nil {
			return DashboardProjection{}, err
		}
		if err := dashboardAccumulate(&projection.ScoreSum, snapshot.Risk.Score); err != nil {
			return DashboardProjection{}, err
		}
		if err := dashboardAccumulate(&projection.Risk.High, snapshot.Risk.High); err != nil {
			return DashboardProjection{}, err
		}
		if err := dashboardAccumulate(&projection.Risk.Medium, snapshot.Risk.Medium); err != nil {
			return DashboardProjection{}, err
		}
		if err := dashboardAccumulate(&projection.Risk.Low, snapshot.Risk.Low); err != nil {
			return DashboardProjection{}, err
		}
		mappingVersion := strings.TrimSpace(snapshot.Risk.MappingVersion)
		if mappingVersion != "" {
			versions[mappingVersion] = struct{}{}
		}
		day := utcDay(snapshot.CompletedAt)
		point := trend[day]
		point.Date = day
		if err := dashboardAccumulate(&point.Completed, 1); err != nil {
			return DashboardProjection{}, err
		}
		if err := dashboardAccumulate(&point.ScoreSum, snapshot.Risk.Score); err != nil {
			return DashboardProjection{}, err
		}
		if err := dashboardAccumulate(&point.High, snapshot.Risk.High); err != nil {
			return DashboardProjection{}, err
		}
		if err := dashboardAccumulate(&point.Medium, snapshot.Risk.Medium); err != nil {
			return DashboardProjection{}, err
		}
		if err := dashboardAccumulate(&point.Low, snapshot.Risk.Low); err != nil {
			return DashboardProjection{}, err
		}
		trend[day] = point
		if snapshot.Risk.High > 0 || snapshot.Risk.Score < 60 {
			projection.Attention = append(projection.Attention, DashboardAttention{
				ReportID: snapshot.ID, TaskID: snapshot.TaskID, TaskType: snapshot.TaskType,
				CompletedAt: snapshot.CompletedAt.UTC(), Score: snapshot.Risk.Score,
				High: snapshot.Risk.High, Medium: snapshot.Risk.Medium, Low: snapshot.Risk.Low,
			})
		}
	}
	for version := range versions {
		projection.MappingVersions = append(projection.MappingVersions, version)
	}
	sort.Strings(projection.MappingVersions)
	for _, point := range trend {
		projection.Trend = append(projection.Trend, point)
	}
	sort.Slice(projection.Trend, func(left, right int) bool { return projection.Trend[left].Date.Before(projection.Trend[right].Date) })
	sort.Slice(projection.Attention, func(left, right int) bool {
		first, second := projection.Attention[left], projection.Attention[right]
		if first.High != second.High {
			return first.High > second.High
		}
		if first.Score != second.Score {
			return first.Score < second.Score
		}
		if !first.CompletedAt.Equal(second.CompletedAt) {
			return first.CompletedAt.After(second.CompletedAt)
		}
		return first.ReportID > second.ReportID
	})
	if len(projection.Attention) > query.AttentionLimit {
		projection.Attention = projection.Attention[:query.AttentionLimit]
	}
	return projection, nil
}

func (repository *MemoryRepository) MCPWorkbench(_ context.Context, query MCPWorkbenchQuery) (MCPWorkbenchProjection, error) {
	if err := validateMCPWorkbenchQuery(query); err != nil {
		return MCPWorkbenchProjection{}, err
	}
	lowerBound, upperBound := mcpWorkbenchWindow(query.Now)
	repository.mu.RLock()
	accumulator := newMCPWorkbenchAccumulator()
	for _, snapshot := range repository.byID {
		if !isMCPTaskType(snapshot.TaskType) ||
			query.OwnerUserID != "" && snapshot.OwnerUserID != query.OwnerUserID ||
			snapshot.CompletedAt.Before(lowerBound) || !snapshot.CompletedAt.Before(upperBound) {
			continue
		}
		if err := accumulator.add(mcpWorkbenchReport{
			ReportID: snapshot.ID, TaskID: snapshot.TaskID, TaskType: snapshot.TaskType,
			CompletedAt: snapshot.CompletedAt, Risk: snapshot.Risk, RenderData: snapshot.RenderData,
		}); err != nil {
			repository.mu.RUnlock()
			return MCPWorkbenchProjection{}, err
		}
	}
	repository.mu.RUnlock()
	return accumulator.result(), nil
}

type mcpWorkbenchRecord struct {
	ReportID    string          `gorm:"column:report_id"`
	TaskID      string          `gorm:"column:task_id"`
	TaskType    string          `gorm:"column:task_type"`
	CompletedAt time.Time       `gorm:"column:completed_at"`
	RiskSummary json.RawMessage `gorm:"column:risk_summary"`
	RenderData  json.RawMessage `gorm:"column:render_data"`
}

type mcpWorkbenchReport struct {
	ReportID    string
	TaskID      string
	TaskType    string
	CompletedAt time.Time
	Risk        RiskSummary
	RenderData  json.RawMessage
}

type mcpWorkbenchAccumulator struct {
	projection MCPWorkbenchProjection
}

func newMCPWorkbenchAccumulator() *mcpWorkbenchAccumulator {
	return &mcpWorkbenchAccumulator{projection: MCPWorkbenchProjection{Highlights: make([]MCPRiskHighlight, 0, mcpWorkbenchMaxHighlights)}}
}

func (accumulator *mcpWorkbenchAccumulator) addRecord(record mcpWorkbenchRecord) error {
	var risk RiskSummary
	_ = json.Unmarshal(record.RiskSummary, &risk)
	return accumulator.add(mcpWorkbenchReport{
		ReportID: record.ReportID, TaskID: record.TaskID, TaskType: record.TaskType,
		CompletedAt: record.CompletedAt, Risk: risk, RenderData: record.RenderData,
	})
}

func (accumulator *mcpWorkbenchAccumulator) add(report mcpWorkbenchReport) error {
	if !isMCPTaskType(report.TaskType) {
		return nil
	}
	if err := dashboardAccumulate(&accumulator.projection.Completed30d, 1); err != nil {
		return err
	}
	if !validMCPWorkbenchRisk(report.Risk) {
		return nil
	}
	if err := dashboardAccumulate(&accumulator.projection.HighRisk, report.Risk.High); err != nil {
		return err
	}
	mcpWorkbenchVisitHighlights(report, accumulator.addHighlight)
	return nil
}

func (accumulator *mcpWorkbenchAccumulator) addHighlight(highlight MCPRiskHighlight) {
	accumulator.projection.Highlights = append(accumulator.projection.Highlights, highlight)
	sortMCPWorkbenchHighlights(accumulator.projection.Highlights)
	if len(accumulator.projection.Highlights) > mcpWorkbenchMaxHighlights {
		accumulator.projection.Highlights = accumulator.projection.Highlights[:mcpWorkbenchMaxHighlights]
	}
}

func (accumulator *mcpWorkbenchAccumulator) result() MCPWorkbenchProjection {
	sortMCPWorkbenchHighlights(accumulator.projection.Highlights)
	return accumulator.projection
}

func sortMCPWorkbenchHighlights(highlights []MCPRiskHighlight) {
	sort.SliceStable(highlights, func(left, right int) bool {
		first, second := highlights[left], highlights[right]
		if mcpWorkbenchSeverityRank(first.Severity) != mcpWorkbenchSeverityRank(second.Severity) {
			return mcpWorkbenchSeverityRank(first.Severity) < mcpWorkbenchSeverityRank(second.Severity)
		}
		if !first.CompletedAt.Equal(second.CompletedAt) {
			return first.CompletedAt.After(second.CompletedAt)
		}
		if first.ReportID != second.ReportID {
			return first.ReportID > second.ReportID
		}
		return first.Summary < second.Summary
	})
}

func mcpWorkbenchVisitHighlights(report mcpWorkbenchReport, visit func(MCPRiskHighlight)) {
	var header mcpWorkbenchRenderHeader
	if json.Unmarshal(report.RenderData, &header) == nil && validMCPWorkbenchRenderHeader(report, header) {
		var safeRender mcpWorkbenchSafeRender
		if json.Unmarshal(report.RenderData, &safeRender) != nil {
			mcpWorkbenchVisitGenericHighlights(report, visit)
			return
		}
		found := false
		for _, finding := range safeRender.TechnicalFindings {
			if !validMCPFindingCategory(finding.Category) || !validMCPFindingSeverity(finding.Severity) {
				continue
			}
			found = true
			visit(MCPRiskHighlight{
				ReportID: report.ReportID, TaskID: report.TaskID, Severity: finding.Severity, Category: finding.Category,
				Summary:     mcpWorkbenchFindingSummary(finding.Category, finding.Severity),
				CompletedAt: report.CompletedAt.UTC(),
			})
		}
		if found {
			return
		}
	}
	mcpWorkbenchVisitGenericHighlights(report, visit)
}

// mcpWorkbenchRenderHeader intentionally omits TechnicalFindings so legacy
// fallback never decodes historic title, evidence, impact, or remediation text.
type mcpWorkbenchRenderHeader struct {
	RenderVersion string      `json:"render_version"`
	TaskID        string      `json:"task_id"`
	TaskType      string      `json:"task_type"`
	CompletedAt   time.Time   `json:"completed_at"`
	Risk          RiskSummary `json:"risk"`
}

// mcpWorkbenchSafeRender is a projection of the only finding fields accepted
// by the workbench. It deliberately has no scanner-controlled display text.
type mcpWorkbenchSafeRender struct {
	TechnicalFindings []struct {
		Category string `json:"category"`
		Severity string `json:"severity"`
	} `json:"technical_findings"`
}

func validMCPWorkbenchRenderHeader(report mcpWorkbenchReport, header mcpWorkbenchRenderHeader) bool {
	return header.RenderVersion == "report-render-v2" && header.TaskID == report.TaskID && header.TaskType == report.TaskType &&
		header.CompletedAt.UTC().Equal(report.CompletedAt.UTC()) && header.Risk == report.Risk
}

func mcpWorkbenchVisitGenericHighlights(report mcpWorkbenchReport, visit func(MCPRiskHighlight)) {
	for _, item := range []struct {
		severity string
		count    int
		label    string
	}{
		{severity: "high", count: report.Risk.High, label: "高"},
		{severity: "medium", count: report.Risk.Medium, label: "中"},
		{severity: "low", count: report.Risk.Low, label: "低"},
	} {
		if item.count == 0 {
			continue
		}
		visit(MCPRiskHighlight{
			ReportID: report.ReportID, TaskID: report.TaskID, Severity: item.severity, Category: "other",
			Summary:     safeFindingText(fmt.Sprintf("该 MCP 报告包含 %d 项%s风险发现。", item.count, item.label), "MCP 安全发现", mcpWorkbenchSummaryRunes, mcpWorkbenchSummaryBytes),
			CompletedAt: report.CompletedAt.UTC(),
		})
	}
}

func mcpWorkbenchFindingSummary(category, severity string) string {
	categoryLabel := "MCP 其他安全风险"
	switch category {
	case "dangerous_tool":
		categoryLabel = "MCP 危险工具风险"
	case "command_file":
		categoryLabel = "MCP 命令或文件访问风险"
	case "authorization":
		categoryLabel = "MCP 授权边界风险"
	case "data_leakage":
		categoryLabel = "MCP 数据泄露风险"
	case "tool_poisoning":
		categoryLabel = "MCP 工具投毒风险"
	case "skill_mismatch":
		categoryLabel = "MCP 技能匹配风险"
	}
	severityLabel := "低"
	switch severity {
	case "high":
		severityLabel = "高"
	case "medium":
		severityLabel = "中"
	}
	return safeFindingText(fmt.Sprintf("%s发现（%s风险）。", categoryLabel, severityLabel), "MCP 安全发现", mcpWorkbenchSummaryRunes, mcpWorkbenchSummaryBytes)
}

func validMCPFindingCategory(value string) bool {
	switch value {
	case "dangerous_tool", "command_file", "authorization", "data_leakage", "tool_poisoning", "skill_mismatch", "other":
		return true
	default:
		return false
	}
}

func validMCPFindingSeverity(value string) bool {
	switch value {
	case "high", "medium", "low":
		return true
	default:
		return false
	}
}

func mcpWorkbenchSeverityRank(value string) int {
	switch value {
	case "high":
		return 0
	case "medium":
		return 1
	default:
		return 2
	}
}

func validMCPWorkbenchRisk(risk RiskSummary) bool {
	return risk.High >= 0 && risk.Medium >= 0 && risk.Low >= 0
}

func mcpWorkbenchWindow(now time.Time) (time.Time, time.Time) {
	today := utcDay(now)
	return today.AddDate(0, 0, -29), today.AddDate(0, 0, 1)
}

func validateMCPWorkbenchQuery(query MCPWorkbenchQuery) error {
	if query.Now.IsZero() {
		return ErrInvalidSnapshot
	}
	return nil
}

func validDashboardRisk(risk RiskSummary) bool {
	return strings.TrimSpace(risk.MappingVersion) != "" && risk.Score >= 0 && risk.Score <= 100 &&
		risk.High >= 0 && risk.High <= dashboardMaxRiskCount &&
		risk.Medium >= 0 && risk.Medium <= dashboardMaxRiskCount &&
		risk.Low >= 0 && risk.Low <= dashboardMaxRiskCount
}

func dashboardCheckedAdd(left, right int64) (int64, bool) {
	if right > 0 && left > math.MaxInt64-right || right < 0 && left < math.MinInt64-right {
		return 0, false
	}
	return left + right, true
}

func dashboardInts(values ...int64) ([]int, error) {
	converted := make([]int, len(values))
	for index, value := range values {
		if value < 0 || uint64(value) > uint64(^uint(0)>>1) {
			return nil, ErrInvalidSnapshot
		}
		converted[index] = int(value)
	}
	return converted, nil
}

func dashboardAccumulate(target *int, value int) error {
	if target == nil || value < 0 {
		return ErrInvalidSnapshot
	}
	sum, ok := dashboardCheckedAdd(int64(*target), int64(value))
	if !ok {
		return ErrInvalidSnapshot
	}
	converted, err := dashboardInts(sum)
	if err != nil {
		return err
	}
	*target = converted[0]
	return nil
}

func validateDashboardQuery(query DashboardQuery) error {
	if query.From.IsZero() || query.To.IsZero() || !query.To.After(query.From) || query.AttentionLimit < 1 || query.AttentionLimit > 5 {
		return ErrInvalidSnapshot
	}
	return nil
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
	return buildSnapshotWithInfrastructurePortScanAt(taskID, ownerUserID, taskType, raw, branding, completedAt, generatedAt, history, "", "")
}

func buildSnapshotWithInfrastructurePortScanAt(taskID, ownerUserID, taskType string, raw json.RawMessage, branding brand.Config, completedAt, generatedAt time.Time, history []TrendPoint, portScanMode portscan.Mode, portSpec string) (*Snapshot, error) {
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
	if mode, spec, valid := trustedInfrastructurePortScan(taskType, portScanMode, portSpec); valid {
		render.PortScanMode = string(mode)
		render.PortSpec = spec
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
