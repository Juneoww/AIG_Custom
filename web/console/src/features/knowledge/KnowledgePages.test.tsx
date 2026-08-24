/**
 * 功能：验证六类知识页面、角色写边界、真实原文编辑和嵌套路由。
 * 实现：渲染生产路由树并按 URL 返回严格 legacy fixture，观察真实 fetch。
 * 输入：三角色会话、列表/原文响应、保存失败和管理员确认操作。
 * 输出：全角色只读页、管理员治理按钮、403 与单次原文写请求。
 * 依赖：Testing Library、React Router、生产 Providers 与知识页面。
 */
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AppContent } from '../../app/App'
import { AppProviders, createAppQueryClient } from '../../app/providers/AppProviders'
import type { SessionState } from '../auth/session'
import type { SubjectRole } from '../../shared/api/types'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'

function subjectState(role: SubjectRole): SessionState {
  return { status: 'authenticated', subject: { id: `${role}-1`, username: `${role}-operator`, role, must_change_password: false } }
}

function legacy(data: unknown, status = 0, message = 'success'): Response {
  return new Response(JSON.stringify({ status, message, data }), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

function knowledgeFetch(urlValue: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const url = new URL(String(urlValue))
  if (init?.method && init.method !== 'GET') return Promise.resolve(legacy(null, 0, 'saved'))
  if (url.pathname.endsWith('/raw')) return Promise.resolve(legacy({ content: '# original comment\ninfo:\n  name: dify\n' }))
  if (url.pathname.endsWith('/fingerprints')) return Promise.resolve(legacy({ total: 1, page: 1, size: 20, items: [{ info: { name: 'dify', author: 'lab', desc: 'AI app', severity: 'info', recommendation: 9 }, http: [], version: [] }] }))
  if (url.pathname.endsWith('/vulnerabilities')) return Promise.resolve(legacy({ total: 1, page: 1, size: 20, items: [{ info: { name: 'dify', cve: 'CVE-2026-1', summary: 'summary', details: 'details', cvss: '9.8', severity: 'high', security_advise: 'fix', references: [], author: 'lab' }, rule: 'version < 2', references: [] }] }))
  if (url.pathname.endsWith('/evaluations')) return Promise.resolve(legacy({ total: 1, page: 1, size: 20, items: [{ name: 'safe', description: 'desc', count: 1, default: false }] }))
  if (url.pathname.endsWith('/mcp')) return Promise.resolve(legacy({ total: 1, items: [{ info: { id: 'cors', name: 'CORS', description: 'desc', author: 'lab', category: ['code'] }, RawData: 'info:\n  id: cors\n' }] }))
  if (url.pathname.endsWith('/prompt_collections')) return Promise.resolve(legacy({ total: 1, items: [{ id: 'prompt-1', product: 'product', affiliation: 'lab', model_version: 'v1', prompt: 'prompt', code_exec: false, upload_file: false, multi_modal: false, web_search: false, sec_policies: true }] }))
  if (url.pathname.endsWith('/agent/names')) return Promise.resolve(legacy(['openai']))
  if (url.pathname.endsWith('/agent/openai')) return Promise.resolve(legacy('type: http\nlabel: openai\n'))
  return new Promise<Response>(() => undefined)
}

function renderRoute(role: SubjectRole, path: string) {
  const queryClient = createAppQueryClient()
  const view = render(
    <ThemeProvider initialMode="light">
      <AppProviders queryClient={queryClient} sessionInitialState={subjectState(role)}>
        <MemoryRouter initialEntries={[path]}><AppContent /></MemoryRouter>
      </AppProviders>
    </ThemeProvider>,
  )
  return { ...view, queryClient }
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('fetch', vi.fn(knowledgeFetch))
})

afterEach(() => vi.unstubAllGlobals())

describe('knowledge routes and role matrix', () => {
  it.each([
    ['/knowledge/fingerprints', '指纹规则'],
    ['/knowledge/vulnerabilities', '漏洞规则'],
    ['/knowledge/evaluations', '安全评测集'],
    ['/knowledge/mcp', 'MCP 插件'],
    ['/knowledge/prompts', 'Prompt 集合'],
    ['/knowledge/agents', 'Agent 配置'],
  ] as const)('让全部已登录角色读取%s真实页面', async (path, heading) => {
    for (const role of ['user', 'auditor', 'admin'] as const) {
      const view = renderRoute(role, path)
      expect(await screen.findByRole('heading', { name: heading })).toBeInTheDocument()
      expect(screen.getByRole('navigation', { name: '知识库分类' })).toBeInTheDocument()
      expect(screen.queryByText('规则与知识库尚未接入')).not.toBeInTheDocument()
      view.unmount()
    }
  })

  it('普通用户与审计员只读，管理员可见治理动作', async () => {
    const userView = renderRoute('user', '/knowledge/fingerprints')
    expect(await screen.findByRole('table')).toHaveProperty('tagName', 'TABLE')
    expect(screen.queryByRole('button', { name: '新增指纹规则' })).not.toBeInTheDocument()
    userView.unmount()

    renderRoute('admin', '/knowledge/fingerprints')
    expect(await screen.findByRole('button', { name: '新增指纹规则' })).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: '编辑 dify' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '删除 dify' })).toBeInTheDocument()
  })

  it('管理员管理直达路由已知，其他角色直接访问显示403', async () => {
    renderRoute('auditor', '/knowledge/prompts/manage')
    expect(await screen.findByRole('heading', { name: '无权访问' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '页面不存在' })).not.toBeInTheDocument()
  })
})

