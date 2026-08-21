/**
 * 功能：向管理员与审计员展示只读、服务端分页的治理审计台账。
 * 实现：筛选条件进入 GET 查询；页面只使用适配层白名单 DTO，不渲染 metadata、IP 或请求标识。
 * 输入：当前 URL 分页、用户输入的四个服务端筛选条件和审计 API 响应。
 * 输出：原生审计表格及加载、空、无权限与失败状态。
 * 依赖：Fluent UI、TanStack Query、React Router 和共享台账组件。
 */
import { Button, Input, makeStyles, tokens } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import { ApiError } from '../../../shared/api/errors'
import { DataTable, type DataTableColumn } from '../../../shared/components/DataTable'
import { PageHeader } from '../../../shared/components/PageHeader'
import { StatePanel } from '../../../shared/components/StatePanel'
import { fetchAuditEvents, type AuditEventView } from '../api'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  filters: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(12rem, 1fr))', gap: tokens.spacingVerticalS, alignItems: 'end' },
  pagination: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: tokens.spacingHorizontalM },
})

function pageFrom(value: string | null): number {
  if (!value || !/^\d+$/.test(value)) return 1
  const page = Number(value)
  return Number.isSafeInteger(page) && page >= 1 && page <= 1_000 ? page : 1
}

function filterValue(value: string | null): string {
  return value && value.length <= 256 ? value : ''
}

function setIfPresent(values: URLSearchParams, key: string, value: string) {
  if (value) values.set(key, value)
}

export function AuditListPage() {
  const styles = useStyles()
  const [searchParams, setSearchParams] = useSearchParams()
  const page = pageFrom(searchParams.get('page'))
  const action = filterValue(searchParams.get('action'))
  const actorUserID = filterValue(searchParams.get('actor_user_id'))
  const resourceType = filterValue(searchParams.get('resource_type'))
  const resourceID = filterValue(searchParams.get('resource_id'))
  const [draft, setDraft] = useState({ action, actorUserID, resourceType, resourceID })

  useEffect(() => setDraft({ action, actorUserID, resourceType, resourceID }), [action, actorUserID, resourceType, resourceID])

  const query = useQuery({
    queryKey: ['audit-events', { page, pageSize: 20, action, actorUserID, resourceType, resourceID }],
    queryFn: ({ signal }) => fetchAuditEvents({ page, pageSize: 20 }, { action, actorUserID, resourceType, resourceID }, signal),
    retry: false,
  })

  const setPage = (nextPage: number) => {
    const values = new URLSearchParams()
    if (nextPage > 1) values.set('page', String(nextPage))
    setIfPresent(values, 'action', action)
    setIfPresent(values, 'actor_user_id', actorUserID)
    setIfPresent(values, 'resource_type', resourceType)
    setIfPresent(values, 'resource_id', resourceID)
    setSearchParams(values)
  }

  const applyFilters = () => {
    const values = new URLSearchParams()
    setIfPresent(values, 'action', draft.action.trim())
    setIfPresent(values, 'actor_user_id', draft.actorUserID.trim())
    setIfPresent(values, 'resource_type', draft.resourceType.trim())
    setIfPresent(values, 'resource_id', draft.resourceID.trim())
    setSearchParams(values)
  }

  const columns: readonly DataTableColumn<AuditEventView>[] = [
    { id: 'time', header: '发生时间', render: (event) => event.occurred_at },
    { id: 'actor', header: '操作者', render: (event) => event.actor_username ?? event.actor_user_id ?? '系统' },
    { id: 'action', header: '动作', render: (event) => event.action },
    { id: 'resource', header: '资源', render: (event) => event.resource_type && event.resource_id ? `${event.resource_type}: ${event.resource_id}` : event.resource_type ?? '未关联' },
    { id: 'outcome', header: '结果', render: (event) => event.outcome === 'success' ? '成功' : event.outcome === 'failure' ? '失败' : '处理中' },
  ]

  return (
    <section className={styles.page}>
      <PageHeader title="审计事件" description="审计台账仅显示受治理摘要；metadata、客户端 IP 和请求标识不会进入浏览器。" />
      <form className={styles.filters} onSubmit={(event) => { event.preventDefault(); applyFilters() }}>
        <label>动作筛选<Input value={draft.action} onChange={(_, data) => setDraft((current) => ({ ...current, action: data.value }))} /></label>
        <label>操作者 ID<Input value={draft.actorUserID} onChange={(_, data) => setDraft((current) => ({ ...current, actorUserID: data.value }))} /></label>
        <label>资源类型<Input value={draft.resourceType} onChange={(_, data) => setDraft((current) => ({ ...current, resourceType: data.value }))} /></label>
        <label>资源 ID<Input value={draft.resourceID} onChange={(_, data) => setDraft((current) => ({ ...current, resourceID: data.value }))} /></label>
        <Button appearance="secondary" type="submit">应用筛选</Button>
      </form>

      {query.isPending ? <StatePanel state="loading" title="正在加载审计事件" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? <StatePanel state="forbidden" title="无权查看审计事件" /> : null}
      {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? (
        <StatePanel state="error" title="暂时无法加载审计事件" description="请稍后重试。" actionLabel="重试" onAction={() => void query.refetch()} />
      ) : null}
      {query.data?.items.length === 0 ? <StatePanel state="empty" title="没有匹配的审计事件" description="可调整筛选条件或稍后再试。" /> : null}
      {query.data?.items.length ? <DataTable caption="治理审计台账" columns={columns} rows={query.data.items} getRowKey={(event) => event.id} /> : null}
      {query.data ? (
        <nav className={styles.pagination} aria-label="审计事件分页">
          <span>共 {query.data.total} 条，第 {query.data.page} 页</span>
          <div>
            <Button appearance="secondary" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</Button>{' '}
            <Button appearance="secondary" disabled={page * query.data.page_size >= query.data.total} onClick={() => setPage(page + 1)}>下一页</Button>
          </div>
        </nav>
      ) : null}
    </section>
  )
}
