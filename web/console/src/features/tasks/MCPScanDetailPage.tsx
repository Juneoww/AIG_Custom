/** 功能：呈现安全 MCP 扫描详情及确认取消流程；实现：专属查询、有限轮询和未知取消显式重试；输出：安全状态与报告链接。 */
import { Button, Dialog, DialogActions, DialogBody, DialogContent, DialogSurface, DialogTitle, MessageBar, MessageBarBody, Text } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../../shared/api/errors'
import { StatePanel } from '../../shared/components/StatePanel'
import { useSession } from '../auth/session'
import { isMCPUnknownOutcome, mcpErrorMessage, type MCPMutation } from '../mcp-connections/api'
import { AIInfraWorkbenchHeader } from './components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from './components/AIInfraWorkbench.styles'
import { createMCPCancelSubmission, fetchMCPScan, isMCPScanTerminal, mcpSourceLabels, mcpStatusLabels, type MCPScanResult } from './mcpScansApi'

export function MCPScanDetailPage() {
  const { taskId = '' } = useParams()
  return <MCPScanDetail key={taskId} taskId={taskId} />
}
function MCPScanDetail({ taskId }: { taskId: string }) {
  const viewStyles = useAIInfraWorkbenchStyles()
  const { state } = useSession()
  const [confirm, setConfirm] = useState(false)
  const [pending, setPending] = useState(false)
  const [unknown, setUnknown] = useState(false)
  const [error, setError] = useState('')
  const operation = useRef<MCPMutation<MCPScanResult> | null>(null)
  const controller = useRef<AbortController | null>(null)
  const mounted = useRef(true)
  const locked = useRef(false)
  const query = useQuery({ queryKey: ['mcp-scan', taskId], queryFn: ({ signal }) => fetchMCPScan(taskId, signal), retry: false,
    refetchInterval: (current) => current.state.error || !current.state.data || isMCPScanTerminal(current.state.data.status) || current.state.dataUpdateCount >= 8 ? false : 4000,
  })
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; controller.current?.abort(); operation.current?.clear(); operation.current = null } }, [])
  const cancel = async () => {
    if (locked.current) return
    locked.current = true; setPending(true); setError(''); setConfirm(false)
    const request = new AbortController(); controller.current = request
    try {
      operation.current ??= createMCPCancelSubmission(taskId)
      await operation.current.submit(request.signal)
      if (!mounted.current || request.signal.aborted) return
      operation.current.clear(); operation.current = null; setUnknown(false); await query.refetch()
    } catch (caught) { if (mounted.current && !request.signal.aborted) { setUnknown(isMCPUnknownOutcome(caught)); setError(mcpErrorMessage(caught)); if (!isMCPUnknownOutcome(caught)) { operation.current?.clear(); operation.current = null } } }
    finally { locked.current = false; if (mounted.current) setPending(false) }
  }
  const task = query.isSuccess ? query.data : undefined
  const canCancel = state.status === 'authenticated' && state.subject.role !== 'auditor' && task && !isMCPScanTerminal(task.status)
  return <section className={viewStyles.page}>
    <AIInfraWorkbenchHeader title="MCP 扫描详情" description="跟踪扫描状态并查看安全报告。" backLink={{ to: '/tasks/mcp', label: '返回 MCP 工作台' }} />
    {query.isPending ? <StatePanel state="loading" title="正在加载 MCP 扫描详情" /> : null}
    {query.isError ? <StatePanel state={query.error instanceof ApiError && query.error.status === 403 ? 'forbidden' : 'error'} title="无法读取 MCP 扫描详情" actionLabel="重新加载" onAction={() => void query.refetch()} /> : null}
    {error ? <MessageBar intent="error"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
    {task ? <section className={viewStyles.surface}><h2 className={viewStyles.sectionHeading}>扫描状态</h2><div className={viewStyles.creationForm}><Text>任务编号：{task.id}</Text><Text role="status">{mcpStatusLabels[task.status]}</Text><Text>扫描来源：{mcpSourceLabels[task.source_kind]}</Text>{task.source_kind !== 'legacy_unknown' ? <Text>分析方式：{task.input_summary.model_id ? '模型辅助分析' : '基础检查（未使用模型）'}</Text> : null}<Text>报告语言：中文</Text>{task.input_summary.thread ? <Text>并发数：{task.input_summary.thread}</Text> : null}<Text>更新时间：{new Date(task.updated_at).toLocaleString('zh-CN')}</Text><div className={viewStyles.attachmentControls}>
      <Button disabled={query.isFetching || pending} onClick={() => void query.refetch()}>刷新状态</Button>
      {canCancel && !unknown ? <Button disabled={pending} onClick={() => setConfirm(true)}>{pending ? '正在取消' : '取消扫描'}</Button> : null}
      {unknown ? <Button disabled={pending} onClick={() => void cancel()}>{pending ? '正在确认取消' : '重试同一取消请求'}</Button> : null}
      {task.status === 'succeeded' && task.report_id ? <Link className={viewStyles.primaryAction} to={`/reports/${encodeURIComponent(task.report_id)}`}>查看安全报告</Link> : null}
      <Link to="/tasks/mcp/scans">查看 MCP 扫描历史</Link>
    </div></div></section> : null}
    <Dialog open={confirm} onOpenChange={(_, data) => { if (!pending) setConfirm(data.open) }}><DialogSurface><DialogBody><DialogTitle>确认取消 MCP 扫描</DialogTitle><DialogContent>取消后将停止当前扫描。是否继续？</DialogContent><DialogActions><Button onClick={() => setConfirm(false)}>继续扫描</Button><Button appearance="primary" disabled={pending} onClick={() => void cancel()}>确认取消</Button></DialogActions></DialogBody></DialogSurface></Dialog>
  </section>
}
