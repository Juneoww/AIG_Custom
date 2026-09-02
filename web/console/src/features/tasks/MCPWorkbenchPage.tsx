/**
 * 功能：展示 MCP 安全扫描的受治理入口、进度指标和近期风险摘要。
 * 实现：仅消费 MCP 工作台白名单 DTO，并以 Fluent 台账组件呈现响应式工作区。
 * 输入：当前会话角色与 GET /api/v1/platform/mcp-workbench 的安全投影。
 * 输出：MCP 扫描入口、指标、活动任务和最近风险，不渲染目标、原始结果或凭据。
 * 依赖：React Query、React Router、Fluent UI、共享台账组件和 MCP 工作台 API。
 */
import { Card, Text, makeStyles, mergeClasses, tokens } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'

import { useSession } from '../auth/session'
import { ApiError } from '../../shared/api/errors'
import type {
  MCPRiskCategory,
  MCPRiskSeverity,
  MCPSourceKind,
  MCPWorkbenchActiveTask,
  MCPWorkbenchRecentRisk,
  TaskStatus,
} from '../../shared/api/types'
import { DataTable, type DataTableColumn } from '../../shared/components/DataTable'
import { MetricCard } from '../../shared/components/MetricCard'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { fetchMCPWorkbench } from './mcpWorkbenchApi'

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
    '@media (max-width: 620px)': {
      gridTemplateColumns: '1fr',
    },
  },
  entryGrid: {
    display: 'grid',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
    gap: tokens.spacingHorizontalL,
    '@media (max-width: 720px)': {
      gridTemplateColumns: '1fr',
    },
  },
  entryLink: {
    minWidth: 0,
    color: 'inherit',
    textDecorationLine: 'none',
    ':focus-visible': {
      outlineColor: tokens.colorStrokeFocus2,
      outlineStyle: 'solid',
      outlineWidth: '2px',
      outlineOffset: '3px',
    },
  },
  entryCard: {
    minHeight: '152px',
    display: 'flex',
    flexDirection: 'column',
    justifyContent: 'space-between',
    gap: tokens.spacingVerticalM,
    padding: tokens.spacingVerticalL,
    borderRadius: tokens.borderRadiusMedium,
    boxShadow: 'none',
    borderTop: `3px solid ${tokens.colorBrandStroke1}`,
    ':hover': {
      backgroundColor: tokens.colorNeutralBackground1Hover,
    },
  },
  entryTitle: {
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase400,
    fontWeight: tokens.fontWeightSemibold,
  },
  entryDescription: {
    color: tokens.colorNeutralForeground2,
    lineHeight: tokens.lineHeightBase400,
  },
  entryHint: {
    color: tokens.colorBrandForeground1,
    fontWeight: tokens.fontWeightSemibold,
  },
  ledgerGrid: {
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 3fr) minmax(300px, 2fr)',
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
  riskList: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalM,
    margin: 0,
    padding: 0,
    listStyleType: 'none',
  },
  riskItem: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalXS,
    paddingBottom: tokens.spacingVerticalM,
    borderBottom: `1px solid ${tokens.colorNeutralStroke2}`,
  },
  riskMeta: {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: tokens.spacingHorizontalS,
  },
  severity: {
    fontSize: tokens.fontSizeBase200,
    fontWeight: tokens.fontWeightSemibold,
  },
  severityHigh: { color: tokens.colorPaletteRedForeground1 },
  severityMedium: { color: tokens.colorPaletteDarkOrangeForeground1 },
  severityLow: { color: tokens.colorPaletteBlueForeground2 },
  category: {
    color: tokens.colorNeutralForeground2,
    fontSize: tokens.fontSizeBase200,
  },
  riskSummary: {
    color: tokens.colorNeutralForeground1,
    lineHeight: tokens.lineHeightBase400,
  },
  completedAt: {
    color: tokens.colorNeutralForeground2,
    fontSize: tokens.fontSizeBase200,
  },
  footerLinks: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: tokens.spacingHorizontalL,
  },
  footerLink: {
    color: tokens.colorBrandForegroundLink,
    fontWeight: tokens.fontWeightSemibold,
  },
})

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

const sourceKindLabels: Record<MCPSourceKind, string> = {
  repository: '代码仓库',
  service: '受控服务',
  legacy_unknown: '旧版记录',
}

const severityLabels: Record<MCPRiskSeverity, string> = {
  high: '高风险',
  medium: '中风险',
  low: '低风险',
}