describe('governed knowledge edits', () => {
  it('原文进入ready后不会因页面重渲染重复读取', async () => {
    let rawRequests = 0
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/fingerprints/demo/raw')) {
        rawRequests += 1
        return Promise.resolve(legacy({ content: 'info:\n  name: demo\n' }))
      }
      if (url.pathname.endsWith('/fingerprints')) return Promise.resolve(legacy({ total: 1, page: 1, size: 20, items: [{ info: { name: 'demo', author: 'lab', desc: 'desc', severity: 'info', recommendation: 1 }, http: [], version: [] }] }))
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/fingerprints')
    fireEvent.click(await screen.findByRole('button', { name: '查看 demo' }))
    expect(await screen.findByRole('textbox', { name: '指纹规则原文' })).toHaveValue('info:\n  name: demo\n')
    await act(async () => { await new Promise((resolve) => window.setTimeout(resolve, 50)) })

    expect(rawRequests).toBe(1)
  })

  it('原文仅按需保存在页面状态且不进入 Query cache', async () => {
    let fingerprintRawRequests = 0
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/fingerprints/demo/raw')) {
        fingerprintRawRequests += 1
        return Promise.resolve(legacy({ content: 'raw-token-sentinel' }))
      }
      if (url.pathname.endsWith('/fingerprints')) return Promise.resolve(legacy({ total: 1, page: 1, size: 20, items: [{ info: { name: 'demo', author: 'lab', desc: 'desc', severity: 'info', recommendation: 1 }, http: [], version: [] }] }))
      if (url.pathname.endsWith('/mcp')) return Promise.resolve(legacy({ total: 1, items: [{ info: { id: 'mcp-one', name: 'MCP', description: '', author: '', category: [] }, RawData: 'mcp-token-sentinel' }] }))
      if (url.pathname.endsWith('/agent/names')) return Promise.resolve(legacy(['agent-one']))
      if (url.pathname.endsWith('/agent/agent-one')) return Promise.resolve(legacy('agent-token-sentinel'))
      return knowledgeFetch(urlValue, init)
    }))

    const fingerprint = renderRoute('admin', '/knowledge/fingerprints')
    fireEvent.click(await screen.findByRole('button', { name: '查看 demo' }))
    expect(await screen.findByRole('textbox', { name: '指纹规则原文' })).toHaveValue('raw-token-sentinel')
    await new Promise((resolve) => window.setTimeout(resolve, 20))
    expect(fingerprintRawRequests).toBe(1)
    expect(JSON.stringify(fingerprint.queryClient.getQueryCache().getAll().map((query) => query.state.data))).not.toContain('raw-token-sentinel')
    fingerprint.unmount()

    const mcp = renderRoute('admin', '/knowledge/mcp')
    expect(await screen.findByText('mcp-one')).toBeInTheDocument()
    expect(JSON.stringify(mcp.queryClient.getQueryCache().getAll().map((query) => query.state.data))).not.toContain('mcp-token-sentinel')
    mcp.unmount()

    const agent = renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '查看 agent-one' }))
    expect(await screen.findByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('agent-token-sentinel')
    expect(JSON.stringify(agent.queryClient.getQueryCache().getAll().map((query) => query.state.data))).not.toContain('agent-token-sentinel')
  })

  it('原文写请求忽略 abort 时切换资源仍释放互斥并清理旧提交态', async () => {
    const pendingWrite = new Promise<Response>(() => undefined)
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (init?.method === 'PUT') return pendingWrite
      if (url.pathname.endsWith('/fingerprints/one/raw')) return Promise.resolve(legacy({ content: 'info:\n  name: one\n' }))
      if (url.pathname.endsWith('/fingerprints/two/raw')) return Promise.resolve(legacy({ content: 'info:\n  name: two\n' }))
      if (url.pathname.endsWith('/fingerprints')) return Promise.resolve(legacy({ total: 2, page: 1, size: 20, items: [
        { info: { name: 'one', author: '', desc: '', severity: '', recommendation: 0 }, http: [], version: [] },
        { info: { name: 'two', author: '', desc: '', severity: '', recommendation: 0 }, http: [], version: [] },
      ] }))
      return knowledgeFetch(urlValue, init)
    }))
    renderRoute('admin', '/knowledge/fingerprints')
    fireEvent.click(await screen.findByRole('button', { name: '编辑 one' }))
    const firstSave = await screen.findByRole('button', { name: '保存指纹规则' })
    await waitFor(() => expect(firstSave).toBeEnabled())
    fireEvent.click(firstSave)
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }))
    fireEvent.click(screen.getByRole('button', { name: '编辑 two' }))

    expect(await screen.findByRole('textbox', { name: '指纹规则原文' })).toHaveValue('info:\n  name: two\n')
    expect(screen.getByRole('button', { name: '保存指纹规则' })).toBeEnabled()
  })

  it('Prompt 写请求忽略 abort 时切换对象不会把新表单永久禁用', async () => {
    const pendingWrite = new Promise<Response>(() => undefined)
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (init?.method === 'PUT') return pendingWrite
      if (url.pathname.endsWith('/prompt_collections')) return Promise.resolve(legacy({ total: 2, items: [
        { id: 'prompt-a', product: 'A', affiliation: '', model_version: '', prompt: 'A prompt', code_exec: false, upload_file: false, multi_modal: false, web_search: false, sec_policies: true },
        { id: 'prompt-b', product: 'B', affiliation: '', model_version: '', prompt: 'B prompt', code_exec: false, upload_file: false, multi_modal: false, web_search: false, sec_policies: true },
      ] }))
      return knowledgeFetch(urlValue, init)
    }))
    renderRoute('admin', '/knowledge/prompts')
    fireEvent.click(await screen.findByRole('button', { name: '编辑 prompt-a' }))
    fireEvent.click(screen.getByRole('button', { name: '保存 Prompt 集合' }))
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }))
    fireEvent.click(screen.getByRole('button', { name: '编辑 prompt-b' }))

    expect(screen.getByRole('textbox', { name: '产品' })).toHaveValue('B')
    expect(screen.getByRole('button', { name: '保存 Prompt 集合' })).toBeEnabled()
  })

  it('读取原始YAML后保存前二次确认，PUT保留注释且只发送一次', async () => {
    const fetchMock = vi.mocked(fetch)
    renderRoute('admin', '/knowledge/fingerprints')
    fireEvent.click(await screen.findByRole('button', { name: '编辑 dify' }))
    const editor = await screen.findByRole('textbox', { name: '指纹规则原文' })
    expect(editor).toHaveValue('# original comment\ninfo:\n  name: dify\n')
    fireEvent.change(editor, { target: { value: '# keep comment\ninfo:\n  name: dify\n' } })
    fireEvent.click(screen.getByRole('button', { name: '保存指纹规则' }))
    expect(screen.getByRole('dialog', { name: '确认保存指纹规则' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(
      'http://localhost:3000/api/v1/knowledge/fingerprints/dify',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ file_content: '# keep comment\ninfo:\n  name: dify\n' }) }),
    ))
    expect(fetchMock.mock.calls.filter((call) => (call[1] as RequestInit | undefined)?.method === 'PUT')).toHaveLength(1)
  })

  it('legacy保存失败显示固定中文并清空编辑内容，不泄露服务端路径', async () => {
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'PUT') return Promise.resolve(legacy(null, 1, 'C:\\private\\rule.yaml TOKEN_SENTINEL'))
      return knowledgeFetch(urlValue, init)
    }))
    renderRoute('admin', '/knowledge/fingerprints')
    fireEvent.click(await screen.findByRole('button', { name: '编辑 dify' }))
    const saveButton = await screen.findByRole('button', { name: '保存指纹规则' })
    await waitFor(() => expect(saveButton).toBeEnabled())
    fireEvent.click(saveButton)
    fireEvent.click(screen.getByRole('button', { name: '确认保存' }))

    expect(await screen.findByText('指纹规则保存失败，请显式重试。')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '指纹规则原文' })).toHaveValue('')
    expect(document.body).not.toHaveTextContent(/private|TOKEN_SENTINEL/)
  })

  it('Prompt管理员保留创建和删除，普通用户无按钮', async () => {
    const fetchMock = vi.mocked(fetch)
    const user = renderRoute('user', '/knowledge/prompts')
    expect(await screen.findByText('prompt-1')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '新增 Prompt 集合' })).not.toBeInTheDocument()
    user.unmount()

    renderRoute('admin', '/knowledge/prompts')
    fireEvent.click(await screen.findByRole('button', { name: '删除 prompt-1' }))
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(fetchMock.mock.calls.some((call) => new URL(String(call[0])).pathname === '/api/v1/knowledge/prompt_collections/prompt-1' && (call[1] as RequestInit).method === 'DELETE')).toBe(true))
  })

  it('Agent管理员可执行连通性与Prompt测试，审计员只读', async () => {
    const auditor = renderRoute('auditor', '/knowledge/agents')
    expect(await screen.findByText('openai')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '测试连通性' })).not.toBeInTheDocument()
    auditor.unmount()

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '查看 openai' }))
    expect(await screen.findByRole('button', { name: '测试连通性' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Prompt 测试' })).toBeInTheDocument()
  })
})
