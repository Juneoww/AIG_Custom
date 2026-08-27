/**
 * 功能：展示按当前主体限定的管理者摘要、治理总览及独立加载、空、失败、无权限状态。
 * 实现：用 TanStack Query 读取服务端聚合 DTO，以 Fluent 指标、趋势和原生表格呈现。
 * 输入：GET /api/v1/platform/dashboard 的安全白名单响应。
 * 输出：管理者摘要、核心指标、30 日趋势、高风险待办和最近扫描任务。
 * 依赖：React Query、React Router、Fluent UI 与共享监管台账组件。
 */
import { Card, makeStyles, tokens } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'

import { ApiError } from '../../shared/api/errors'
import type { TaskStatus, TaskType } from '../../shared/api/types'
import { DataTable, type DataTableColumn } from '../../shared/components/DataTable'
import { MetricCard } from '../../shared/components/MetricCard'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { fetchDashboard, type AttentionItem, type DashboardView } from './api'
import { ExecutiveSummary } from './components/ExecutiveSummary'
import { RiskTrend } from './components/RiskTrend'

const useStyles = makeStyles({
  page: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalXXL,
  },
  metrics: {
    display: 'grid',
    gridTemplateColumns: 'repeat(4, minmax(0, 1fr))',
    gap: tokens.spacingHorizontalL,
    '@media (max-width: 960px)': {
      gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
    },
    '@media (max-width: 640px)': {
      gridTemplateColumns: '1fr',
    },
  },
  ledgerGrid: {
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 3fr) minmax(320px, 2fr)',
    gap: tokens.spacingHorizontalL,
    '@media (max-width: 960px)': {
      gridTemplateColumns: '1fr',
    },
  },
  panel: {
    minWidth: 0,
    padding: tokens.spacingVerticalL,
    borderRadius: tokens.borderRadiusMedium,
    boxShadow: 'none',
  },
  sectionTitle: {
    margin: `0 0 ${tokens.spacingVerticalL} 0`,
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase400,
    lineHeight: tokens.lineHeightBase400,
    fontWeight: tokens.fontWeightSemibold,
  },
  taskLink: {
    color: tokens.colorBrandForegroundLink,
  },
})

const taskTypeLabels: Record<TaskType, string> = {
  mcp_scan: 'MCP 扫描',
  ai_infra_scan: 'AI 基础设施扫描',
  model_redteam_report: '模型红队评测',
  agent_scan: 'Agent 扫描',
  unknown: '未知任务',
}

const taskStatusLabels: Record<TaskStatus, string> = {
  pending: '等待调度',
  dispatching: '正在调度',
  running: '执行中',
  succeeded: '已完成',
  failed: '执行失败',
  dispatch_failed: '调度失败',
  dispatch_unknown: '调度状态待确认',
  cancelled: '已取消',
}

const dateTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'UTC',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

function formatDateTime(value: string): string {
  return dateTimeFormatter.format(new Date(value))
}

function AttentionTable({ items }: { items: readonly AttentionItem[] }) {
  const styles = useStyles()
  const columns: readonly DataTableColumn<AttentionItem>[] = [
    { id: 'type', header: '任务类型', render: (item) => taskTypeLabels[item.task_type] },
    { id: 'score', header: '安全分', render: (item) => item.score },
    { id: 'high', header: '高风险', render: (item) => item.high },
    { id: 'completed', header: '完成时间', render: (item) => formatDateTime(item.completed_at) },
    {
      id: 'action',
      header: '操作',
      render: (item) => (
        <Link className={styles.taskLink} to={`/reports/${encodeURIComponent(item.report_id)}`} aria-label={`查看报告 ${item.report_id}`}>
          查看报告
        </Link>
      ),
    },
  ]
  return <DataTable caption="高风险待办台账" columns={columns} rows={items} getRowKey={(item) => item.report_id} />
}

function RecentTaskTable({ tasks }: { tasks: DashboardView['recent_tasks'] }) {
  const styles = useStyles()
  const columns: readonly DataTableColumn<DashboardView['recent_tasks'][number]>[] = [
    { id: 'type', header: '任务类型', render: (task) => taskTypeLabels[task.task_type] },
    { id: 'owner', header: '负责人', render: (task) => task.owner },
    { id: 'status', header: '状态', render: (task) => taskStatusLabels[task.status] },
    { id: 'updated', header: '更新时间', render: (task) => formatDateTime(task.updated_at) },
    {
      id: 'action',
      header: '操作',
      render: (task) => (
        <Link className={styles.taskLink} to={`/tasks/${encodeURIComponent(task.id)}`} aria-label={`查看任务 ${task.id}`}>
          查看
        </Link>
      ),
    },
  ]
  return <DataTable caption="最近扫描任务" columns={columns} rows={tasks} getRowKey={(task) => task.id} />
}

function DashboardContent({ view }: { view: DashboardView }) {
  const styles = useStyles()
  return (
    <>
      {!view.has_data ? (
        <StatePanel
          state="empty"
          title="暂无已完成报告"
          description="完成首份扫描并生成安全报告后，此处将展示快照指标。"
        />
      ) : null}
      {view.has_data ? <ExecutiveSummary view={view} /> : null}
      <section aria-label="核心指标">
        <div className={styles.metrics}>
          <MetricCard label="快照平均安全分" value={view.security_score ?? '暂无'} supportingText="最近 30 个 UTC 自然日" />
          <MetricCard label="高风险" value={view.risk.high} status="high" supportingText="有效报告快照汇总" />
          <MetricCard label="中风险" value={view.risk.medium} status="medium" supportingText="有效报告快照汇总" />
          <MetricCard label="低风险" value={view.risk.low} status="low" supportingText="有效报告快照汇总" />
        </div>
      </section>
      <div className={styles.ledgerGrid}>
        <Card className={styles.panel} role="region" aria-label="最近 30 日趋势">
          <RiskTrend points={view.trend} />
        </Card>
        <Card className={styles.panel} role="region" aria-label="高风险待办">
          <h2 className={styles.sectionTitle}>高风险待办</h2>
          {view.attention.length > 0 ? (
            <AttentionTable items={view.attention} />
          ) : (
            <StatePanel state="empty" title="暂无待关注事项" description="当前窗口内没有高风险或低分报告。" />
          )}
        </Card>
      </div>
      <Card className={styles.panel} role="region" aria-label="最近任务">
        <h2 className={styles.sectionTitle}>最近任务</h2>
        {view.recent_tasks.length > 0 ? (
          <RecentTaskTable tasks={view.recent_tasks} />
        ) : (
          <StatePanel state="empty" title="暂无扫描任务" description="创建扫描任务后将在此显示最近状态。" />
        )}
      </Card>
    </>
  )
}

export function DashboardPage() {
  const styles = useStyles()
  const query = useQuery({
    queryKey: ['dashboard'],
    queryFn: ({ signal }) => fetchDashboard(signal),
    retry: false,
  })

  return (
    <section className={styles.page}>
      <PageHeader title="治理总览" description="查看当前权限范围内的安全快照、风险待办与任务状态。" />
      {query.isPending ? <StatePanel state="loading" title="正在加载治理总览" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? (
        <StatePanel state="forbidden" title="无权查看治理总览" description="当前身份没有该总览的数据权限。" />
      ) : null}
      {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? (
        <StatePanel
          state="error"
          title="暂时无法加载治理总览"
          description="请检查网络或稍后重试。"
          actionLabel="重试"
          onAction={() => void query.refetch()}
        />
      ) : null}
      {query.data ? <DashboardContent view={query.data} /> : null}
    </section>
  )
}