const categoryLabels: Record<MCPRiskCategory, string> = {
  dangerous_tool: '危险工具调用',
  command_file: '命令与文件',
  authorization: '授权边界',
  data_leakage: '数据泄露',
  tool_poisoning: '工具投毒',
  skill_mismatch: '能力错配',
  other: '其他风险',
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

function ActiveTaskTable({ tasks }: { tasks: readonly MCPWorkbenchActiveTask[] }) {
  const columns: readonly DataTableColumn<MCPWorkbenchActiveTask>[] = [
    { id: 'label', header: '任务', render: (task) => task.label },
    { id: 'source', header: '扫描对象', render: (task) => sourceKindLabels[task.source_kind] },
    { id: 'status', header: '状态', render: (task) => taskStatusLabels[task.status] },
    { id: 'phase', header: '阶段', render: (task) => task.phase ?? '阶段未提供' },
    { id: 'updated', header: '更新时间', render: (task) => formatDateTime(task.updated_at) },
  ]

  return <DataTable caption="进行中的 MCP 扫描" columns={columns} rows={tasks} getRowKey={(task) => task.task_id} />
}

function RecentRiskList({ risks }: { risks: readonly MCPWorkbenchRecentRisk[] }) {
  const styles = useStyles()
  const severityClass = (severity: MCPRiskSeverity) =>
    mergeClasses(styles.severity, severity === 'high' && styles.severityHigh, severity === 'medium' && styles.severityMedium, severity === 'low' && styles.severityLow)

  return (
    <ol className={styles.riskList}>
      {risks.map((risk) => (
        <li className={styles.riskItem} key={risk.report_id}>
          <div className={styles.riskMeta}>
            <Text className={severityClass(risk.severity)}>{severityLabels[risk.severity]}</Text>
            <Text className={styles.category}>{categoryLabels[risk.category]}</Text>
          </div>
          <Text className={styles.riskSummary}>{risk.summary}</Text>
          <Text className={styles.completedAt}>{formatDateTime(risk.completed_at)}</Text>
        </li>
      ))}
    </ol>
  )
}

function CreationEntries() {
  const styles = useStyles()
  return (
    <section aria-labelledby="mcp-scan-entry-title">
      <h2 className={styles.sectionTitle} id="mcp-scan-entry-title">开始 MCP 扫描</h2>
      <div className={styles.entryGrid}>
        <Link className={styles.entryLink} to="/tasks/new?task_type=mcp_scan&source_kind=repository" aria-label="创建仓库 MCP 扫描">
          <Card className={styles.entryCard}>
            <div>
              <Text className={styles.entryTitle}>仓库 / 代码包扫描</Text>
              <Text as="p" className={styles.entryDescription}>对已获授权的仓库地址或已上传代码包进行静态安全扫描。</Text>
            </div>
            <Text className={styles.entryHint}>配置仓库扫描 →</Text>
          </Card>
        </Link>
        <Link className={styles.entryLink} to="/tasks/new?task_type=mcp_scan&source_kind=service" aria-label="创建受控服务 MCP 扫描">
          <Card className={styles.entryCard}>
            <div>
              <Text className={styles.entryTitle}>受控 MCP 服务验证</Text>
              <Text as="p" className={styles.entryDescription}>仅针对已获授权的受控服务开展验证，提交时需再次确认授权。</Text>
            </div>
            <Text className={styles.entryHint}>配置服务扫描 →</Text>
          </Card>
        </Link>
      </div>
    </section>
  )
}

function WorkbenchContent({
  activeTasks,
  recentRisks,
  metrics,
  canCreate,
}: {
  activeTasks: readonly MCPWorkbenchActiveTask[]
  recentRisks: readonly MCPWorkbenchRecentRisk[]
  metrics: { running: number; pending: number; high_risk: number; completed_30d: number }
  canCreate: boolean
}) {
  const styles = useStyles()
  return (
    <>
      <section aria-label="MCP 扫描指标">
        <div className={styles.metrics}>
          <MetricCard label="正在执行" value={metrics.running} supportingText="30 日窗口内创建的任务" />
          <MetricCard label="等待执行" value={metrics.pending} supportingText="30 日窗口内创建的任务" />
          <MetricCard label="高风险" value={metrics.high_risk} status="high" supportingText="30 日窗口内完成的报告" />
          <MetricCard label="30 日已完成" value={metrics.completed_30d} status="success" supportingText="已生成安全报告" />
        </div>
      </section>
      {canCreate ? <CreationEntries /> : null}
      <div className={styles.ledgerGrid}>
        <Card className={styles.panel} role="region" aria-label="进行中的任务">
          <h2 className={styles.sectionTitle}>进行中的任务</h2>
          {activeTasks.length > 0 ? (
            <ActiveTaskTable tasks={activeTasks} />
          ) : (
            <StatePanel state="empty" title="暂无进行中的 MCP 扫描" description="提交受治理的 MCP 扫描后，此处会显示当前状态。" />
          )}
        </Card>
        <Card className={styles.panel} role="region" aria-label="最近风险">
          <h2 className={styles.sectionTitle}>最近风险</h2>
          {recentRisks.length > 0 ? (
            <RecentRiskList risks={recentRisks} />
          ) : (
            <StatePanel state="empty" title="暂无近期 MCP 风险" description="完成扫描并生成安全报告后，此处会显示固定风险摘要。" />
          )}
        </Card>
      </div>
      <nav className={styles.footerLinks} aria-label="MCP 工作台相关链接">
        <Link className={styles.footerLink} to="/tasks?task_type=mcp_scan" aria-label="查看 MCP 扫描历史">查看 MCP 扫描历史</Link>
        <Link className={styles.footerLink} to="/knowledge/mcp" aria-label="查看 MCP 知识库">查看 MCP 知识库</Link>
      </nav>
    </>
  )
}

export function MCPWorkbenchPage() {
  const styles = useStyles()
  const { state } = useSession()
  const canCreate = state.status === 'authenticated' && state.subject.role !== 'auditor'
  const query = useQuery({
    queryKey: ['mcp-workbench'],
    queryFn: ({ signal }) => fetchMCPWorkbench(signal),
    retry: false,
  })

  return (
    <section className={styles.page}>
      <PageHeader title="MCP 安全扫描" description="在同一工作台创建受控扫描、跟踪执行状态，并查看已脱敏的风险摘要。" />
      {query.isPending ? <StatePanel state="loading" title="正在加载 MCP 安全扫描工作台" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? (
        <StatePanel state="forbidden" title="无权查看 MCP 安全扫描工作台" description="当前身份没有该工作台的数据权限。" />
      ) : null}
      {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? (
        <StatePanel state="error" title="暂时无法加载 MCP 安全扫描工作台" description="请检查网络或稍后重试。" actionLabel="重试" onAction={() => void query.refetch()} />
      ) : null}
      {query.data ? <WorkbenchContent activeTasks={query.data.active_tasks} recentRisks={query.data.recent_risks} metrics={query.data.metrics} canCreate={canCreate} /> : null}
    </section>
  )
}
