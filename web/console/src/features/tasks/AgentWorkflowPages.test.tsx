/**
 * 功能：锁定 Agent 工作台固定类型、统计范围、权限与报告跳转。
 * 实现：以真实任务 API 解码和路由驱动页面；输入：受控响应；输出：可访问界面断言。
 * 依赖：React Query、Testing Library、Vitest。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'

import { SessionProvider } from '../auth/session'
import { TaskListPage } from './TaskListPage'
import { TaskDetailPage } from './TaskDetailPage'

const task = { id: 'agent-task-1', owner: 'alice', task_type: 'agent_scan', status: 'succeeded', created_at: '2026-09-05T01:00:00Z', updated_at: '2026-09-05T01:00:00Z' }
const response = (body: unknown) => new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } })
function show(page: ReactNode, detail = false) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><SessionProvider initialState={{ status: 'authenticated', subject: { id: 'user-1', username: 'alice', role: 'auditor', must_change_password: false } }}><MemoryRouter initialEntries={[detail ? '/tasks/agent-workflow/agent-task-1' : '/tasks/agent-workflow?task_type=mcp_scan']}><Routes><Route path={detail ? '/tasks/agent-workflow/:taskId' : '/tasks/agent-workflow'} element={page} /></Routes></MemoryRouter></SessionProvider></QueryClientProvider>)
}
beforeEach(() => { vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('Agent 扫描工作台', () => {
  it('固定服务端类型，六列表格与统计范围正确，审计员可查看', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response({ items: [task], total: 41, page: 1, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)
    show(<TaskListPage fixedTaskType="agent_scan" />)
    expect(await screen.findByRole('heading', { name: 'Agent 工作流扫描' })).toBeInTheDocument()
    expect(await screen.findByRole('table', { name: 'Agent 工作流扫描任务台账' })).toBeInTheDocument()
    expect(screen.getAllByRole('columnheader')).toHaveLength(6)
    expect(screen.getByRole('group', { name: '当前查询匹配任务 41' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: '当前页正在执行 0' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '新建 Agent 工作流扫描任务' })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: '查看任务 agent-task-1' })).toHaveAttribute('href', '/tasks/agent-workflow/agent-task-1')
    expect(new URL(fetchMock.mock.calls[0][0]).searchParams.get('task_type')).toBe('agent_scan')
  })
  it('使用服务端报告关联，展示 Agent、备注和安全模型回退', async () => {
    const fetchMock = vi.fn((url: RequestInfo | URL) => Promise.resolve(String(url).includes('/platform/models')
      ? response({ items: [], total: 0, page: 1, page_size: 100 })
      : response({ ...task, remark: '验收记录', report_id: 'snapshot-1', input_summary: { agent_id: 'support', eval_model_id: 'model-1' } })))
    vi.stubGlobal('fetch', fetchMock)
    show(<TaskDetailPage expectedTaskType="agent_scan" returnTo="/tasks/agent-workflow" />, true)
    expect(await screen.findByText('support')).toBeInTheDocument()
    expect(await screen.findByText('已选择的模型（ID: model-1）')).toBeInTheDocument()
    expect(screen.getByText('验收记录')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '查看扫描报告' })).toHaveAttribute('href', '/reports/snapshot-1')
    expect(fetchMock.mock.calls.some(([url]) => /\/result|\/agent\//.test(String(url)))).toBe(false)
  })
  it('错误类型详情停止轮询，隐藏摘要与操作', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response({ ...task, task_type: 'mcp_scan', status: 'running', input_summary: {} }))
    vi.stubGlobal('fetch', fetchMock)
    show(<TaskDetailPage expectedTaskType="agent_scan" />, true)
    expect(await screen.findByText('该任务不属于 Agent 工作流扫描')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: '任务安全摘要' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '取消任务' })).not.toBeInTheDocument()
    vi.useFakeTimers()
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000) })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})
