/**
 * 功能：呈现 AI 基础设施扫描专属的安全任务台账表格。
 * 实现：使用语义 Fluent 表格、精确状态标签和受控详情路径，不修改通用 DataTable。
 * 输入：安全任务摘要及可选的详情路径生成函数。
 * 输出：六列、可横向滚动的专属任务表格。
 * 依赖：React Router、Fluent UI、任务 DTO 与共享工作台样式。
 */
import {
  Button,
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableHeaderCell,
  TableRow,
  Text,
  mergeClasses,
} from '@fluentui/react-components'
import { Link } from 'react-router-dom'

import type { TaskStatus, TaskSummary } from '../../../shared/api/types'
import { useAIInfraWorkbenchStyles } from './AIInfraWorkbench.styles'

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

const taskTimeFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'UTC',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
  hour12: false,
})

function formatTaskTime(value: string): string {
  return taskTimeFormatter.format(new Date(value))
}

function statusTone(styles: ReturnType<typeof useAIInfraWorkbenchStyles>, status: TaskStatus): string | undefined {
  if (status === 'running') return styles.statusRunning
  if (status === 'pending' || status === 'dispatching') return styles.statusWaiting
  if (status === 'failed' || status === 'dispatch_failed' || status === 'dispatch_unknown') return styles.statusAttention
  return undefined
}

interface AIInfraTaskTableProps {
  tasks: readonly TaskSummary[]
  label?: string
  detailPath?: (task: TaskSummary) => string
  pagination?: {
    total: number
    page: number
    pageSize: number
    onNextPage?: () => void
    onPreviousPage?: () => void
  }
}

export function AIInfraTaskTable({ tasks, detailPath, pagination, label = 'AI 基础设施扫描' }: AIInfraTaskTableProps) {
  const styles = useAIInfraWorkbenchStyles()
  const pathOf = detailPath ?? ((task: TaskSummary) => `/tasks/ai-infra/${encodeURIComponent(task.id)}`)

  return (
    <section className={styles.tableShell} aria-label={`${label}任务列表`}>
      <div
        className={styles.tableViewport}
        role="region"
        aria-label={`可横向滚动的 ${label}任务表格`}
        tabIndex={0}
      >
        <Table className={styles.table}>
          <caption className={styles.tableCaption}>{label}任务台账</caption>
          <TableHeader>
            <TableRow>
              {['任务 ID', '负责人', '状态', '创建时间', '更新时间', '操作'].map((header) => (
                <TableHeaderCell className={styles.tableHeader} key={header} scope="col">{header}</TableHeaderCell>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {tasks.map((task) => (
              <TableRow key={task.id}>
                <TableCell className={mergeClasses(styles.tableCell, styles.identifier)}>{task.id}</TableCell>
                <TableCell className={styles.tableCell}>{task.owner}</TableCell>
                <TableCell className={styles.tableCell}>
                  <Text className={mergeClasses(styles.statusTag, statusTone(styles, task.status))}>{taskStatusLabels[task.status]}</Text>
                </TableCell>
                <TableCell className={styles.tableCell}>{formatTaskTime(task.created_at)}</TableCell>
                <TableCell className={styles.tableCell}>{formatTaskTime(task.updated_at)}</TableCell>
                <TableCell className={styles.tableCell}>
                  <Link className={styles.taskLink} to={pathOf(task)} aria-label={`查看任务 ${task.id}`}>查看</Link>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {pagination ? (
        <nav className={styles.tablePagination} aria-label={`${label}任务分页`}>
          <div className={styles.tablePaginationMeta}>
            <span>共 {pagination.total} 条</span>
            <span>第 {pagination.page} 页</span>
            <span>{pagination.pageSize} 条/页</span>
          </div>
          <div className={styles.tablePaginationActions}>
            <Button
              appearance="secondary"
              disabled={!pagination.onPreviousPage || pagination.page <= 1}
              onClick={pagination.onPreviousPage}
            >
              上一页
            </Button>
            <Button
              appearance="secondary"
              disabled={!pagination.onNextPage || pagination.page * pagination.pageSize >= pagination.total}
              onClick={pagination.onNextPage}
            >
              下一页
            </Button>
          </div>
        </nav>
      ) : null}
    </section>
  )
}
