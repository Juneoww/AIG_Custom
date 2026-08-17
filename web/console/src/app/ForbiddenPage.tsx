/**
 * 功能：为已认证但无权访问的站内路径提供不泄露资源信息的反馈。
 * 实现：仅说明权限不足并提供返回治理总览操作，不显示隐藏模块名称。
 * 输入：当前无权路由。
 * 输出：语义化 403 状态页。
 * 依赖：React Router、PageHeader 与 StatePanel。
 */
import { Link } from 'react-router-dom'

import { PageHeader } from '../shared/components/PageHeader'
import { StatePanel } from '../shared/components/StatePanel'

export function ForbiddenPage() {
  return (
    <section>
      <PageHeader title="无权访问" description="当前账号没有访问此页面的权限。" />
      <StatePanel state="forbidden" title="访问受限" description="如需访问，请联系平台管理员核对角色权限。" />
      <Link to="/">返回治理总览</Link>
    </section>
  )
}
