/**
 * 功能：展示任务安全详情、受控短轮询、角色化取消和专属 AI 模型名称恢复。
 * 实现：查询和写请求随路由/卸载取消，刷新失败隐藏旧摘要；非终态有界退避，模型目录仅以安全白名单分页读取。
 * 输入：opaque 任务 ID、可选预期类型、当前 Subject 与安全任务/模型 DTO。
 * 输出：安全输入摘要、状态、更新时间、允许的取消按钮和模型安全标签。
 * 依赖：Fluent UI、React Query、React Router、Session、任务与模型目录 API。
 */
import { Button, Card, MessageBar, MessageBarBody, Text, makeStyles, tokens } from '@fluentui/react-components'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { useSession } from '../auth/session'
import { ApiError } from '../../shared/api/errors'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { fetchModelCatalog } from '../models/api'
import { cancelTaskGoverned, fetchTaskDetail, taskPollDelay } from './api'
import { MODEL_CATALOG_PAGE_SIZE, canonicalModels, fallbackModelLabel, hasRepeatedCatalogPage, modelOptionLabel, nextCatalogPage, selectableModels } from './governedModels'
import { formatTaskTime, taskStatusLabels, taskTypeLabels } from './TaskListPage'
import { taskWorkbench, type DedicatedTaskType } from './taskWorkbenches'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL, minWidth: 0 },
  panel: { padding: tokens.spacingVerticalL, boxShadow: 'none' },
  facts: { display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', gap: tokens.spacingHorizontalL, '@media (max-width: 768px)': { gridTemplateColumns: 'minmax(0, 1fr)' } },
  fact: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalXS, minWidth: 0, overflowWrap: 'anywhere' },
  label: { color: tokens.colorNeutralForeground2 },
  back: { color: tokens.colorBrandForegroundLink },
})

const terminal = new Set(['succeeded', 'failed', 'cancelled'])
const portScanModeLabels = {
  fixed_ai: '固定 AI 端口（11434、1337、7000–9000、18789）',
  full_tcp: '全量 TCP 1–65535',
} as const

function unavailableModelLabel(id: string): string {
  return `已选择的模型（ID: ${id}，目录暂不可用）`
}

