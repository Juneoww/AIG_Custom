/**
 * 功能：展示受控规则数据同步状态，并仅允许管理员发起异步同步。
 * 实现：状态读取对管理员和审计员开放，触发按钮按会话角色隐藏；失败消息使用 API 固定安全文本。
 * 输入：当前会话、GET/POST update-data 的遗留状态信封和浏览器 AbortSignal。
 * 输出：同步状态、最近安全摘要及受 CSRF 保护的单次触发操作。
 * 依赖：Fluent UI、TanStack Query、Session 与管理 API 适配层。
 */
import { Button, MessageBar, MessageBarBody, makeStyles, tokens } from '@fluentui/react-components'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'

import { useSession } from '../../auth/session'
import { ApiError } from '../../../shared/api/errors'
import { PageHeader } from '../../../shared/components/PageHeader'
import { StatePanel } from '../../../shared/components/StatePanel'
import { fetchSystemStatus, triggerSystemSync } from '../api'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  details: { display: 'grid', gridTemplateColumns: 'max-content minmax(0, 1fr)', gap: tokens.spacingVerticalS, margin: 0 },
  term: { color: tokens.colorNeutralForeground2 },
  actions: { display: 'flex', gap: tokens.spacingHorizontalS },
})

export function SystemPage() {
  const styles = useStyles()
  const { state } = useSession()
  const client = useQueryClient()
  const [notice, setNotice] = useState('')
  const [syncing, setSyncing] = useState(false)
  const mutex = useRef(false)
  const query = useQuery({ queryKey: ['system-update-status'], queryFn: ({ signal }) => fetchSystemStatus(signal), retry: false })
  const canTrigger = state.status === 'authenticated' && state.subject.role === 'admin'

  const trigger = async () => {
    if (!canTrigger || mutex.current) return
    mutex.current = true
    setSyncing(true)
    setNotice('')
    try {
      const next = await triggerSystemSync()
      client.setQueryData(['system-update-status'], next)
      setNotice(next.running ? '数据同步已开始。' : next.message)
    } catch {
      setNotice('无法发起数据同步，请显式重试。')
    } finally {
      mutex.current = false
      setSyncing(false)
    }
  }

  return (
    <section className={styles.page}>
      <PageHeader title="系统信息" description="管理员可发起规则数据同步；审计员可读取当前受控同步状态。">
        {canTrigger ? <Button appearance="primary" disabled={syncing} onClick={() => void trigger()}>{syncing ? '正在发起' : '发起数据同步'}</Button> : null}
      </PageHeader>
      {query.isPending ? <StatePanel state="loading" title="正在加载同步状态" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? <StatePanel state="forbidden" title="无权查看系统状态" /> : null}
      {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? <StatePanel state="error" title="暂时无法加载同步状态" actionLabel="重试" onAction={() => void query.refetch()} /> : null}
      {query.data ? (
        <dl className={styles.details}>
          <dt className={styles.term}>状态</dt><dd>{query.data.running ? '同步进行中' : query.data.success === false ? '同步未完成' : '空闲'}</dd>
          <dt className={styles.term}>已更新文件</dt><dd>{query.data.files_updated}</dd>
          <dt className={styles.term}>受控引用</dt><dd>{query.data.ref ?? '未提供'}</dd>
          <dt className={styles.term}>状态说明</dt><dd>{query.data.message || '未提供'}</dd>
        </dl>
      ) : null}
      {notice ? <MessageBar intent={notice.includes('无法') ? 'error' : 'info'} role="alert"><MessageBarBody>{notice}</MessageBarBody></MessageBar> : null}
    </section>
  )
}
