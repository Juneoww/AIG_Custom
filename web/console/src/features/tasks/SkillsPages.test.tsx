/** 功能：通过真实路由和 API 锁定 Skills 工作台、必选模型、ZIP 上传及任务隔离。 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { Link, MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AppContent } from '../../app/App'
import { AppProviders } from '../../app/providers/AppProviders'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { SessionProvider } from '../auth/session'
import type { SubjectRole } from '../../shared/api/types'
import { TaskDetailPage } from './TaskDetailPage'
import { TaskListPage } from './TaskListPage'

const task = {
  id: 'skills/opaque-1', owner: 'alice', task_type: 'skills_scan', status: 'running',
  created_at: '2026-09-05T01:00:00Z', updated_at: '2026-09-05T01:01:00Z',
  input_summary: { language: 'zh', model_id: 'model-1', scan_mode: 'static' },
} as const
const model = {
  id: 'model-1', owner_user_id: 'user-1', scope: 'private', name: 'Skills 审核模型', provider_model: 'review-model',
  base_url: 'https://models.example.test/v1', note: 'secret-note', limit: 4, disabled: false, token: '********', source: 'platform', read_only: false,
}
const attachment = { id: 'attachment-1', filename: 'skills.zip', size: 4, state: 'ready', created_at: task.created_at }
const user = { id: 'user-1', username: 'alice', role: 'user', must_change_password: false } as const

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function LocationProbe() {
  const { pathname, search } = useLocation()
  return <output aria-label="当前位置">{pathname}{search}</output>
}

function renderRoute(path: string, role: SubjectRole = 'user', queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  return render(
    <ThemeProvider initialMode="light">
      <AppProviders queryClient={queryClient} sessionInitialState={{ status: 'authenticated', subject: { ...user, role } }}>
        <MemoryRouter initialEntries={[path]}><AppContent /><LocationProbe /></MemoryRouter>
      </AppProviders>
    </ThemeProvider>,
  )
}

function renderPage(page: React.ReactNode, path: string, routePath = '*') {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <SessionProvider initialState={{ status: 'authenticated', subject: user }}>
        <MemoryRouter initialEntries={[path]}><Routes><Route path={routePath} element={page} /></Routes><LocationProbe /></MemoryRouter>
      </SessionProvider>
    </QueryClientProvider>,
  )
}

function mockCreationFetch() {
  const mock = vi.fn((request: RequestInfo | URL, init?: RequestInit) => {
    const url = String(request)
    if (url.includes('/models?')) return Promise.resolve(json({ items: [model], total: 1, page: 1, page_size: 100 }))
    if (url.endsWith('/attachments')) return Promise.resolve(json(attachment, 201))
    if (url.endsWith('/tasks') && init?.method === 'POST') return Promise.resolve(json(task, 202))
    return Promise.resolve(json(task))
  })
  vi.stubGlobal('fetch', mock)
  return mock
}

async function uploadZIP() {
  fireEvent.change(screen.getByLabelText('Skills ZIP 包'), { target: { files: [new File(['zip!'], 'skills.zip', { type: 'application/zip' })] } })
  fireEvent.click(screen.getByRole('button', { name: '上传 Skills 包' }))
  await screen.findByText('skills.zip（4 字节）')
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('Skills 工作台', () => {
  it('固定类型不受 URL 覆盖，并使用安全六列与当前页运行指标', async () => {
    const fetchMock = vi.fn().mockResolvedValue(json({ items: [task], total: 31, page: 2, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(<TaskListPage fixedTaskType="skills_scan" />, '/tasks/skills?page=2&task_type=agent_scan&scan=mcp')
    const table = await screen.findByRole('table', { name: 'Skills 扫描任务台账' })
    expect(within(table).getAllByRole('columnheader').map((cell) => cell.textContent)).toEqual(['任务 ID', '负责人', '状态', '创建时间', '更新时间', '操作'])
    expect(within(table).getByRole('link', { name: '查看任务 skills/opaque-1' })).toHaveAttribute('href', '/tasks/skills/skills%2Fopaque-1')
    expect(screen.getByRole('group', { name: '当前查询匹配任务 31' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: '当前页正在执行 1' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: '当前页等待调度 0' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: '当前页需关注 0' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: '任务类型' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('/tasks/skills?page=2')
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/tasks?page=2&page_size=20&task_type=skills_scan')
  })

  it.each(['user', 'auditor', 'admin'] as const)('%s 可以读取 Skills 列表，审计员没有创建入口', async (role) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({ items: [], total: 0, page: 1, page_size: 20 })))
    renderRoute('/tasks/skills', role)
    expect(await screen.findByRole('heading', { name: 'Skills 扫描' })).toBeInTheDocument()
    const create = screen.queryByRole('link', { name: '新建 Skills 扫描任务' })
    if (role === 'auditor') expect(create).not.toBeInTheDocument()
    else expect(create).toHaveAttribute('href', '/tasks/skills/new')
  })

  it.each(['user', 'admin'] as const)('%s 可以访问专属创建页且 URL 不改变类型', async (role) => {
    mockCreationFetch()
    renderRoute('/tasks/skills/new?task_type=ai_infra_scan&scan=mcp', role)
    expect(screen.getByRole('heading', { name: '新建 Skills 扫描任务' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: '扫描类型' })).not.toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: '扫描目标或任务说明' })).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '不使用模型' })).not.toBeInTheDocument()
    expect(screen.getByText(/仅含 SKILL.md 的技能包也可扫描/)).toBeInTheDocument()
    await screen.findByRole('option', { name: /Skills 审核模型/ })
    expect(screen.getByRole('button', { name: '创建 Skills 扫描任务' })).toBeDisabled()
  })

  it('审计员不能进入 Skills 创建页', () => {
    vi.stubGlobal('fetch', vi.fn())
    renderRoute('/tasks/skills/new', 'auditor')
    expect(screen.getByRole('heading', { name: '无权访问' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Skills ZIP 包')).not.toBeInTheDocument()
  })

  it('上传一个 ZIP 并选择可用模型后仅发送固定合同字段', async () => {
    const fetchMock = mockCreationFetch()
    renderRoute('/tasks/skills/new')
    await screen.findByRole('option', { name: /Skills 审核模型/ })
    await uploadZIP()
    expect(screen.getByRole('button', { name: '创建 Skills 扫描任务' })).toBeDisabled()
    fireEvent.change(screen.getByRole('combobox', { name: '扫描模型' }), { target: { value: 'model-1' } })
    fireEvent.change(screen.getByRole('textbox', { name: '任务说明 / 备注（可选）' }), { target: { value: '  只扫描所上传技能  ' } })
    fireEvent.click(screen.getByRole('button', { name: '创建 Skills 扫描任务' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(([url, init]) => String(url).endsWith('/tasks') && init?.method === 'POST')).toBe(true))
    const request = fetchMock.mock.calls.find(([url, init]) => String(url).endsWith('/tasks') && init?.method === 'POST')?.[1]
    expect(JSON.parse(String(request?.body))).toEqual({ task_type: 'skills_scan', content: '', params: { model_id: 'model-1' }, attachment_ids: ['attachment-1'], country_iso_code: 'zh_CN', remark: '只扫描所上传技能' })
    await waitFor(() => expect(screen.getByLabelText('当前位置')).toHaveTextContent('/tasks/skills/skills%2Fopaque-1'))
  })

  it.each([
    ['非 ZIP', [new File(['x'], 'skill.tar')], /仅支持 ZIP/],
    ['多个 ZIP', [new File(['x'], 'a.zip'), new File(['x'], 'b.zip')], /恰好一个 ZIP/],
    ['超过 20 MiB', [new File(['x'], 'large.zip')], /不能超过 20 MiB/],
  ])('本地拒绝%s，不发送附件请求', async (caseName, files, message) => {
    if (caseName === '超过 20 MiB') Object.defineProperty(files[0], 'size', { value: 20 * 1024 * 1024 + 1 })
    const fetchMock = mockCreationFetch()
    renderRoute('/tasks/skills/new')
    await screen.findByRole('option', { name: /Skills 审核模型/ })
    fireEvent.change(screen.getByLabelText('Skills ZIP 包'), { target: { files } })
    fireEvent.click(screen.getByRole('button', { name: '上传 Skills 包' }))
    expect(await screen.findByText(message)).toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(([url]) => String(url).includes('/attachments'))).toHaveLength(0)
  })

  it('详情类型不符时隐藏摘要与取消按钮并停止轮询', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn().mockResolvedValue(json({ ...task, task_type: 'ai_infra_scan', input_summary: { target_count: 3 } }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(<TaskDetailPage expectedTaskType="skills_scan" returnTo="/tasks/skills" />, '/tasks/skills/task-1', '/tasks/skills/:taskId')
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(screen.getByText('该任务不属于 Skills 扫描')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: '任务安全摘要' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '取消任务' })).not.toBeInTheDocument()
    await act(async () => { await vi.advanceTimersByTimeAsync(120_000) })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('通用创建选择 Skills 后使用同一 ZIP 与模型表单', async () => {
    mockCreationFetch()
    renderRoute('/tasks/new')
    fireEvent.change(screen.getByRole('combobox', { name: '扫描类型' }), { target: { value: 'skills_scan' } })
    expect(screen.getByLabelText('Skills ZIP 包')).toBeInTheDocument()
    expect(screen.queryByLabelText('扫描目标或任务说明')).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '不使用模型' })).not.toBeInTheDocument()
    await screen.findByRole('option', { name: /Skills 审核模型/ })
    fireEvent.change(screen.getByRole('combobox', { name: '扫描类型' }), { target: { value: 'mcp_scan' } })
    expect(screen.getByRole('textbox', { name: '扫描目标或任务说明' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Skills ZIP 包')).not.toBeInTheDocument()
  })

  it('目录刷新期间阻止提交且刷新失败后保留待确认选择', async () => {
    const fetchMock = mockCreationFetch()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    renderRoute('/tasks/skills/new', 'user', queryClient)
    await screen.findByRole('option', { name: /Skills 审核模型/ })
    await uploadZIP()
    fireEvent.change(screen.getByRole('combobox', { name: '扫描模型' }), { target: { value: 'model-1' } })
    await waitFor(() => expect(screen.getByRole('button', { name: '创建 Skills 扫描任务' })).toBeEnabled())
    let resolveRefresh: ((response: Response) => void) | undefined
    fetchMock.mockImplementation(() => new Promise<Response>((resolve) => { resolveRefresh = resolve }))
    await act(async () => { void queryClient.invalidateQueries({ queryKey: ['governed-model-catalog'] }) })
    await waitFor(() => expect(screen.getByRole('button', { name: '创建 Skills 扫描任务' })).toBeDisabled())
    await act(async () => { resolveRefresh?.(new Response(null, { status: 500 })) })
    expect(await screen.findByText('模型目录刷新失败，当前选择待确认')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '扫描模型' })).toHaveValue('model-1')
    expect(screen.getByRole('button', { name: '创建 Skills 扫描任务' })).toBeDisabled()
  })

  it('上传未就绪的附件不能提交', async () => {
    const fetchMock = mockCreationFetch()
    fetchMock.mockImplementation((request: RequestInfo | URL) => {
      if (String(request).includes('/models?')) return Promise.resolve(json({ items: [model], total: 1, page: 1, page_size: 100 }))
      return Promise.resolve(json({ ...attachment, state: 'uploading' }))
    })
    renderRoute('/tasks/skills/new')
    await screen.findByRole('option', { name: /Skills 审核模型/ })
    fireEvent.change(screen.getByRole('combobox', { name: '扫描模型' }), { target: { value: 'model-1' } })
    fireEvent.change(screen.getByLabelText('Skills ZIP 包'), { target: { files: [new File(['zip!'], 'skills.zip')] } })
    fireEvent.click(screen.getByRole('button', { name: '上传 Skills 包' }))
    expect(await screen.findByText(/附件尚未就绪/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建 Skills 扫描任务' })).toBeDisabled()
  })

  it('显式重试复用幂等键，修改备注后换键并按码点限制备注', async () => {
    const fetchMock = mockCreationFetch()
    fetchMock.mockImplementation((request: RequestInfo | URL, init?: RequestInit) => {
      if (String(request).includes('/models?')) return Promise.resolve(json({ items: [model], total: 1, page: 1, page_size: 100 }))
      if (String(request).endsWith('/attachments')) return Promise.resolve(json(attachment))
      if (init?.method === 'POST') return Promise.reject(new TypeError('network'))
      return Promise.resolve(json(task))
    })
    renderRoute('/tasks/skills/new')
    await screen.findByRole('option', { name: /Skills 审核模型/ })
    await uploadZIP()
    fireEvent.change(screen.getByRole('combobox', { name: '扫描模型' }), { target: { value: 'model-1' } })
    const remark = screen.getByRole('textbox', { name: '任务说明 / 备注（可选）' })
    fireEvent.change(remark, { target: { value: '😀'.repeat(2_000) } })
    const submit = screen.getByRole('button', { name: '创建 Skills 扫描任务' })
    fireEvent.click(submit)
    await screen.findByText(/显式重试将复用同一幂等键/)
    fireEvent.click(submit)
    await waitFor(() => expect(fetchMock.mock.calls.filter(([url, init]) => String(url).endsWith('/tasks') && init?.method === 'POST')).toHaveLength(2))
    await waitFor(() => expect(submit).toBeEnabled())
    fireEvent.change(remark, { target: { value: '已修改' } })
    fireEvent.click(submit)
    await waitFor(() => expect(fetchMock.mock.calls.filter(([url, init]) => String(url).endsWith('/tasks') && init?.method === 'POST')).toHaveLength(3))
    const calls = fetchMock.mock.calls.filter(([url, init]) => String(url).endsWith('/tasks') && init?.method === 'POST')
    const keys = calls.map(([, init]) => new Headers(init?.headers).get('Idempotency-Key'))
    expect(keys[0]).toBeTruthy()
    expect(keys[1]).toBe(keys[0])
    expect(keys[2]).not.toBe(keys[0])
    await waitFor(() => expect(submit).toBeEnabled())
    fireEvent.change(remark, { target: { value: '😀'.repeat(2_001) } })
    expect(submit).toBeDisabled()
    expect(screen.getByText('任务说明不能超过 2,000 个字符。')).toBeInTheDocument()
  })

  it('切换 Skills 详情后不携带上个任务的取消不确定状态', async () => {
    const fetchMock = vi.fn((request: RequestInfo | URL) => {
      const url = String(request)
      if (url.endsWith('/cancel')) return Promise.reject(new TypeError('network'))
      return Promise.resolve(json({ ...task, id: url.endsWith('/task-second') ? 'task-second' : 'task-first', input_summary: { language: 'zh', scan_mode: 'static' } }))
    })
    vi.stubGlobal('fetch', fetchMock)
    renderPage(<><TaskDetailPage expectedTaskType="skills_scan" /><Link to="/tasks/skills/task-second">切换任务</Link></>, '/tasks/skills/task-first', '/tasks/skills/:taskId')
    fireEvent.click(await screen.findByRole('button', { name: '取消任务' }))
    await screen.findByText('网络确认中断，已重新读取任务状态，未自动重复取消。')
    expect(fetchMock.mock.calls.filter(([url]) => String(url).endsWith('/cancel'))).toHaveLength(1)
    fireEvent.click(screen.getByRole('link', { name: '切换任务' }))
    await screen.findByRole('region', { name: '任务安全摘要' })
    expect(screen.queryByText('网络确认中断，已重新读取任务状态，未自动重复取消。')).not.toBeInTheDocument()
    expect(screen.getByText('静态扫描')).toBeInTheDocument()
  })

  it('离开上传页后中止请求，重新进入时不恢复旧附件和备注', async () => {
    const fetchMock = mockCreationFetch()
    let uploadSignal: AbortSignal | undefined
    fetchMock.mockImplementation((request: RequestInfo | URL, init?: RequestInit) => {
      if (String(request).includes('/models?')) return Promise.resolve(json({ items: [model], total: 1, page: 1, page_size: 100 }))
      uploadSignal = init?.signal ?? undefined
      return new Promise<Response>(() => undefined)
    })
    const view = renderRoute('/tasks/skills/new')
    await screen.findByRole('option', { name: /Skills 审核模型/ })
    fireEvent.change(screen.getByRole('textbox', { name: '任务说明 / 备注（可选）' }), { target: { value: '旧任务备注' } })
    fireEvent.change(screen.getByLabelText('Skills ZIP 包'), { target: { files: [new File(['zip!'], 'skills.zip')] } })
    fireEvent.click(screen.getByRole('button', { name: '上传 Skills 包' }))
    await waitFor(() => expect(uploadSignal).toBeDefined())
    view.unmount()
    expect(uploadSignal?.aborted).toBe(true)
    renderRoute('/tasks/skills/new')
    expect(screen.getByRole('textbox', { name: '任务说明 / 备注（可选）' })).toHaveValue('')
    expect(screen.queryByLabelText('已上传 Skills 包')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '创建 Skills 扫描任务' })).toBeDisabled()
  })
})
