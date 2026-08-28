/**
 * 功能：验证任务列表、创建和详情页面的角色、可访问性及请求取消边界。
 * 实现：在真实 Query/Session/Router 上下文中渲染页面并观察受控 fetch。
 * 输入：普通用户、审计员会话与任务安全 DTO。
 * 输出：原生表格、写按钮可见性和卸载取消的回归断言。
 * 依赖：Testing Library、TanStack Query、React Router 与 SessionProvider。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { StrictMode } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { SessionProvider } from '../auth/session'
import type { CurrentSubject, TaskSummary } from '../../shared/api/types'
import { TaskCreatePage } from './TaskCreatePage'
import { TaskDetailPage } from './TaskDetailPage'
import { TaskListPage } from './TaskListPage'

const taskSummary = {
  id: 'task-opaque-1',
  owner: 'alice',
  task_type: 'mcp_scan',
  status: 'running',
  created_at: '2026-08-18T01:00:00Z',
  updated_at: '2026-08-18T01:01:00Z',
} as const satisfies TaskSummary

const task = {
  ...taskSummary,
  input_summary: { language: 'zh', thread: 4 },
} as const

const operationalTasks = [
  { ...taskSummary, id: 'task-pending', status: 'pending' },
  { ...taskSummary, id: 'task-dispatching', status: 'dispatching' },
  { ...taskSummary, id: 'task-running', status: 'running' },
  { ...taskSummary, id: 'task-failed', status: 'failed' },
  { ...taskSummary, id: 'task-dispatch-failed', status: 'dispatch_failed' },
  { ...taskSummary, id: 'task-unknown', status: 'dispatch_unknown' },
  { ...taskSummary, id: 'task-cancelled', status: 'cancelled' },
  { ...taskSummary, id: 'task-finished', status: 'succeeded' },
] as const

const agentScanRunningTask = {
  ...taskSummary,
  id: 'task-agent-running',
  task_type: 'agent_scan',
  status: 'running',
} as const satisfies TaskSummary

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

function renderPage(page: React.ReactNode, subject: CurrentSubject, path = '/', routePath = '*') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <SessionProvider initialState={{ status: 'authenticated', subject }}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path={routePath} element={page} />
          </Routes>
        </MemoryRouter>
      </SessionProvider>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  )
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('任务页面', () => {
  it('列表使用原生表格并让审计员保持只读', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ items: [task], total: 1, page: 1, page_size: 20 })))
    renderPage(
      <TaskListPage />,
      { id: 'auditor-1', username: 'auditor', role: 'auditor', must_change_password: false },
      '/tasks',
    )

    const table = await screen.findByRole('table', { name: '扫描任务台账' })
    expect(table.tagName).toBe('TABLE')
    expect(screen.getByRole('columnheader', { name: '任务类型' }).tagName).toBe('TH')
    expect(screen.queryByRole('link', { name: '创建扫描任务' })).not.toBeInTheDocument()
  })

  it('普通用户在任务工作台查看当前页运行态势且保留原任务台账', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ items: operationalTasks, total: 47, page: 1, page_size: 20 })),
    )
    renderPage(
      <TaskListPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks',
    )

    const summary = await screen.findByRole('region', { name: '任务运行态势' })
    expect(summary).toHaveTextContent('当前查询')
    expect(summary).toHaveTextContent('全部状态')
    expect(summary).toHaveTextContent('全部类型')
    expect(summary).toHaveTextContent('匹配任务 47')
    expect(summary).toHaveTextContent('本页正在执行 2')
    expect(summary).toHaveTextContent('本页等待调度 1')
    expect(summary).toHaveTextContent('本页需关注 3')
    expect(summary).not.toHaveTextContent('本页正在执行 47')

    const filters = screen.getByRole('group', { name: '任务筛选' })
    expect(filters).toContainElement(screen.getByRole('combobox', { name: '任务状态' }))
    expect(filters).toContainElement(screen.getByRole('combobox', { name: '任务类型' }))
    expect(screen.getByRole('table', { name: '扫描任务台账' })).toBeInTheDocument()
    for (const { id } of operationalTasks) {
      expect(screen.getByRole('link', { name: `查看任务 ${id}` })).toBeInTheDocument()
    }
    expect(screen.queryByRole('button', { name: '清除筛选' })).not.toBeInTheDocument()
  })

  it('筛选后为空仍保留任务运行态势和可清除筛选入口', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 0, page: 1, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskListPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks?status=running&task_type=agent_scan',
    )

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    const summary = await screen.findByRole('region', { name: '任务运行态势' })
    expect(summary).toHaveTextContent('当前查询')
    expect(summary).toHaveTextContent('状态：执行中')
    expect(summary).toHaveTextContent('类型：Agent 扫描')
    expect(summary).toHaveTextContent('匹配任务 0')
    expect(screen.getByRole('button', { name: '清除筛选' })).toBeInTheDocument()
    expect(summary).not.toHaveTextContent('本页正在执行 0')
    expect(summary).not.toHaveTextContent('本页等待调度 0')
    expect(summary).not.toHaveTextContent('本页需关注 0')

    const filters = screen.getByRole('group', { name: '任务筛选' })
    expect(filters).toContainElement(screen.getByRole('combobox', { name: '任务状态' }))
    expect(filters).toContainElement(screen.getByRole('combobox', { name: '任务类型' }))
  })

  it('清除筛选后只以第一页无筛选请求任务列表', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ items: [agentScanRunningTask], total: 41, page: 3, page_size: 20 }))
      .mockResolvedValueOnce(jsonResponse({ items: operationalTasks, total: 47, page: 1, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskListPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks?page=3&status=running&task_type=agent_scan',
    )

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      'http://localhost:3000/api/v1/platform/tasks?page=3&page_size=20&status=running&task_type=agent_scan',
    )
    await screen.findByRole('table', { name: '扫描任务台账' })

    screen.getByRole('button', { name: '清除筛选' }).click()

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(fetchMock.mock.calls[1]?.[0]).toBe('http://localhost:3000/api/v1/platform/tasks?page=1&page_size=20')
  })

  it('列表加载失败时仍提供具名的任务筛选控件组', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 500 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskListPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks',
    )

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(await screen.findByText('暂时无法加载任务')).toBeInTheDocument()

    const filters = screen.getByRole('group', { name: '任务筛选' })
    expect(filters).toContainElement(screen.getByRole('combobox', { name: '任务状态' }))
    expect(filters).toContainElement(screen.getByRole('combobox', { name: '任务类型' }))
  })

  it('列表卸载会取消仍在等待的真实查询', async () => {
    let requestSignal: AbortSignal | undefined
    const fetchMock = vi.fn((_url: string, init?: RequestInit) => {
      requestSignal = init?.signal ?? undefined
      return new Promise<Response>(() => undefined)
    })
    vi.stubGlobal('fetch', fetchMock)
    const view = renderPage(
      <TaskListPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks',
    )
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    view.unmount()

    expect(requestSignal?.aborted).toBe(true)
  })

  it('列表从可分享URL恢复服务端分页和精确筛选', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 0, page: 3, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskListPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks?page=3&status=running&task_type=agent_scan',
    )

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      'http://localhost:3000/api/v1/platform/tasks?page=3&page_size=20&status=running&task_type=agent_scan',
    )
  })

  it('列表对恶意URL参数安全回到第一页且不发送未知筛选', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 0, page: 1, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskListPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks?page=-9&status=constructor&task_type=unknown',
    )

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/tasks?page=1&page_size=20')
  })

  it('列表拒绝超出服务端分页上限的页码', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 0, page: 1, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskListPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks?page=1001',
    )

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/tasks?page=1&page_size=20')
  })

  it('详情按角色与真实状态显示取消能力，审计员无写按钮', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(task)))
    renderPage(
      <TaskDetailPage />,
      { id: 'auditor-1', username: 'auditor', role: 'auditor', must_change_password: false },
      '/tasks/task-opaque-1',
      '/tasks/:taskId',
    )

    expect(await screen.findByRole('heading', { name: '任务详情' })).toBeInTheDocument()
    expect(await screen.findByText('执行中')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '取消任务' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /下载附件/ })).not.toBeInTheDocument()
  })

  it('普通用户可见的非终态详情不使用展示 owner 字段做前端授权', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ ...task, owner: '规范化展示名' })))
    renderPage(
      <TaskDetailPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks/task-opaque-1',
      '/tasks/:taskId',
    )

    expect(await screen.findByRole('button', { name: '取消任务' })).toBeInTheDocument()
  })

  it('取消写入不确定时复用确认详情且不触发第三次读取', async () => {
    const cancelled = { ...task, status: 'cancelled', updated_at: '2026-08-18T01:02:00Z' }
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(task))
      .mockResolvedValueOnce(jsonResponse({ status: 'unexpected-success-body' }))
      .mockResolvedValueOnce(jsonResponse(cancelled))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskDetailPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks/task-opaque-1',
      '/tasks/:taskId',
    )

    const button = await screen.findByRole('button', { name: '取消任务' })
    button.click()

    expect(await screen.findByText('网络确认中断，已重新读取任务状态，未自动重复取消。')).toBeInTheDocument()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3))
    await new Promise((resolve) => window.setTimeout(resolve, 0))
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(screen.getByText('已取消')).toBeInTheDocument()
  })

  it('详情后续请求持续失败时仍在固定次数内停止轮询', async () => {
    vi.useFakeTimers()
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(task))
      .mockResolvedValue(new Response(null, { status: 500 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskDetailPage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks/task-opaque-1',
      '/tasks/:taskId',
    )

    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    for (let index = 0; index < 12; index += 1) {
      await act(async () => { await vi.advanceTimersByTimeAsync(8_000) })
    }
    const boundedCalls = fetchMock.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(120_000) })

    expect(boundedCalls).toBe(8)
    expect(fetchMock).toHaveBeenCalledTimes(8)
  })

  it('普通用户可进入分步创建页且不出现密钥输入', () => {
    vi.stubGlobal('fetch', vi.fn())
    renderPage(
      <TaskCreatePage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks/new',
    )

    expect(screen.getByRole('heading', { name: '创建扫描任务' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: '第一步：任务类型' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: '第二步：参数' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: '第三步：附件' })).toBeInTheDocument()
    expect(screen.queryByLabelText(/密钥|Token|API Key/i)).not.toBeInTheDocument()
  })

  it.each([
    {
      taskType: 'model_redteam_report',
      fields: [
        ['评测模型 ID（逗号分隔）', 'model-1, model-2'],
        ['裁判模型 ID', 'eval-model-1'],
      ],
      params: {
        model_id: ['model-1', 'model-2'],
        eval_model_id: 'eval-model-1',
        dataset: { numPrompts: 100 },
      },
    },
    {
      taskType: 'agent_scan',
      fields: [
        ['Agent 配置 ID', 'agent-config-1'],
        ['裁判模型 ID', 'eval-model-1'],
      ],
      params: { agent_id: 'agent-config-1', eval_model_id: 'eval-model-1' },
    },
  ])('$taskType 创建只发送该类真实治理引用', async ({ taskType, fields, params }) => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(task))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskCreatePage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks/new',
    )
    fireEvent.change(screen.getByRole('combobox', { name: '扫描类型' }), { target: { value: taskType } })
    fireEvent.change(screen.getByRole('textbox', { name: '扫描目标或任务说明' }), { target: { value: '安全评测说明' } })
    for (const [label, value] of fields) {
      fireEvent.change(screen.getByRole('textbox', { name: label }), { target: { value } })
    }

    screen.getByRole('button', { name: '创建任务' }).click()

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    const body = JSON.parse(String((fetchMock.mock.calls[0]?.[1] as RequestInit).body))
    expect(body).toEqual(expect.objectContaining({ task_type: taskType, params }))
  })

  it('StrictMode 重新挂载后仍能呈现本地表单校验错误', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 500 })))
    renderPage(
      <StrictMode><TaskCreatePage /></StrictMode>,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks/new',
    )

    fireEvent.change(screen.getByRole('textbox', { name: '扫描目标或任务说明' }), {
      target: { value: 'https://example.test' },
    })
    screen.getByRole('button', { name: '创建任务' }).click()

    expect(await screen.findByText('任务创建未确认，显式重试将复用同一幂等键。')).toBeInTheDocument()
  })

  it('多文件上传部分成功时保留已取得的 opaque ID 供任务引用', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({
        id: 'attachment-first',
        filename: 'first.txt',
        size: 5,
        state: 'ready',
        created_at: '2026-08-18T01:00:00Z',
      }))
      .mockResolvedValueOnce(new Response(null, { status: 500 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskCreatePage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks/new',
    )
    fireEvent.change(document.querySelector('input[type="file"]') as HTMLInputElement, {
      target: { files: [new File(['first'], 'first.txt'), new File(['second'], 'second.txt')] },
    })

    screen.getByRole('button', { name: '上传附件' }).click()

    expect(await screen.findByText('附件上传失败，请核对后显式重试。')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button', { name: '下载附件 first.txt' })).toBeInTheDocument()
  })

  it('仍有待上传附件时禁止创建不含附件的任务', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskCreatePage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks/new',
    )
    fireEvent.change(screen.getByRole('textbox', { name: '扫描目标或任务说明' }), { target: { value: 'https://example.test' } })
    fireEvent.change(document.querySelector('input[type="file"]') as HTMLInputElement, {
      target: { files: [new File(['evidence'], 'evidence.txt')] },
    })

    expect(screen.getByRole('button', { name: '创建任务' })).toBeDisabled()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('已上传附件下载失败时显示固定错误而不产生未处理拒绝', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({
        id: 'attachment-first',
        filename: 'first.txt',
        size: 5,
        state: 'ready',
        created_at: '2026-08-18T01:00:00Z',
      }))
      .mockResolvedValueOnce(new Response(null, { status: 500 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(
      <TaskCreatePage />,
      { id: 'user-1', username: 'alice', role: 'user', must_change_password: false },
      '/tasks/new',
    )
    fireEvent.change(document.querySelector('input[type="file"]') as HTMLInputElement, {
      target: { files: [new File(['first'], 'first.txt')] },
    })
    screen.getByRole('button', { name: '上传附件' }).click()

    const download = await screen.findByRole('button', { name: '下载附件 first.txt' })
    download.click()

    expect(await screen.findByText('附件下载失败，请稍后重试。')).toBeInTheDocument()
  })
})
