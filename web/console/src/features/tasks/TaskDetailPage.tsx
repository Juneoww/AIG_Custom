/**
 * 功能：展示任务安全详情、受控短轮询和角色化取消操作。
 * 实现：查询信号随路由/卸载取消，非终态按有界退避轮询，写入失败不自动重放。
 * 输入：路由中的 opaque 任务 ID、当前 Subject 与 TaskDetail。
 * 输出：安全输入摘要、状态、更新时间及允许角色的取消按钮。
 * 依赖：Fluent UI、React Query、React Router、Session 与任务 API。
 */
import { Button, Card, MessageBar, MessageBarBody, Text, makeStyles, tokens } from '@fluentui/react-components'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'

import { useSession } from '../auth/session'
import { ApiError } from '../../shared/api/errors'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { cancelTaskGoverned, fetchTaskDetail, taskPollDelay } from './api'
import { formatTaskTime, taskStatusLabels, taskTypeLabels } from './TaskListPage'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  panel: { padding: tokens.spacingVerticalL, boxShadow: 'none' },
  facts: { display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', gap: tokens.spacingHorizontalL },
  fact: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalXS },
  label: { color: tokens.colorNeutralForeground2 },
  back: { color: tokens.colorBrandForegroundLink },
})

const terminal = new Set(['succeeded', 'failed', 'cancelled'])
const portScanModeLabels = {
  fixed_ai: '固定 AI 端口（11434、1337、7000–9000、18789）',
  full_tcp: '全量 TCP 1–65535',
} as const

export function TaskDetailPage() {
  const styles = useStyles()
  const queryClient = useQueryClient()
  const { taskId = '' } = useParams<{ taskId: string }>()
  const { state } = useSession()
  const query = useQuery({
    queryKey: ['task', taskId],
    queryFn: ({ signal }) => fetchTaskDetail(taskId, signal),
    enabled: taskId.length > 0,
    retry: false,
    refetchInterval: (current) =>
      taskPollDelay(current.state.data, current.state.dataUpdateCount + current.state.errorUpdateCount),
  })
  const cancel = useMutation({
    mutationFn: () => cancelTaskGoverned(taskId),
    retry: false,
    onSuccess: (result) => {
      if (result.status === 'uncertain') {
        queryClient.setQueryData(['task', taskId], result.task)
        return
      }
      void query.refetch()
    },
  })
  const subject = state.status === 'authenticated' ? state.subject : undefined
  const canCancel = Boolean(
    query.data &&
    subject &&
    subject.role !== 'auditor' &&
    !terminal.has(query.data.status)
  )

  return (
    <section className={styles.page}>
      <PageHeader
        title="任务详情"
        description="仅展示任务类型、状态、时间和有界输入摘要，不读取原始结果。"
      >
        {canCancel ? <Button appearance="secondary" disabled={cancel.isPending} onClick={() => cancel.mutate()}>取消任务</Button> : null}
      </PageHeader>
      <Link className={styles.back} to="/tasks">返回任务台账</Link>
      {!taskId ? <StatePanel state="error" title="任务标识无效" /> : null}
      {query.isPending && taskId ? <StatePanel state="loading" title="正在加载任务详情" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? <StatePanel state="forbidden" title="无权查看该任务" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'not-found' ? <StatePanel state="empty" title="任务不存在" /> : null}
      {query.isError && !(query.error instanceof ApiError && ['forbidden', 'not-found'].includes(query.error.kind)) ? (
        <StatePanel state="error" title="暂时无法加载任务详情" actionLabel="重试" onAction={() => void query.refetch()} />
      ) : null}
      {cancel.isError ? <MessageBar intent="error"><MessageBarBody>取消状态尚未确认，请先刷新任务状态。</MessageBarBody></MessageBar> : null}
      {cancel.data?.status === 'uncertain' ? <MessageBar intent="warning"><MessageBarBody>网络确认中断，已重新读取任务状态，未自动重复取消。</MessageBarBody></MessageBar> : null}
      {query.data ? (
        <Card className={styles.panel} role="region" aria-label="任务安全摘要">
          <div className={styles.facts}>
            <div className={styles.fact}><Text className={styles.label}>任务类型</Text><Text>{taskTypeLabels[query.data.task_type]}</Text></div>
            <div className={styles.fact}><Text className={styles.label}>当前状态</Text><Text>{taskStatusLabels[query.data.status]}</Text></div>
            <div className={styles.fact}><Text className={styles.label}>负责人</Text><Text>{query.data.owner}</Text></div>
            <div className={styles.fact}><Text className={styles.label}>创建时间</Text><Text>{formatTaskTime(query.data.created_at)}</Text></div>
            <div className={styles.fact}><Text className={styles.label}>最后更新时间</Text><Text>{formatTaskTime(query.data.updated_at)}</Text></div>
            {query.data.input_summary.language ? <div className={styles.fact}><Text className={styles.label}>语言</Text><Text>{query.data.input_summary.language === 'zh' ? '中文' : '英文'}</Text></div> : null}
            {query.data.input_summary.thread ? <div className={styles.fact}><Text className={styles.label}>并发数</Text><Text>{query.data.input_summary.thread}</Text></div> : null}
            {query.data.input_summary.timeout ? <div className={styles.fact}><Text className={styles.label}>超时秒数</Text><Text>{query.data.input_summary.timeout}</Text></div> : null}
            {query.data.input_summary.port_scan_mode ? <div className={styles.fact}><Text className={styles.label}>端口扫描模式</Text><Text>{portScanModeLabels[query.data.input_summary.port_scan_mode]}</Text></div> : null}
            {query.data.input_summary.target_count ? <div className={styles.fact}><Text className={styles.label}>目标数量</Text><Text>{query.data.input_summary.target_count}</Text></div> : null}
            {query.data.input_summary.num_prompts ? <div className={styles.fact}><Text className={styles.label}>提示词数量</Text><Text>{query.data.input_summary.num_prompts}</Text></div> : null}
          </div>
        </Card>
      ) : null}
    </section>
  )
}
