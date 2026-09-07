/**
 * 功能：验证共享任务页在路由切换、卸载和刷新失败后的状态隔离。
 * 实现：使用真实 Query/Router 生命周期，控制取消结果及读取响应的到达顺序。
 * 输入：缓存任务、权限错误和迟到取消响应；输出：缓存、信号及界面断言。
 * 依赖：Vitest、Testing Library、React Query、React Router。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { Link, MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { SessionProvider } from '../auth/session'
import type { TaskDetail, TaskType } from '../../shared/api/types'
import type { GovernedCancelResult } from './api'
import { TaskDetailPage } from './TaskDetailPage'
import { TaskListPage } from './TaskListPage'
import type { DedicatedTaskType } from './taskWorkbenches'

const cancelRequest = vi.hoisted(() => vi.fn())
vi.mock('./api', async (load) => ({ ...await load<typeof import('./api')>(), cancelTaskGoverned: cancelRequest }))

const contexts = [
  { label: '通用', type: 'mcp_scan', dedicated: undefined, title: undefined },
  { label: 'AI', type: 'ai_infra_scan', dedicated: 'ai_infra_scan', title: 'AI 基础设施扫描' },
  { label: 'Agent', type: 'agent_scan', dedicated: 'agent_scan', title: 'Agent 工作流扫描' },
] as const

function task(id: string, type: TaskType = 'agent_scan'): TaskDetail {
  return { id, owner: `owner-${id}`, task_type: type, status: 'running',
    created_at: '2026-09-05T01:00:00Z', updated_at: '2026-09-05T01:00:00Z',
    input_summary: {} }
}

function response(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } })
}

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } })
}

function show(queryClient: QueryClient, page: ReactNode, detail = false) {
  return render(<QueryClientProvider client={queryClient}>
    <SessionProvider initialState={{ status: 'authenticated', subject: { id: 'user-1', username: 'alice', role: 'user', must_change_password: false } }}>
      <MemoryRouter initialEntries={[detail ? '/tasks/task-a' : '/tasks']}>
        <Link to="/tasks/task-b">切换到任务 B</Link><Link to="/outside">离开任务页</Link>
        <Routes><Route path={detail ? '/tasks/:taskId' : '/tasks'} element={page} /><Route path="/outside" element={<p>其他页面</p>} /></Routes>
      </MemoryRouter>
    </SessionProvider>
  </QueryClientProvider>)
}

function pendingCancellation() {
  let resolve!: (value: GovernedCancelResult) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<GovernedCancelResult>((done, fail) => { resolve = done; reject = fail })
  cancelRequest.mockReturnValueOnce(promise)
  return { resolve, reject }
}

async function finishCancellation(pending: ReturnType<typeof pendingCancellation>, outcome: string, source: TaskDetail) {
  await act(async () => {
    if (outcome === 'error') pending.reject(new Error('Controlled cancellation error'))
    else if (outcome === 'aborted') pending.reject(new DOMException('Controlled cancellation abort', 'AbortError'))
    else pending.resolve(outcome === 'confirmed' ? { status: 'confirmed' } : { status: 'uncertain', task: { ...source, status: 'cancelled' } })
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

beforeEach(() => {
  cancelRequest.mockReset()
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe.each(contexts)('$label 工作台刷新边界', ({ type, dedicated, title }) => {
  it.each([403, 404, 500])('详情刷新 %i 后隐藏缓存摘要和取消操作', async (status) => {
    const queryClient = client()
    queryClient.setQueryData(['task', 'task-a'], task('task-a', type))
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status })))
    show(queryClient, <TaskDetailPage expectedTaskType={dedicated} />, true)
    expect(screen.getByRole('region', { name: '任务安全摘要' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '取消任务' })).toBeEnabled()

    await act(async () => { await queryClient.refetchQueries({ queryKey: ['task', 'task-a'], exact: true }) })
    await screen.findByText(status === 403 ? '无权查看该任务' : status === 404 ? '任务不存在' : '暂时无法加载任务详情')
    expect(queryClient.getQueryData(['task', 'task-a'])).toBeDefined()
    expect(screen.queryByRole('region', { name: '任务安全摘要' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '取消任务' })).not.toBeInTheDocument()
    expect(screen.queryByText('owner-task-a')).not.toBeInTheDocument()
  })

  it('已完成任务刷新 403 后隐藏缓存报告链接', async () => {
    const queryClient = client()
    queryClient.setQueryData(['task', 'task-a'], { ...task('task-a', type), status: 'succeeded', report_id: 'report-a' })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 403 })))
    show(queryClient, <TaskDetailPage expectedTaskType={dedicated} />, true)
    expect(screen.getByRole('link', { name: '查看扫描报告' })).toBeInTheDocument()

    await act(async () => { await queryClient.refetchQueries({ queryKey: ['task', 'task-a'], exact: true }) })
    await screen.findByText('无权查看该任务')
    expect(screen.queryByRole('link', { name: '查看扫描报告' })).not.toBeInTheDocument()
  })

  it.each([403, 404, 500])('列表刷新 %i 后隐藏缓存行和分页', async (status) => {
    const queryClient = client()
    queryClient.setQueryData(['tasks', { page: 1, pageSize: 20, status: undefined, taskType: dedicated }],
      { items: [task('task-a', type)], total: 41, page: 1, page_size: 20 })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status })))
    show(queryClient, <TaskListPage fixedTaskType={dedicated} />)
    expect(screen.getByRole('table')).toBeInTheDocument()

    await act(async () => { await queryClient.refetchQueries({ queryKey: ['tasks'] }) })
    await screen.findByText(status === 403
      ? title ? `无权查看 ${title}任务台账` : '无权查看任务台账'
      : title ? `暂时无法加载 ${title}任务` : '暂时无法加载任务')
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '查看任务 task-a' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '下一页' })).not.toBeInTheDocument()
    expect(screen.queryByText('owner-task-a')).not.toBeInTheDocument()
  })
})

describe.each(['ai_infra_scan', 'agent_scan'] satisfies DedicatedTaskType[])('%s 取消请求路由隔离', (type) => {
  it.each(['uncertain', 'confirmed', 'error', 'aborted'])('A 的 %s 迟到结果不能更新 B 缓存或提示', async (outcome) => {
    const queryClient = client()
    const source = task('task-a', type)
    const destination = task('task-b', type)
    const reads: string[] = []
    vi.stubGlobal('fetch', vi.fn((url: RequestInfo | URL) => {
      const id = new URL(String(url)).pathname.split('/').at(-1)!
      reads.push(id)
      return Promise.resolve(response(id === 'task-a' ? source : destination))
    }))
    const pending = pendingCancellation()
    show(queryClient, <TaskDetailPage expectedTaskType={type} />, true)
    fireEvent.click(await screen.findByRole('button', { name: '取消任务' }))
    await waitFor(() => expect(cancelRequest).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole('link', { name: '切换到任务 B' }))
    await screen.findByText('owner-task-b')
    await finishCancellation(pending, outcome, source)

    expect(queryClient.getQueryData(['task', 'task-b'])).toEqual(destination)
    expect(reads.filter((id) => id === 'task-b')).toHaveLength(1)
    expect(screen.getByText('owner-task-b')).toBeInTheDocument()
    expect(screen.queryByText('网络确认中断，已重新读取任务状态，未自动重复取消。')).not.toBeInTheDocument()
    expect(screen.queryByText('取消状态尚未确认，请先刷新任务状态。')).not.toBeInTheDocument()
    expect(cancelRequest.mock.calls[0][0]).toBe('task-a')
    expect(cancelRequest.mock.calls[0][1]?.aborted).toBe(true)
    expect(screen.getByRole('button', { name: '取消任务' })).toBeEnabled()
  })
})

it.each(['uncertain', 'confirmed', 'error', 'aborted'])('详情卸载后忽略 %s 取消结果并中止请求', async (outcome) => {
  const queryClient = client()
  const source = task('task-a')
  queryClient.setQueryData(['task', 'task-a'], source)
  const fetchMock = vi.fn().mockResolvedValue(response(source))
  vi.stubGlobal('fetch', fetchMock)
  const pending = pendingCancellation()
  const view = show(queryClient, <TaskDetailPage expectedTaskType="agent_scan" />, true)
  fireEvent.click(screen.getByRole('button', { name: '取消任务' }))
  await waitFor(() => expect(cancelRequest).toHaveBeenCalledTimes(1))
  view.unmount()
  await finishCancellation(pending, outcome, source)

  expect(queryClient.getQueryData(['task', 'task-a'])).toEqual(source)
  expect(fetchMock).not.toHaveBeenCalled()
  expect(cancelRequest.mock.calls[0][1]?.aborted).toBe(true)
})

it('详情刷新 403 后迟到取消结果不能恢复已失效的缓存摘要', async () => {
  const queryClient = client()
  const source = task('task-a')
  queryClient.setQueryData(['task', 'task-a'], source)
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 403 })))
  const pending = pendingCancellation()
  show(queryClient, <TaskDetailPage expectedTaskType="agent_scan" />, true)
  fireEvent.click(screen.getByRole('button', { name: '取消任务' }))
  await waitFor(() => expect(cancelRequest).toHaveBeenCalledTimes(1))
  await act(async () => { await queryClient.refetchQueries({ queryKey: ['task', 'task-a'], exact: true }) })
  await screen.findByText('无权查看该任务')

  await finishCancellation(pending, 'uncertain', source)
  expect(queryClient.getQueryState(['task', 'task-a'])?.status).toBe('error')
  expect(screen.queryByRole('region', { name: '任务安全摘要' })).not.toBeInTheDocument()
  expect(cancelRequest.mock.calls[0][1]?.aborted).toBe(true)
})
