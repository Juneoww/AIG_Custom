/** 功能：显示当前用户的基础设施凭据；实现：安全 DTO 查询和站内管理链接；输入：私有凭据摘要；输出：台账与加载状态。 */
import { Badge } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { DataTable } from '../../shared/components/DataTable'
import { StatePanel } from '../../shared/components/StatePanel'
import { AIInfraWorkbenchHeader } from '../tasks/components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from '../tasks/components/AIInfraWorkbench.styles'
import { authLabels, fetchCredentials } from './api'

export function TargetCredentialListPage() {
  const styles = useAIInfraWorkbenchStyles()
  const query = useQuery({ queryKey: ['target-credentials'], queryFn: ({ signal }) => fetchCredentials(signal), retry: false })
  return <section className={styles.page}>
    <AIInfraWorkbenchHeader title="基础设施凭据" description="管理扫描目标的访问认证。每份凭据仅供你使用，并绑定一个 HTTP 或 HTTPS 目标地址。" />
    <div><Link className={styles.primaryAction} to="/credentials/target-credentials/new">新建基础设施凭据</Link></div>
    {query.isPending ? <StatePanel state="loading" title="正在加载基础设施凭据" /> : null}
    {query.isError ? <StatePanel state="error" title="无法读取基础设施凭据" actionLabel="重新加载" onAction={() => void query.refetch()} /> : null}
    {query.isSuccess && query.data.length === 0 ? <StatePanel state="empty" title="暂无基础设施凭据" description="保存目标地址和认证信息后，可在 AI 基础设施扫描任务中选择。" /> : null}
    {query.isSuccess && query.data.length > 0 ? <div className={styles.surface}><DataTable caption="基础设施访问凭据" rows={query.data} getRowKey={(row) => row.id} columns={[
      { id: 'name', header: '名称', render: (row) => <Link to={`/credentials/target-credentials/${encodeURIComponent(row.id)}`}>{row.name}</Link> },
      { id: 'origin', header: '允许访问的目标', render: (row) => row.origin },
      { id: 'auth', header: '认证方式', render: (row) => authLabels[row.auth_type] },
      { id: 'status', header: '状态', render: (row) => <Badge appearance="tint" color={row.disabled ? 'informative' : 'success'}>{row.disabled ? '已停用' : '已启用'}</Badge> },
      { id: 'version', header: '版本', render: (row) => row.revision },
    ]} /></div> : null}
  </section>
}
