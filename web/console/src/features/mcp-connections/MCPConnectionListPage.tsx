/** 功能：呈现 MCP 连接配置安全台账；实现：仅缓存摘要，按角色提供管理入口；输入/输出：安全 DTO 与站内导航。 */
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { ApiError } from '../../shared/api/errors'
import { DataTable } from '../../shared/components/DataTable'
import { StatePanel } from '../../shared/components/StatePanel'
import { useSession } from '../auth/session'
import { AIInfraWorkbenchHeader } from '../tasks/components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from '../tasks/components/AIInfraWorkbench.styles'
import { fetchConnections } from './api'

export function MCPConnectionListPage() {
  const viewStyles = useAIInfraWorkbenchStyles()
  const { state } = useSession()
  const canManage = state.status === 'authenticated' && state.subject.role !== 'auditor'
  const query = useQuery({ queryKey: ['mcp-connections'], queryFn: ({ signal }) => fetchConnections(signal), retry: false })
  return <section className={viewStyles.page}>
    <AIInfraWorkbenchHeader title="MCP 连接配置" description="保存连接材料，测试通过后启用，供 MCP 服务扫描选择。" />
    {canManage ? <div><Link className={viewStyles.primaryAction} to="/credentials/mcp-connections/new">新建 MCP 连接</Link></div> : null}
    {query.isPending ? <StatePanel state="loading" title="正在加载 MCP 连接配置" /> : null}
    {query.isError ? <StatePanel state={query.error instanceof ApiError && query.error.status === 403 ? 'forbidden' : 'error'} title="无法读取 MCP 连接配置" actionLabel="重新加载" onAction={() => void query.refetch()} /> : null}
    {query.isSuccess && query.data.length === 0 ? <StatePanel state="empty" title="暂无 MCP 连接配置" description="新建连接、完成测试并启用后，即可创建服务扫描。" /> : null}
    {query.isSuccess && query.data.length > 0 ? <div className={viewStyles.surface}><DataTable caption="MCP 连接配置" rows={query.data} getRowKey={(row) => row.id} columns={[
      { id: 'name', header: '名称', render: (row) => canManage ? <Link to={`/credentials/mcp-connections/${encodeURIComponent(row.id)}`}>{row.name}</Link> : row.name },
      { id: 'description', header: '说明', render: (row) => row.description || '未填写' },
      { id: 'transport', header: '传输协议', render: (row) => row.detected_transport ?? row.transport },
      { id: 'authentication', header: '认证方式', render: (row) => ({ none: '无需认证', bearer: 'Bearer Token', api_key_header: 'API Key 请求头', custom_headers: '自定义请求头', unknown: '认证状态不可用' })[row.authentication_kind] },
      { id: 'version', header: '版本', render: (row) => row.current_version },
      { id: 'test', header: '连接测试', render: (row) => row.probe_status === 'passed' ? '测试通过' : row.probe_status === 'failed' ? '测试失败' : '待测试' },
      { id: 'enabled', header: '状态', render: (row) => row.enabled ? '已启用' : '未启用' },
    ]} /></div> : null}
  </section>
}
