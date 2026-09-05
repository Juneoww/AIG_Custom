/**
 * 功能：展示按当前主体限定的任务分页台账和服务端精确筛选。
 * 实现：用 TanStack Query 将分页、状态和类型传给真实列表 API，并以原生表格呈现安全摘要。
 * 输入：当前会话角色和 GET /platform/tasks 的分页响应。
 * 输出：任务台账、筛选、分页及独立加载/空/失败/403状态。
 * 依赖：Fluent UI、React Query、React Router 与共享监管组件。
 */
import { AddRegular } from '@fluentui/react-icons'
import { Button, Field, Select, makeStyles, mergeClasses, tokens } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

import { useSession } from '../auth/session'
import { ApiError } from '../../shared/api/errors'
import type { TaskStatus, TaskSummary, TaskType } from '../../shared/api/types'
import { DataTable, type DataTableColumn } from '../../shared/components/DataTable'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { fetchTaskList } from './api'
import { AIInfraTaskOperationsSummary } from './components/AIInfraTaskOperationsSummary'
import { AIInfraTaskTable } from './components/AIInfraTaskTable'
import { AIInfraWorkbenchHeader } from './components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from './components/AIInfraWorkbench.styles'
import { TaskOperationsSummary } from './components/TaskOperationsSummary'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL, minWidth: 0 },
  headerAction: { color: tokens.colorBrandForegroundLink, fontWeight: tokens.fontWeightSemibold },
  filterPanel: {
    minWidth: 0,
    padding: tokens.spacingVerticalL,
    border: `1px solid ${tokens.colorNeutralStroke1}`,
    borderRadius: tokens.borderRadiusLarge,
    backgroundColor: tokens.colorNeutralBackground2,
  },
  filters: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: tokens.spacingHorizontalL,
    alignItems: 'end',
    minWidth: 0,
    '@media (max-width: 960px)': {
      flexDirection: 'column',
      alignItems: 'stretch',
      gap: tokens.spacingVerticalM,
    },
  },
  filter: {
    flexGrow: 1,
    minWidth: '200px',
    '@media (max-width: 960px)': {
      minWidth: 0,
      width: '100%',
    },
  },
  select: { minWidth: 0, width: '100%' },
  tableViewport: { minWidth: 0, overflowX: 'auto' },
  pagination: {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: tokens.spacingHorizontalM,
    '@media (max-width: 960px)': {
      flexDirection: 'column',
      alignItems: 'stretch',
    },
  },
  paginationActions: { display: 'flex', flexWrap: 'wrap', gap: tokens.spacingHorizontalS },
  taskLink: { color: tokens.colorBrandForegroundLink },
  statusMark: {
    display: 'inline-flex',
    alignItems: 'center',
    padding: `${tokens.spacingVerticalXXS} ${tokens.spacingHorizontalS}`,
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    borderRadius: tokens.borderRadiusCircular,
    fontWeight: tokens.fontWeightSemibold,
    whiteSpace: 'nowrap',
  },
  statusActive: {
    border: `1px solid ${tokens.colorBrandStroke1}`,
    backgroundColor: tokens.colorBrandBackground2,
    color: tokens.colorBrandForeground1,
  },
  statusPending: {
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    backgroundColor: tokens.colorNeutralBackground3,
    color: tokens.colorNeutralForeground2,
  },
  statusAttention: {
    border: `1px solid ${tokens.colorStatusWarningBorder1}`,
    backgroundColor: tokens.colorStatusWarningBackground1,
    color: tokens.colorStatusWarningForeground1,
  },
  statusTerminal: {
    border: `1px solid ${tokens.colorNeutralStroke1}`,
    backgroundColor: tokens.colorNeutralBackground1,
    color: tokens.colorNeutralForeground2,
  },
})

export const taskTypeLabels: Record<TaskType, string> = {
  mcp_scan: 'MCP 扫描',
  ai_infra_scan: 'AI 基础设施扫描',
  model_redteam_report: '模型红队评测',
  agent_scan: 'Agent 扫描',
  unknown: '未知任务',
}

export const taskStatusLabels: Record<TaskStatus, string> = {
  pending: '等待调度',
  dispatching: '正在调度',
  running: '执行中',
  succeeded: '已完成',
  failed: '执行失败',
  dispatch_failed: '调度失败',
  dispatch_unknown: '调度状态待确认',
  cancelled: '已取消',
}

const formatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'UTC',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

const selectableStatuses = new Set<TaskStatus>(Object.keys(taskStatusLabels) as TaskStatus[])
const selectableTaskTypes = new Set<Exclude<TaskType, 'unknown'>>(
  (Object.keys(taskTypeLabels) as TaskType[]).filter((value): value is Exclude<TaskType, 'unknown'> => value !== 'unknown'),
)

function positivePage(value: string | null): number {
  if (!value || !/^\d+$/.test(value)) return 1
  const page = Number(value)
  return Number.isSafeInteger(page) && page >= 1 && page <= 1_000 ? page : 1
}

