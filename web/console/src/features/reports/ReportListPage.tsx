/**
 * 功能：展示当前主体权限范围内的不可变报告分页台账。
 * 实现：从 URL 规范化服务端分页参数，以 TanStack Query 和原生表格读取安全摘要。
 * 输入：page 深链参数与 GET /platform/reports 白名单响应。
 * 输出：报告台账、分页及加载、空、403、失败状态。
 * 依赖：Fluent UI、React Query、React Router 与共享监管组件。
 */
import { Button, makeStyles, tokens } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

import { ApiError } from '../../shared/api/errors'
import { DataTable, type DataTableColumn } from '../../shared/components/DataTable'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { fetchReportList, type ReportSummaryView } from './api'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  pagination: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: tokens.spacingHorizontalM },
  link: { color: tokens.colorBrandForegroundLink },
  risk: { whiteSpace: 'nowrap' },
})

const taskTypeLabels = {
  mcp_scan: 'MCP 扫描', ai_infra_scan: 'AI 基础设施扫描',
  model_redteam_report: '模型红队评测', agent_scan: 'Agent 扫描',
} as const

const formatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'UTC', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
})

function pageFrom(value: string | null): number {
  if (!value || !/^\d+$/.test(value)) return 1
  const page = Number(value)
  return Number.isSafeInteger(page) && page >= 1 && page <= 1_000 ? page : 1
}

export function ReportListPage() {
  const styles = useStyles()
  const [searchParams, setSearchParams] = useSearchParams()
  const page = pageFrom(searchParams.get('page'))
  const normalized = page > 1 ? `page=${page}` : ''
  useEffect(() => {
    if (searchParams.toString() !== normalized) setSearchParams(normalized, { replace: true })
  }, [normalized, searchParams, setSearchParams])
  const query = useQuery({
    queryKey: ['reports', { page, pageSize: 20 }],
    queryFn: ({ signal }) => fetchReportList(page, 20, signal),
    retry: false,
  })
  const columns: readonly DataTableColumn<ReportSummaryView>[] = [
    { id: 'type', header: '任务类型', render: (report) => taskTypeLabels[report.task_type] },
    { id: 'score', header: '安全分', render: (report) => report.risk.score },
    { id: 'risk', header: '风险分布', render: (report) => <span className={styles.risk}>高 {report.risk.high} / 中 {report.risk.medium} / 低 {report.risk.low}</span> },
    { id: 'brand', header: '快照品牌', render: (report) => report.brand_product_name },
    { id: 'completed', header: '完成时间', render: (report) => formatter.format(new Date(report.completed_at)) },
    { id: 'action', header: '操作', render: (report) => (
      <Link className={styles.link} to={`/reports/${encodeURIComponent(report.id)}`} aria-label={`查看报告 ${report.id}`}>查看</Link>
    ) },
  ]

  return (
    <section className={styles.page}>
      <PageHeader title="安全报告" description="查看当前权限范围内已固化的安全快照，分页由服务端执行。" />
      {query.isPending ? <StatePanel state="loading" title="正在加载安全报告" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? <StatePanel state="forbidden" title="无权查看安全报告" /> : null}
      {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? (
        <StatePanel state="error" title="暂时无法加载安全报告" description="请稍后重试。" actionLabel="重试" onAction={() => void query.refetch()} />
      ) : null}
      {query.data?.items.length === 0 ? <StatePanel state="empty" title="暂无安全报告" description="扫描任务完成并生成报告后将在此显示。" /> : null}
      {query.data?.items.length ? <DataTable caption="不可变安全报告台账" columns={columns} rows={query.data.items} getRowKey={(report) => report.id} /> : null}
      {query.data ? (
        <nav className={styles.pagination} aria-label="报告分页">
          <span>共 {query.data.total} 条，第 {query.data.page} 页</span>
          <div>
            <Button appearance="secondary" disabled={page <= 1} onClick={() => setSearchParams(page > 2 ? { page: String(page - 1) } : {})}>上一页</Button>{' '}
            <Button appearance="secondary" disabled={page * query.data.page_size >= query.data.total} onClick={() => setSearchParams({ page: String(page + 1) })}>下一页</Button>
          </div>
        </nav>
      ) : null}
    </section>
  )
}