function RestoredModelName({ modelID }: { modelID: string }) {
  const automaticPageRef = useRef<string | undefined>(undefined)
  const [retrying, setRetrying] = useState(false)
  const catalog = useInfiniteQuery({
    queryKey: ['task-detail-model-catalog', modelID],
    initialPageParam: 1,
    queryFn: async ({ pageParam, signal }) => {
      const page = await fetchModelCatalog({ page: pageParam, pageSize: MODEL_CATALOG_PAGE_SIZE }, signal)
      if (page.page !== pageParam || page.page_size !== MODEL_CATALOG_PAGE_SIZE) throw new ApiError('unexpected-response', 200)
      return page
    },
    getNextPageParam: (lastPage, allPages, lastPageParam, allPageParams) => {
      if (lastPage.page !== lastPageParam || hasRepeatedCatalogPage(lastPage.items, allPages.slice(0, -1).map((page) => page.items))) return undefined
      return nextCatalogPage(lastPage, allPageParams)
    },
    retry: false,
  })
  const catalogItems = catalog.data?.pages.flatMap((page) => page.items) ?? []
  const catalogRefreshInProgress = catalog.isRefetching && !catalog.isFetchingNextPage
  const catalogRefreshFailed = Boolean(catalog.data) && catalog.isRefetchError && !catalog.isFetchNextPageError
  const catalogTrusted = !catalogRefreshInProgress && !catalogRefreshFailed
  const trustedCatalogItems = catalogTrusted ? catalogItems : []
  const canonicalCatalogItems = canonicalModels(trustedCatalogItems)
  const selectedCanonicalModel = canonicalCatalogItems.find((model) => model.id === modelID)
  const selectedModel = selectableModels(trustedCatalogItems).find((model) => model.id === modelID)
  const lastPage = catalog.data?.pages.at(-1)
  const repeatedCatalogPage = catalogTrusted && lastPage !== undefined && hasRepeatedCatalogPage(
    lastPage.items,
    (catalog.data?.pages.slice(0, -1) ?? []).map((page) => page.items),
  )
  const catalogUnavailable = catalogRefreshFailed || catalog.isError || catalog.isFetchNextPageError || repeatedCatalogPage
  const catalogExhausted = catalogTrusted && Boolean(catalog.data) && !catalog.hasNextPage && !catalog.isFetching && !catalog.isFetchingNextPage
  const canAutomaticallyLoadNextPage = !selectedCanonicalModel && Boolean(catalog.data) && Boolean(catalog.hasNextPage) &&
    catalogTrusted && !catalogUnavailable && !catalog.isFetching && !catalog.isFetchingNextPage
  const automaticPageKey = catalog.data?.pageParams.join(',') ?? ''

  useEffect(() => {
    if (!canAutomaticallyLoadNextPage) {
      automaticPageRef.current = undefined
      return
    }
    if (automaticPageRef.current === automaticPageKey) return
    automaticPageRef.current = automaticPageKey
    void catalog.fetchNextPage({ cancelRefetch: false })
  }, [automaticPageKey, canAutomaticallyLoadNextPage, catalog.fetchNextPage])

  useEffect(() => {
    if (!catalog.isFetching) setRetrying(false)
  }, [catalog.isFetching])

  if (catalogUnavailable || retrying) {
    return (
      <>
        <Text>{unavailableModelLabel(modelID)}</Text>
        <Button
          appearance="transparent"
          disabled={catalog.isFetching || retrying}
          onClick={() => { setRetrying(true); void catalog.refetch() }}
        >
          {catalog.isFetching || retrying ? '正在重试恢复模型名称…' : '重试恢复模型名称'}
        </Button>
      </>
    )
  }
  if (catalogRefreshInProgress) return <Text role="status">正在恢复模型名称</Text>
  if (selectedModel) return <Text>{modelOptionLabel(selectedModel)}</Text>
  if (selectedCanonicalModel?.disabled || catalogExhausted) return <Text>{fallbackModelLabel(modelID)}</Text>
  return <Text role="status">正在恢复模型名称</Text>
}

export interface TaskDetailPageProps {
  expectedTaskType?: DedicatedTaskType
  returnTo?: string
}

interface TaskCancellation {
  taskId: string
  controller: AbortController
}

