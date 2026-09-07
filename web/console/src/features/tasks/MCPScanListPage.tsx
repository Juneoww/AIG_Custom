/** 功能：呈现 MCP 专属扫描历史；实现：仅读取 MCP 分页摘要；输入：页码/状态；输出：安全表格与专属详情导航。 */
import { Button, Field, Select, Text } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../../shared/api/errors'
import { DataTable } from '../../shared/components/DataTable'
import { StatePanel } from '../../shared/components/StatePanel'
import { useSession } from '../auth/session'
import { AIInfraWorkbenchHeader } from './components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from './components/AIInfraWorkbench.styles'
import { fetchMCPScans, mcpSourceLabels, mcpStatusLabels } from './mcpScansApi'

export function MCPScanListPage() {
  const viewStyles = useAIInfraWorkbenchStyles()
  const { state } = useSession()
  const [page, setPage] = useState(1)
  const [status, setStatus] = useState('')
  const query = useQuery({ queryKey: ['mcp-scans', page, status], queryFn: ({ signal }) => fetchMCPScans(page, status, signal), retry: false })
  return <section className={viewStyles.page}>
    <AIInfraWorkbenchHeader title="MCP 扫描历史" description="查看 MCP 扫描状态与报告。" backLink={{ to: '/tasks/mcp', label: '返回 MCP 工作台' }} />
    {state.status === 'authenticated' && state.subject.role !== 'auditor' ? <div><Link to="/tasks/mcp/new" className={viewStyles.primaryAction}>新建 MCP 扫描</Link></div> : null}
    <Field label="扫描状态"><Select value={status} onChange={(_, data) => { setStatus(data.value); setPage(1) }}><option value="">全部状态</option>{Object.entries(mcpStatusLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</Select></Field>
    {query.isPending ? <StatePanel state="loading" title="正在加载 MCP 扫描历史" /> : null}
    {query.isError ? <StatePanel state={query.error instanceof ApiError && query.error.status === 403 ? 'forbidden' : 'error'} title="无法读取 MCP 扫描历史" actionLabel="重新加载" onAction={() => void query.refetch()} /> : null}
    {query.isSuccess && query.data.items.length === 0 ? <StatePanel state="empty" title="暂无 MCP 扫描记录" description="调整状态筛选或创建新的 MCP 扫描。" /> : null}
    {query.isSuccess && query.data.items.length > 0 ? <div className={viewStyles.surface}><DataTable caption="MCP 扫描历史" rows={query.data.items} getRowKey={(row) => row.id} columns={[
      { id: 'id', header: '扫描任务', render: (row) => <Link to={`/tasks/mcp/${encodeURIComponent(row.id)}`}>MCP 扫描 · {row.id}</Link> },
      { id: 'source', header: '来源', render: (row) => mcpSourceLabels[row.source_kind] },
      { id: 'status', header: '状态', render: (row) => mcpStatusLabels[row.status] },
      { id: 'updated', header: '更新时间', render: (row) => new Date(row.updated_at).toLocaleString('zh-CN') },
    ]} /></div> : null}
    {query.isSuccess ? <div className={viewStyles.attachmentControls}><Text>共 {query.data.total} 条 · 第 {page} 页</Text><Button disabled={page <= 1} onClick={() => setPage((value) => value - 1)}>上一页</Button><Button disabled={page * query.data.page_size >= query.data.total} onClick={() => setPage((value) => value + 1)}>下一页</Button></div> : null}
  </section>
}
