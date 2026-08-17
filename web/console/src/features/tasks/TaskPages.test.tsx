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
import type { CurrentSubject } from '../../shared/api/types'
import { TaskCreatePage } from './TaskCreatePage'
import { TaskDetailPage } from './TaskDetailPage'
import { TaskListPage } from './TaskListPage'

const task = {
  id: 'task-opaque-1',
  owner: 'alice',
  task_type: 'mcp_scan',
  status: 'running',
  created_at: '2026-08-18T01:00:00Z',
  updated_at: '2026-08-18T01:01:00Z',
  input_summary: { language: 'zh', thread: 4 },
} as const

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
