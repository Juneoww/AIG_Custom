/**
 * 功能：验证 Agent 专属创建页的表单、幂等和卸载隔离。
 * 实现：用真实会话与 API 客户端配合受控响应；输入：用户操作；输出：请求和导航断言。
 * 依赖：Vitest、Testing Library、React Query、React Router。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { Link, MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { SessionProvider } from '../auth/session'
import type { SubjectRole } from '../../shared/api/types'
import { AgentWorkflowTaskCreatePage } from './AgentWorkflowTaskCreatePage'

const task = { id: 'agent-task-1', owner: 'alice', task_type: 'agent_scan', status: 'running', created_at: '2026-09-05T01:00:00Z', updated_at: '2026-09-05T01:00:00Z', input_summary: { agent_id: 'support', eval_model_id: 'model-1' } }
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const model = { id: 'model-1', owner_user_id: 'user-1', scope: 'private', name: '扫描模型', provider_model: 'test-model', base_url: 'https://secret.invalid', note: '', limit: 4, disabled: false, token: '********', source: 'platform', read_only: false }
type Call = [RequestInfo | URL, RequestInit?]

function Location() { return <output aria-label="当前地址">{useLocation().pathname}</output> }
function show(role: SubjectRole = 'user', page: ReactNode = <AgentWorkflowTaskCreatePage />) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><SessionProvider initialState={{ status: 'authenticated', subject: { id: 'user-1', username: 'alice', role, must_change_password: false } }}><MemoryRouter initialEntries={['/tasks/agent-workflow/new']}>{page}<Location /></MemoryRouter></SessionProvider></QueryClientProvider>)
}
function fixtures(onPost: (init?: RequestInit) => Promise<Response> = async () => response(task, 202)) {
  const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(url)).pathname
    if (path.endsWith('/identity/csrf')) return Promise.resolve(response({ csrf_token: 'test-csrf' }))
    if (path.endsWith('/agent/names')) return Promise.resolve(response({ status: 0, message: '', data: ['support'] }))
    if (path.endsWith('/platform/models')) return Promise.resolve(response({ items: [model], total: 1, page: 1, page_size: 100 }))
    if (path.endsWith('/tasks') && init?.method === 'POST') return onPost(init)
    throw new Error(`unexpected test path ${path}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}
async function fill() {
  await screen.findByRole('option', { name: 'support' })
  await screen.findByRole('option', { name: /扫描模型（test-model/ })
  fireEvent.change(screen.getByRole('combobox', { name: 'Agent 配置' }), { target: { value: 'support' } })
  fireEvent.change(screen.getByRole('combobox', { name: '扫描 / 裁判模型' }), { target: { value: 'model-1' } })
  fireEvent.change(screen.getByRole('textbox', { name: '执行说明' }), { target: { value: '测试客服 Agent 的提示注入边界' } })
  await waitFor(() => expect(screen.getByRole('button', { name: '创建扫描任务' })).toBeEnabled())
}

beforeEach(() => { vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('Agent 专属创建页', () => {
  it('分离执行说明与备注，只提交安全引用且无附件', async () => {
    const fetchMock = fixtures()
    show()
    expect(screen.getByRole('button', { name: '创建扫描任务' })).toBeDisabled()
    await fill()
    fireEvent.change(screen.getByRole('textbox', { name: '任务备注（可选）' }), { target: { value: '上线前验收' } })
    fireEvent.click(screen.getByRole('button', { name: '创建扫描任务' }))
    await waitFor(() => expect(screen.getByLabelText('当前地址')).toHaveTextContent('/tasks/agent-workflow/agent-task-1'))
    const posts = fetchMock.mock.calls.filter((call: Call) => call[1]?.method === 'POST')
    expect(posts).toHaveLength(1)
    expect(JSON.parse(String(posts[0][1]?.body))).toEqual({ task_type: 'agent_scan', content: '测试客服 Agent 的提示注入边界', remark: '上线前验收', country_iso_code: 'zh_CN', params: { agent_id: 'support', eval_model_id: 'model-1' }, attachment_ids: [] })
    expect(document.body).not.toHaveTextContent('https://secret.invalid')
    expect(document.querySelector('input[type=file]')).toBeNull()
  })
  it('不确定重试复用幂等键，编辑后建立新提交', async () => {
    const fetchMock = fixtures(async () => { throw new TypeError('network private') })
    show(); await fill()
    fireEvent.click(screen.getByRole('button', { name: '创建扫描任务' }))
    await screen.findByText('创建结果尚未确认。重试会复用本次提交，避免重复创建。')
    fireEvent.click(screen.getByRole('button', { name: '创建扫描任务' }))
    await waitFor(() => expect(fetchMock.mock.calls.filter((call: Call) => call[1]?.method === 'POST')).toHaveLength(2))
    await waitFor(() => expect(screen.getByRole('button', { name: '创建扫描任务' })).toBeEnabled())
    fireEvent.change(screen.getByRole('textbox', { name: '任务备注（可选）' }), { target: { value: '第二次验收' } })
    fireEvent.click(screen.getByRole('button', { name: '创建扫描任务' }))
    await waitFor(() => expect(fetchMock.mock.calls.filter((call: Call) => call[1]?.method === 'POST')).toHaveLength(3))
    const keys = fetchMock.mock.calls.filter((call: Call) => call[1]?.method === 'POST').map((call: Call) => new Headers(call[1]?.headers).get('Idempotency-Key'))
    expect(keys[0]).toBe(keys[1]); expect(keys[2]).not.toBe(keys[1])
  })
  it('取消后迟到响应不会覆盖用户导航', async () => {
    let finish: (value: Response) => void = () => undefined
    let signal: AbortSignal | null | undefined
    fixtures((init) => { signal = init?.signal; return new Promise((resolve) => { finish = resolve }) })
    show(); await fill()
    fireEvent.click(screen.getByRole('button', { name: '创建扫描任务' }))
    await waitFor(() => expect(signal).toBeDefined())
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(signal?.aborted).toBe(true)
    await act(async () => { finish(response(task, 202)) })
    expect(screen.getByLabelText('当前地址').textContent).toBe('/tasks/agent-workflow')
  })
  it('审计员没有提交表单', () => {
    const fetchMock = fixtures()
    show('auditor')
    expect(screen.queryByRole('button', { name: '创建扫描任务' })).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it.each([
    ['ASCII 32 KiB', 'a'.repeat(32_768), ''],
    ['中文与 ASCII 合计 32 KiB', `${'中'.repeat(10_922)}ab`, ''],
    ['emoji 32 KiB 与 2,000 码点备注', '😀'.repeat(8_192), '😀'.repeat(2_000)],
  ])('接受边界内的%s，并保留实际文本', async (_label, content, remark) => {
    const fetchMock = fixtures()
    show(); await fill()
    fireEvent.change(screen.getByRole('textbox', { name: '执行说明' }), { target: { value: content } })
    fireEvent.change(screen.getByRole('textbox', { name: '任务备注（可选）' }), { target: { value: remark } })
    fireEvent.click(screen.getByRole('button', { name: '创建扫描任务' }))
    await waitFor(() => expect(screen.getByLabelText('当前地址')).toHaveTextContent('/tasks/agent-workflow/agent-task-1'))
    const post = fetchMock.mock.calls.find((call: Call) => call[1]?.method === 'POST')
    const body = JSON.parse(String(post?.[1]?.body))
    expect(body.content).toBe(content)
    expect(body.remark).toBe(remark || undefined)
  })

  it.each([
    ['ASCII 超出 32 KiB', 'a'.repeat(32_769)],
    ['中文超出 32 KiB', '中'.repeat(10_923)],
    ['emoji 超出 32 KiB', '😀'.repeat(8_193)],
    ['孤立高代理项', '说明\uD800'],
    ['孤立低代理项', '说明\uDC00'],
  ])('拒绝执行说明%s，不发创建请求', async (_label, content) => {
    const fetchMock = fixtures()
    show(); await fill()
    fireEvent.change(screen.getByRole('textbox', { name: '执行说明' }), { target: { value: content } })
    fireEvent.click(screen.getByRole('button', { name: '创建扫描任务' }))
    expect(await screen.findByText('执行说明必须为有效文本，且不能超过 32 KiB。')).toBeInTheDocument()
    expect(fetchMock.mock.calls.filter((call: Call) => call[1]?.method === 'POST')).toHaveLength(0)
  })

  it.each([
    ['2,001 个码点', '😀'.repeat(2_001)],
    ['孤立高代理项', '备注\uD800'],
    ['孤立低代理项', '备注\uDC00'],
  ])('拒绝任务备注%s，不发创建请求', async (_label, remark) => {
    const fetchMock = fixtures()
    show(); await fill()
    fireEvent.change(screen.getByRole('textbox', { name: '任务备注（可选）' }), { target: { value: remark } })
    fireEvent.click(screen.getByRole('button', { name: '创建扫描任务' }))
    expect(await screen.findByText('任务备注必须为有效文本，且不能超过 2,000 个字符。')).toBeInTheDocument()
    expect(fetchMock.mock.calls.filter((call: Call) => call[1]?.method === 'POST')).toHaveLength(0)
  })

  it.each(['success', 'failure'])('卸载后中止请求并忽略迟到的 %s 提交结果', async (outcome) => {
    let finish!: (value: Response) => void
    let fail!: (reason: Error) => void
    let signal: AbortSignal | null | undefined
    fixtures((init) => { signal = init?.signal; return new Promise((resolve, reject) => { finish = resolve; fail = reject }) })
    show('user', <Routes>
      <Route path="/tasks/agent-workflow/new" element={<><Link to="/outside">离开创建页</Link><AgentWorkflowTaskCreatePage /></>} />
      <Route path="/outside" element={<p>其他页面</p>} />
    </Routes>)
    await fill()
    fireEvent.click(screen.getByRole('button', { name: '创建扫描任务' }))
    await waitFor(() => expect(signal).toBeDefined())
    fireEvent.click(screen.getByRole('link', { name: '离开创建页' }))
    expect(signal?.aborted).toBe(true)
    await act(async () => { if (outcome === 'success') finish(response(task, 202)); else fail(new Error('Controlled late submission failure')) })
    expect(screen.getByLabelText('当前地址')).toHaveTextContent('/outside')
    expect(screen.getByText('其他页面')).toBeInTheDocument()
    expect(screen.queryByText('创建结果尚未确认。重试会复用本次提交，避免重复创建。')).not.toBeInTheDocument()
  })
})
