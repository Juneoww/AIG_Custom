/**
 * 功能：为已认证用户的未知控制台地址提供明确 404 反馈。
 * 实现：展示通用不存在说明并提供返回治理总览的站内链接。
 * 输入：无法匹配的已认证路由。
 * 输出：语义化 404 状态页。
 * 依赖：React Router、PageHeader 与 StatePanel。
 */
import { Link } from 'react-router-dom'

import { PageHeader } from '../shared/components/PageHeader'
import { StatePanel } from '../shared/components/StatePanel'

export function NotFoundPage() {
  return (
    <section>
      <PageHeader title="页面不存在" description="该地址未对应到控制台中的页面。" />
      <StatePanel state="empty" title="未找到页面" description="请检查地址，或返回治理总览继续。" />
      <Link to="/">返回治理总览</Link>
    </section>
  )
}
