/**
 * 功能：展示按当前主体限定的任务分页台账和服务端精确筛选。
 * 实现：用 TanStack Query 将分页、状态和类型传给真实列表 API，并以原生表格呈现安全摘要。
 * 输入：当前会话角色和 GET /platform/tasks 的分页响应。
 * 输出：任务台账、筛选、分页及独立加载/空/失败/403状态。
 * 依赖：Fluent UI、React Query、React Router 与共享监管组件。
 */
import { Button, Field, Select, makeStyles, tokens } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router-dom'

import { useSession } from '../auth/session'
import { ApiError } from '../../shared/api/errors'
import type { TaskStatus, TaskSummary, TaskType } from '../../shared/api/types'
import { DataTable, type DataTableColumn } from '../../shared/components/DataTable'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { fetchTaskList } from './api'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  headerAction: { color: tokens.colorBrandForegroundLink, fontWeight: tokens.fontWeightSemibold },
  filters: { display: 'flex', flexWrap: 'wrap', gap: tokens.spacingHorizontalL, alignItems: 'end' },
  filter: { minWidth: '200px' },
  pagination: { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: tokens.spacingHorizontalM },
  taskLink: { color: tokens.colorBrandForegroundLink },
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

export function formatTaskTime(value: string): string {
  return formatter.format(new Date(value))
}

export function TaskListPage() {
  const styles = useStyles()
  const { state } = useSession()
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState<TaskStatus | undefined>()
  const [taskType, setTaskType] = useState<Exclude<TaskType, 'unknown'> | undefined>()
  const query = useQuery({
    queryKey: ['tasks', { page, pageSize: 20, status, taskType }],
    queryFn: ({ signal }) => fetchTaskList({ page, pageSize: 20, status, taskType }, signal),
    retry: false,
  })
  const canCreate = state.status === 'authenticated' && state.subject.role !== 'auditor'
  const columns: readonly DataTableColumn<TaskSummary>[] = [
    { id: 'type', header: '任务类型', render: (task) => taskTypeLabels[task.task_type] },
    { id: 'owner', header: '负责人', render: (task) => task.owner },
    { id: 'status', header: '状态', render: (task) => taskStatusLabels[task.status] },
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

  return (
    <section className={styles.page}>
      <PageHeader
        title="扫描任务"
        description="按权限范围查看任务状态，筛选由服务端在分页前执行。"
      >
        {canCreate ? (
          <Link className={styles.headerAction} to="/tasks/new" aria-label="创建扫描任务">
            创建任务
          </Link>
        ) : null}
      </PageHeader>
      <div className={styles.filters} aria-label="任务筛选">
        <Field className={styles.filter} label="任务状态">
          <Select
            value={status ?? ''}
            onChange={(_, data) => {
              setStatus((data.value || undefined) as TaskStatus | undefined)
              setPage(1)
            }}
          >
            <option value="">全部状态</option>
            {Object.entries(taskStatusLabels).map(([value, label]) => (
              <option key={value} value={value}>{label}</option>
            ))}
          </Select>
        </Field>
        <Field className={styles.filter} label="任务类型">
          <Select
            value={taskType ?? ''}
            onChange={(_, data) => {
              setTaskType((data.value || undefined) as Exclude<TaskType, 'unknown'> | undefined)
              setPage(1)
            }}
          >
            <option value="">全部类型</option>
            {Object.entries(taskTypeLabels).filter(([value]) => value !== 'unknown').map(([value, label]) => (
              <option key={value} value={value}>{label}</option>
            ))}
          </Select>
        </Field>
      </div>
      {query.isPending ? <StatePanel state="loading" title="正在加载扫描任务" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? (
        <StatePanel state="forbidden" title="无权查看任务台账" />
      ) : null}
      {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? (
        <StatePanel state="error" title="暂时无法加载任务" description="请稍后重试。" actionLabel="重试" onAction={() => void query.refetch()} />
      ) : null}
      {query.data?.items.length === 0 ? <StatePanel state="empty" title="暂无匹配任务" description="调整筛选条件或创建新的扫描任务。" /> : null}
      {query.data?.items.length ? (
        <DataTable caption="扫描任务台账" columns={columns} rows={query.data.items} getRowKey={(task) => task.id} />
      ) : null}
      {query.data ? (
        <nav className={styles.pagination} aria-label="任务分页">
          <span>共 {query.data.total} 条，第 {query.data.page} 页</span>
          <div>
            <Button appearance="secondary" disabled={page <= 1} onClick={() => setPage((current) => current - 1)}>上一页</Button>{' '}
            <Button appearance="secondary" disabled={page * query.data.page_size >= query.data.total} onClick={() => setPage((current) => current + 1)}>下一页</Button>
          </div>
        </nav>
      ) : null}
    </section>
  )
}
