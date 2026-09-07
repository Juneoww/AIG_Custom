/**
 * 功能：验证 Agent 目录选择与任务详情的安全引用合同。
 * 实现：用受控 fetch 和查询缓存覆盖加载、刷新、失效与敏感字段过滤。
 * 输入：测试目录、详情响应；输出：Vitest 断言；依赖：Testing Library、React Query。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { parseTaskDetail } from './api'
import { GovernedAgentSelector } from './components/GovernedAgentSelector'
import { GovernedModelSelector } from './components/GovernedModelSelector'

const task = { id: 'task-1', owner: 'alice', task_type: 'agent_scan', status: 'succeeded', created_at: '2026-09-05T01:00:00Z', updated_at: '2026-09-05T01:00:00Z' }
const response = (body: unknown) => new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } })
const agentNames = (names: string[]) => response({ status: 0, message: '', data: names })

beforeEach(() => { vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('Agent 任务安全详情', () => {
  it('只投影引用与已就绪报告，不投影目标配置', () => {
    expect(parseTaskDetail({ ...task, report_id: 'report-1', input_summary: { agent_id: 'customer agent', eval_model_id: 'model-1', agent_data: 'private' } })).toEqual({ ...task, report_id: 'report-1', input_summary: { agent_id: 'customer agent', eval_model_id: 'model-1' } })
    expect(parseTaskDetail({ ...task, task_type: 'mcp_scan', input_summary: { agent_id: 'customer agent', eval_model_id: 'model-1' } }).input_summary).toEqual({})
  })
  it.each(['sk-secret', '../target', 'a'.repeat(129), 'Bearer secret'])('拒绝不安全 Agent 引用 %s', (id) => {
    expect(() => parseTaskDetail({ ...task, input_summary: { agent_id: id } })).toThrow()
  })
  it.each(['https://private.invalid', '../report', 'sk-secret'])('拒绝不安全报告引用 %s', (reportID) => {
    expect(() => parseTaskDetail({ ...task, report_id: reportID, input_summary: {} })).toThrow()
  })
  it('拒绝未完成任务携带报告引用', () => {
    expect(() => parseTaskDetail({ ...task, status: 'running', report_id: 'report-1', input_summary: {} })).toThrow()
  })
})

describe('Agent 治理目录', () => {
  it('仅请求名称目录，去重过滤并在刷新期间暂停确认', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(agentNames(['customer agent', 'customer agent', 'sk-private']))
    vi.stubGlobal('fetch', fetchMock)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const availability = vi.fn()
    const change = vi.fn()
    render(<QueryClientProvider client={client}><MemoryRouter><GovernedAgentSelector value="customer agent" onChange={change} onAvailabilityChange={availability} /></MemoryRouter></QueryClientProvider>)
    expect(await screen.findByRole('option', { name: 'customer agent' })).toBeInTheDocument()
    expect(screen.getAllByRole('option', { name: 'customer agent' })).toHaveLength(1)
    expect(document.body).not.toHaveTextContent('sk-private')
    expect(new URL(fetchMock.mock.calls[0][0]).pathname).toBe('/api/v1/knowledge/agent/names')
    await waitFor(() => expect(availability).toHaveBeenLastCalledWith('available'))
    let resolve: (value: Response) => void = () => undefined
    fetchMock.mockImplementationOnce(() => new Promise<Response>((done) => { resolve = done }))
    fireEvent.click(screen.getByRole('button', { name: '刷新 Agent 目录' }))
    await waitFor(() => expect(availability).toHaveBeenLastCalledWith('pending'))
    await act(async () => { resolve(agentNames([])) })
    await waitFor(() => expect(change).toHaveBeenCalledWith(undefined))
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('失败时保留未确认选择，显式重试后恢复', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValueOnce(new TypeError('private network error')).mockResolvedValueOnce(agentNames(['target'])))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const availability = vi.fn()
    render(<QueryClientProvider client={client}><MemoryRouter><GovernedAgentSelector value="target" onChange={vi.fn()} onAvailabilityChange={availability} /></MemoryRouter></QueryClientProvider>)
    expect(await screen.findByText('Agent 目录加载失败，请重试。')).toBeInTheDocument()
    expect(availability).not.toHaveBeenCalledWith('available')
    expect(document.body).not.toHaveTextContent('private network error')
    fireEvent.click(screen.getByRole('button', { name: '重试 Agent 目录' }))
    await waitFor(() => expect(availability).toHaveBeenLastCalledWith('available'))
  })

  it('必选模型使用独立标签，空选择不可提交', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ items: [], total: 0, page: 1, page_size: 100 })))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const availability = vi.fn()
    render(<QueryClientProvider client={client}><MemoryRouter><GovernedModelSelector required label="扫描 / 裁判模型" onChange={vi.fn()} onAvailabilityChange={availability} /></MemoryRouter></QueryClientProvider>)
    expect(screen.getByRole('combobox', { name: '扫描 / 裁判模型' })).toBeRequired()
    expect(screen.queryByRole('option', { name: '不使用模型' })).not.toBeInTheDocument()
    await waitFor(() => expect(availability).toHaveBeenLastCalledWith('unavailable'))
  })
})