function normalizedTaskSearch(page: number, status?: TaskStatus, taskType?: Exclude<TaskType, 'unknown'>): string {
  const next = new URLSearchParams()
  if (page > 1) next.set('page', String(page))
  if (status) next.set('status', status)
  if (taskType) next.set('task_type', taskType)
  return next.toString()
}

export function formatTaskTime(value: string): string {
  return formatter.format(new Date(value))
}

interface TaskListPageProps {
  fixedTaskType?: Exclude<TaskType, 'unknown'>
}

export function TaskListPage({ fixedTaskType }: TaskListPageProps) {
  const styles = useStyles()
  const aiStyles = useAIInfraWorkbenchStyles()
  const { state } = useSession()
  const isAiInfraList = fixedTaskType === 'ai_infra_scan'
  const [searchParams, setSearchParams] = useSearchParams()
  const page = positivePage(searchParams.get('page'))
  const statusValue = searchParams.get('status') as TaskStatus | null
  const taskTypeValue = searchParams.get('task_type') as Exclude<TaskType, 'unknown'> | null
  const status = statusValue && selectableStatuses.has(statusValue) ? statusValue : undefined
  const taskType = fixedTaskType ?? (taskTypeValue && selectableTaskTypes.has(taskTypeValue) ? taskTypeValue : undefined)
  const normalizedSearch = normalizedTaskSearch(page, status, fixedTaskType ? undefined : taskType)
  useEffect(() => {
    if (searchParams.toString() !== normalizedSearch) setSearchParams(normalizedSearch, { replace: true })
  }, [normalizedSearch, searchParams, setSearchParams])
  const updateSearch = (
    nextPage: number,
    ...nextFilters: [] | [TaskStatus | undefined, Exclude<TaskType, 'unknown'> | undefined]
  ) => {
    const [nextStatus, nextTaskType] = nextFilters
    setSearchParams(normalizedTaskSearch(
      nextPage,
      nextFilters.length === 0 ? status : nextStatus,
      fixedTaskType ? undefined : nextFilters.length === 0 ? taskType : nextTaskType,
    ))
  }
  const query = useQuery({
    queryKey: ['tasks', { page, pageSize: 20, status, taskType }],
    queryFn: ({ signal }) => fetchTaskList({ page, pageSize: 20, status, taskType }, signal),
    retry: false,
  })
  const canCreate = state.status === 'authenticated' && state.subject.role !== 'auditor'
  const filterLabels = [
    status ? `状态：${taskStatusLabels[status]}` : '全部状态',
    taskType ? `类型：${taskTypeLabels[taskType]}` : '全部类型',
  ]
  const hasActiveFilters = Boolean(status || (!fixedTaskType && taskType))
  const statusMarkStyles: Record<TaskStatus, string> = {
    pending: styles.statusPending,
    dispatching: styles.statusActive,
    running: styles.statusActive,
    succeeded: styles.statusTerminal,
    failed: styles.statusAttention,
    dispatch_failed: styles.statusAttention,
    dispatch_unknown: styles.statusAttention,
    cancelled: styles.statusTerminal,
  }
  const columns: readonly DataTableColumn<TaskSummary>[] = isAiInfraList ? [
    { id: 'id', header: '任务 ID', render: (task) => task.id },
    { id: 'owner', header: '负责人', render: (task) => task.owner },
    {
      id: 'status',
      header: '状态',
      render: (task) => <span className={mergeClasses(styles.statusMark, statusMarkStyles[task.status])}>{taskStatusLabels[task.status]}</span>,
    },
    { id: 'created', header: '创建时间', render: (task) => formatTaskTime(task.created_at) },
    { id: 'updated', header: '更新时间', render: (task) => formatTaskTime(task.updated_at) },
    {
      id: 'action',
      header: '操作',
      render: (task) => (
        <Link className={styles.taskLink} to={`/tasks/ai-infra/${encodeURIComponent(task.id)}`} aria-label={`查看任务 ${task.id}`}>
          查看
        </Link>
      ),
    },
  ] : [
    { id: 'type', header: '任务类型', render: (task) => taskTypeLabels[task.task_type] },
    { id: 'owner', header: '负责人', render: (task) => task.owner },
    {
      id: 'status',
      header: '状态',
      render: (task) => <span className={mergeClasses(styles.statusMark, statusMarkStyles[task.status])}>{taskStatusLabels[task.status]}</span>,
    },
    { id: 'updated', header: '更新时间', render: (task) => formatTaskTime(task.updated_at) },
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

  const pageTitle = '扫描任务'
  const pageDescription = '按权限范围查看任务状态，筛选由服务端在分页前执行。'
  const tableCaption = '扫描任务台账'

  if (isAiInfraList) {
    return (
      <section className={aiStyles.page}>
        <AIInfraWorkbenchHeader
          action={canCreate ? (
            <Link className={aiStyles.primaryAction} to="/tasks/ai-infra/new" aria-label="新建 AI 基础设施扫描任务">
              <AddRegular aria-hidden="true" />
              <span>新建 AI 基础设施扫描任务</span>
            </Link>
          ) : undefined}
        />
        <div className={aiStyles.surface} role="group" aria-label="AI 基础设施扫描状态筛选">
          <div className={aiStyles.filterControls}>
            <Field className={aiStyles.filterField} label="任务状态">
              <Select
                value={status ?? ''}
                onChange={(_, data) => {
                  updateSearch(1, (data.value || undefined) as TaskStatus | undefined, taskType)
                }}
              >
                <option value="">全部状态</option>
                {Object.entries(taskStatusLabels).map(([value, label]) => (
                  <option key={value} value={value}>{label}</option>
                ))}
              </Select>
            </Field>
            {hasActiveFilters ? (
              <Button appearance="subtle" onClick={() => updateSearch(1, undefined, undefined)}>清除筛选</Button>
            ) : null}
          </div>
        </div>
        {query.isSuccess ? <AIInfraTaskOperationsSummary tasks={query.data.items} total={query.data.total} /> : null}
        {query.isPending ? <StatePanel state="loading" title="正在加载 AI 基础设施扫描任务" /> : null}
        {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? (
          <StatePanel state="forbidden" title="无权查看 AI 基础设施扫描任务台账" />
        ) : null}
        {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? (
          <StatePanel state="error" title="暂时无法加载 AI 基础设施扫描任务" description="请稍后重试。" actionLabel="重试" onAction={() => void query.refetch()} />
        ) : null}
        {query.data?.items.length === 0 ? <StatePanel state="empty" title="暂无匹配任务" description="调整状态筛选或创建新的扫描任务。" /> : null}
        {query.data && (query.data.items.length > 0 || query.data.total > 0) ? (
          <AIInfraTaskTable
            tasks={query.data.items}
            pagination={{
              total: query.data.total,
              page: query.data.page,
              pageSize: query.data.page_size,
              onPreviousPage: query.data.page > 1 ? () => updateSearch(query.data.page - 1) : undefined,
              onNextPage: query.data.page * query.data.page_size < query.data.total
                ? () => updateSearch(query.data.page + 1)
                : undefined,
            }}
          />
        ) : null}
      </section>
    )
  }

  return (
    <section className={styles.page}>
      <PageHeader
        title={pageTitle}
        description={pageDescription}
      >
        {canCreate ? (
          <Link
            className={styles.headerAction}
            to="/tasks/new"
            aria-label="创建扫描任务"
          >
            创建任务
          </Link>
        ) : null}
      </PageHeader>
      <div className={styles.filterPanel} role="group" aria-label="任务筛选">
        <div className={styles.filters}>
          <Field className={styles.filter} label="任务状态">
            <Select
              className={styles.select}
              value={status ?? ''}
              onChange={(_, data) => {
                updateSearch(1, (data.value || undefined) as TaskStatus | undefined, taskType)
              }}
            >
              <option value="">全部状态</option>
              {Object.entries(taskStatusLabels).map(([value, label]) => (
                <option key={value} value={value}>{label}</option>
              ))}
            </Select>
          </Field>
          {!fixedTaskType ? (
            <Field className={styles.filter} label="任务类型">
              <Select
                className={styles.select}
                value={taskType ?? ''}
                onChange={(_, data) => {
                  updateSearch(1, status, (data.value || undefined) as Exclude<TaskType, 'unknown'> | undefined)
                }}
              >
                <option value="">全部类型</option>
                {Object.entries(taskTypeLabels).filter(([value]) => value !== 'unknown').map(([value, label]) => (
                  <option key={value} value={value}>{label}</option>
                ))}
              </Select>
            </Field>
          ) : null}
        </div>
      </div>
      {query.isSuccess ? (
        <TaskOperationsSummary
          tasks={query.data.items}
          total={query.data.total}
          filterLabels={filterLabels}
          onClearFilters={hasActiveFilters ? () => updateSearch(1, undefined, undefined) : undefined}
        />
      ) : null}
      {query.isPending ? <StatePanel state="loading" title="正在加载扫描任务" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? (
        <StatePanel state="forbidden" title="无权查看任务台账" />
      ) : null}
      {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? (
        <StatePanel state="error" title="暂时无法加载任务" description="请稍后重试。" actionLabel="重试" onAction={() => void query.refetch()} />
      ) : null}
      {query.data?.items.length === 0 ? <StatePanel state="empty" title="暂无匹配任务" description="调整筛选条件或创建新的扫描任务。" /> : null}
      {query.data?.items.length ? (
        <div className={styles.tableViewport}>
          <DataTable caption={tableCaption} columns={columns} rows={query.data.items} getRowKey={(task) => task.id} />
        </div>
      ) : null}
      {query.data ? (
        <nav className={styles.pagination} aria-label="任务分页">
          <span>共 {query.data.total} 条，第 {query.data.page} 页</span>
          <div className={styles.paginationActions}>
            <Button appearance="secondary" disabled={page <= 1} onClick={() => updateSearch(page - 1)}>上一页</Button>
            <Button appearance="secondary" disabled={page * query.data.page_size >= query.data.total} onClick={() => updateSearch(page + 1)}>下一页</Button>
          </div>
        </nav>
      ) : null}
    </section>
  )
}