export function TaskDetailPage({ expectedTaskType, returnTo }: TaskDetailPageProps) {
  const styles = useStyles()
  const queryClient = useQueryClient()
  const workbench = taskWorkbench(expectedTaskType)
  const { taskId = '' } = useParams<{ taskId: string }>()
  const { state } = useSession()
  const activeCancellation = useRef<AbortController | null>(null)
  const query = useQuery({
    queryKey: ['task', taskId],
    queryFn: ({ signal }) => fetchTaskDetail(taskId, signal),
    enabled: taskId.length > 0,
    retry: false,
    refetchInterval: (current) => {
      if (expectedTaskType && current.state.data?.task_type !== undefined && current.state.data.task_type !== expectedTaskType) return false
      return taskPollDelay(current.state.data, current.state.dataUpdateCount + current.state.errorUpdateCount)
    },
  })
  const cancel = useMutation({
    mutationFn: (request: TaskCancellation) => cancelTaskGoverned(request.taskId, request.controller.signal),
    retry: false,
    onSuccess: (result, request) => {
      if (request.controller.signal.aborted || activeCancellation.current !== request.controller) return
      const queryKey = ['task', request.taskId]
      if (queryClient.getQueryState(queryKey)?.status !== 'success') return
      if (result.status === 'uncertain') {
        queryClient.setQueryData(queryKey, result.task)
        return
      }
      return queryClient.refetchQueries({ queryKey, exact: true, type: 'active' })
    },
    onSettled: (_result, _error, request) => {
      if (activeCancellation.current === request.controller) activeCancellation.current = null
    },
  })
  useEffect(() => {
    cancel.reset()
    return () => {
      activeCancellation.current?.abort()
      activeCancellation.current = null
    }
  }, [taskId, query.isError, cancel.reset])

  const requestCancellation = () => {
    if (activeCancellation.current) return
    const controller = new AbortController()
    activeCancellation.current = controller
    cancel.mutate({ taskId, controller })
  }
  const currentCancellation = cancel.variables?.taskId === taskId && !cancel.variables.controller.signal.aborted
  const subject = state.status === 'authenticated' ? state.subject : undefined
  const typeMismatch = query.isSuccess && expectedTaskType !== undefined && query.data.task_type !== expectedTaskType
  const canCancel = Boolean(
    query.isSuccess &&
    !typeMismatch &&
    subject &&
    subject.role !== 'auditor' &&
    !terminal.has(query.data.status)
  )

  return (
    <section className={styles.page}>
      <PageHeader
        title={workbench ? `${workbench.title}任务详情` : '任务详情'}
        description="查看扫描状态、配置摘要与报告。"
      >
        {canCancel ? <Button appearance="secondary" disabled={currentCancellation && cancel.isPending} onClick={requestCancellation}>取消任务</Button> : null}
      </PageHeader>
      <Link className={styles.back} to={returnTo ?? workbench?.path ?? '/tasks'}>返回任务台账</Link>
      {!taskId ? <StatePanel state="error" title="任务标识无效" /> : null}
      {query.isPending && taskId ? <StatePanel state="loading" title="正在加载任务详情" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? <StatePanel state="forbidden" title="无权查看该任务" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'not-found' ? <StatePanel state="empty" title="任务不存在" /> : null}
      {query.isError && !(query.error instanceof ApiError && ['forbidden', 'not-found'].includes(query.error.kind)) ? (
        <StatePanel state="error" title="暂时无法加载任务详情" actionLabel="重试" onAction={() => void query.refetch()} />
      ) : null}
      {typeMismatch ? <StatePanel state="error" title={`该任务不属于 ${workbench?.title ?? '当前扫描类型'}`} /> : null}
      {query.isSuccess && !typeMismatch && currentCancellation && cancel.isError ? <MessageBar intent="error"><MessageBarBody>取消状态尚未确认，请先刷新任务状态。</MessageBarBody></MessageBar> : null}
      {query.isSuccess && !typeMismatch && currentCancellation && cancel.data?.status === 'uncertain' ? <MessageBar intent="warning"><MessageBarBody>网络确认中断，已重新读取任务状态，未自动重复取消。</MessageBarBody></MessageBar> : null}
      {query.isSuccess && !typeMismatch ? (
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
            {expectedTaskType === 'ai_infra_scan' && query.data.task_type === 'ai_infra_scan' && query.data.input_summary.model_id ? <div className={styles.fact}><Text className={styles.label}>扫描模型</Text><RestoredModelName modelID={query.data.input_summary.model_id} /></div> : null}
            {query.data.task_type === 'agent_scan' && query.data.input_summary.agent_id ? <div className={styles.fact}><Text className={styles.label}>Agent 配置</Text><Text>{query.data.input_summary.agent_id}</Text></div> : null}
            {query.data.task_type === 'agent_scan' && query.data.input_summary.eval_model_id ? <div className={styles.fact}><Text className={styles.label}>扫描 / 裁判模型</Text><RestoredModelName key={query.data.input_summary.eval_model_id} modelID={query.data.input_summary.eval_model_id} /></div> : null}
            {query.data.task_type === 'agent_scan' && query.data.remark ? <div className={styles.fact}><Text className={styles.label}>任务备注</Text><Text>{query.data.remark}</Text></div> : null}
            {query.data.report_id ? <div className={styles.fact}><Text className={styles.label}>扫描报告</Text><Link to={`/reports/${encodeURIComponent(query.data.report_id)}`}>查看扫描报告</Link></div> : null}
            {expectedTaskType === 'ai_infra_scan' && query.data.task_type === 'ai_infra_scan' && query.data.remark ? (
              <div className={styles.fact}><Text className={styles.label}>任务说明</Text><Text>{query.data.remark}</Text></div>
            ) : null}
          </div>
        </Card>
      ) : null}
    </section>
  )
}
