/**
 * 功能：验证六类知识页面、角色写边界、真实原文编辑和嵌套路由。
 * 实现：渲染生产路由树并按 URL 返回严格 legacy fixture，观察真实 fetch。
 * 输入：三角色会话、列表/原文响应、保存失败和管理员确认操作。
 * 输出：全角色只读页、管理员治理按钮、403 与单次原文写请求。
 * 依赖：Testing Library、React Router、生产 Providers 与知识页面。
 */
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
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

const governedFingerprintItems = [
  { info: { name: 'dify', author: 'aig-lab', desc: 'Dify 应用识别规则', severity: 'info', recommendation: 9 }, http: [], version: [] },
  { info: { name: 'langchain', author: 'aig-lab', desc: 'LangChain 组件识别规则', severity: 'medium', recommendation: 6 }, http: [], version: [] },
  { info: { name: 'n8n', author: 'aig-lab', desc: 'n8n 自动化组件识别规则', severity: 'low', recommendation: 3 }, http: [], version: [] },
] as const
const mcpRawDataSentinel = 'MCP_RAWDATA_TOKEN_SENTINEL'
const mcpTokenLikeSentinel = 'MCP_TOKEN_LIKE_SENTINEL'
const knowledgeRouteMatrix = [
  ['/knowledge/fingerprints', '指纹规则'],
  ['/knowledge/vulnerabilities', '漏洞规则'],
  ['/knowledge/evaluations', '安全评测集'],
  ['/knowledge/mcp', 'MCP 插件'],
  ['/knowledge/prompts', 'Prompt 集合'],
  ['/knowledge/agents', 'Agent 配置'],
] as const

function governedFingerprintPage({
  total = 45,
  page = 2,
  size = 20,
  items = governedFingerprintItems,
}: {
  total?: number
  page?: number
  size?: number
  items?: readonly unknown[]
} = {}): Response {
  return legacy({ total, page, size, items })
}

function httpFailure(status: 403 | 500): Response {
  return new Response(null, { status })
}

function mcpCatalogFixture(): Response {
  return legacy({
    total: 1,
    items: [{
      info: { id: 'cors', name: 'CORS', description: 'desc', author: 'lab', category: ['code'] },
      RawData: `info:\n  id: cors\n  api_key: ${mcpRawDataSentinel}\n`,
      token: mcpTokenLikeSentinel,
    }],
  })
}

function agentTemplateFixture(): Response {
  return new Response(JSON.stringify({
    openai: {
      name: 'OpenAI',
      description: '受控 OpenAI 配置模板',
      fields: [
        { field: 'base_url', label: '服务地址', type: 'text', required: true },
        { field: 'api_key', label: '访问密钥', type: 'password', required: true },
      ],
    },
    common: {
      fields: [
        { field: 'timeout', label: '超时', type: 'number', required: false, min: 1 },
      ],
    },
  }), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function expectAgentWorkbenchControlsHidden() {
  expect(screen.queryByRole('textbox', { name: '配置名称' })).not.toBeInTheDocument()
  expect(screen.queryByRole('textbox', { name: 'Agent 配置原文' })).not.toBeInTheDocument()
  expect(screen.queryByRole('textbox', { name: '测试 Prompt' })).not.toBeInTheDocument()
  for (const label of ['保存 Agent 配置', '删除 Agent 配置', '测试连通性', 'Prompt 测试']) {
    expect(screen.queryByRole('button', { name: label })).not.toBeInTheDocument()
  }
}

function changeTextAreaWithinCurrentAct(textarea: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
  if (!setter) throw new Error('textarea value setter is unavailable')
  setter.call(textarea, value)
  textarea.dispatchEvent(new Event('input', { bubbles: true }))
}

function knowledgeFetch(urlValue: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const url = new URL(String(urlValue))
  if (init?.method && init.method !== 'GET') return Promise.resolve(legacy(null, 0, 'saved'))
  if (url.pathname.endsWith('/raw')) return Promise.resolve(legacy({ content: '# original comment\ninfo:\n  name: dify\n' }))
  if (url.pathname.endsWith('/fingerprints')) return Promise.resolve(legacy({ total: 1, page: 1, size: 20, items: [{ info: { name: 'dify', author: 'lab', desc: 'AI app', severity: 'info', recommendation: 9 }, http: [], version: [] }] }))
  if (url.pathname.endsWith('/vulnerabilities')) return Promise.resolve(legacy({ total: 1, page: 1, size: 20, items: [{ info: { name: 'dify', cve: 'CVE-2026-1', summary: 'summary', details: 'details', cvss: '9.8', severity: 'high', security_advise: 'fix', references: [], author: 'lab' }, rule: 'version < 2', references: [] }] }))
  if (url.pathname.endsWith('/evaluations')) return Promise.resolve(legacy({ total: 1, page: 1, size: 20, items: [{ name: 'safe', description: 'desc', count: 1, default: false }] }))
  if (url.pathname.endsWith('/mcp')) return Promise.resolve(mcpCatalogFixture())
  if (url.pathname.endsWith('/prompt_collections')) return Promise.resolve(legacy({ total: 1, items: [{ id: 'prompt-1', product: 'product', affiliation: 'lab', model_version: 'v1', prompt: 'prompt', code_exec: false, upload_file: false, multi_modal: false, web_search: false, sec_policies: true }] }))
  if (url.pathname.endsWith('/agent/template') && url.searchParams.get('language') === 'zh') return Promise.resolve(agentTemplateFixture())
  if (url.pathname.endsWith('/agent/names')) return Promise.resolve(legacy(['openai']))
  if (url.pathname.endsWith('/agent/openai')) return Promise.resolve(legacy('type: http\nlabel: openai\n'))
  return new Promise<Response>(() => undefined)
}

function LocationProbe() {
  const location = useLocation()
  return <output aria-label="当前路由地址">{location.pathname + location.search}</output>
}

function renderRoute(role: SubjectRole, path: string, { locationProbe = false }: { locationProbe?: boolean } = {}) {
  const queryClient = createAppQueryClient()
  const view = render(
    <ThemeProvider initialMode="light">
      <AppProviders queryClient={queryClient} sessionInitialState={subjectState(role)}>
        <MemoryRouter initialEntries={[path]}>
          {locationProbe ? <LocationProbe /> : null}
          <AppContent />
        </MemoryRouter>
      </AppProviders>
    </ThemeProvider>,
  )
  return { ...view, queryClient }
}

function fingerprintFailureTitle(status: 403 | 500): string {
  return status === 403 ? '无权查看指纹规则' : '指纹规则目录加载失败'
}

async function expectFingerprintFailureState(status: 403 | 500) {
  const panel = await screen.findByRole(status === 403 ? 'status' : 'alert')
  expect(panel).toHaveTextContent(fingerprintFailureTitle(status))
}

function expectFingerprintCatalogUIToBeHidden() {
  expect(screen.queryByRole('region', { name: '资产目录概览' })).not.toBeInTheDocument()
  expect(screen.queryByRole('table', { name: '指纹规则台账' })).not.toBeInTheDocument()
  for (const name of ['dify', 'langchain', 'n8n']) {
    expect(screen.queryByRole('button', { name: '查看 ' + name })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '编辑 ' + name })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '删除 ' + name })).not.toBeInTheDocument()
  }
  expect(screen.queryAllByText('匹配资源 45')).toHaveLength(0)
  expect(screen.queryAllByText('服务器第 2 页')).toHaveLength(0)
  expect(screen.queryByText('共 45 条，第 2 页')).not.toBeInTheDocument()
  expect(screen.queryByRole('navigation', { name: '指纹规则分页' })).not.toBeInTheDocument()
}

function installFingerprintRefetchFailure(status: 403 | 500) {
  let fingerprintRequests = 0
  vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(urlValue))
    if (url.pathname.endsWith('/fingerprints')) {
      fingerprintRequests += 1
      return Promise.resolve(fingerprintRequests === 1 ? governedFingerprintPage() : httpFailure(status))
    }
    return knowledgeFetch(urlValue, init)
  }))
  return () => fingerprintRequests
}

function agentFailureTitle(status: 403 | 500): string {
  return status === 403 ? '无权查看 Agent 配置' : 'Agent 配置加载失败'
}

async function expectAgentFailureState(status: 403 | 500) {
  const panel = await screen.findByRole(status === 403 ? 'status' : 'alert')
  expect(panel).toHaveTextContent(agentFailureTitle(status))
}

function expectAgentCatalogUIToBeHidden() {
  expect(screen.queryByRole('region', { name: 'Agent 配置目录概览' })).not.toBeInTheDocument()
  expect(screen.queryByRole('table', { name: 'Agent 配置台账' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '进入工作区 agent-one' })).not.toBeInTheDocument()
}

function installAgentRefetchFailure(status: 403 | 500) {
  let agentNameRequests = 0
  vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(urlValue))
    if (url.pathname.endsWith('/agent/names')) {
      agentNameRequests += 1
      return Promise.resolve(agentNameRequests === 1 ? legacy(['agent-one']) : httpFailure(status))
    }
    if (url.pathname.endsWith('/agent/agent-one')) return Promise.resolve(legacy('agent-token-sentinel'))
    return knowledgeFetch(urlValue, init)
  }))
  return () => agentNameRequests
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('fetch', vi.fn(knowledgeFetch))
})

afterEach(() => vi.unstubAllGlobals())

describe('knowledge routes and role matrix', () => {
  it.each(knowledgeRouteMatrix)('让全部已登录角色读取%s真实页面', async (path, heading) => {
    for (const role of ['user', 'auditor', 'admin'] as const) {
      const view = renderRoute(role, path)
      expect(await screen.findByRole('heading', { name: heading })).toBeInTheDocument()
      const navigation = screen.getByRole('navigation', { name: '知识库分类' })
      const activeLink = within(navigation).getByRole('link', { name: heading })
      expect(activeLink).toHaveAttribute('href', path)
      expect(activeLink).toHaveAttribute('aria-current', 'page')
      const currentLinks = within(navigation).getAllByRole('link').filter((link) => {
        const current = link.getAttribute('aria-current')
        return current !== null && current !== 'false'
      })
      expect(currentLinks).toEqual([activeLink])
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

describe('knowledge catalog governance contracts', () => {
  it('知识库分类按扫描、评测扩展和配置资产分组展示', async () => {
    renderRoute('admin', '/knowledge/fingerprints')

    const navigation = await screen.findByRole('navigation', { name: '知识库分类' })
    expect(within(navigation).getByText('扫描规则')).toBeInTheDocument()
    expect(within(navigation).getByText('评测与扩展')).toBeInTheDocument()
    expect(within(navigation).getByText('配置资产')).toBeInTheDocument()
    for (const [path, label] of knowledgeRouteMatrix) {
      expect(within(navigation).getByRole('link', { name: label })).toHaveAttribute('href', path)
    }
    expect(within(navigation).getByRole('link', { name: '指纹规则' })).toHaveAttribute('aria-current', 'page')
  })

  it('服务器分页的指纹目录先展示治理概览和当前资源范围', async () => {
    const fetchMock = vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/fingerprints')) return Promise.resolve(governedFingerprintPage())
      return knowledgeFetch(urlValue, init)
    })
    vi.stubGlobal('fetch', fetchMock)

    renderRoute('admin', '/knowledge/fingerprints?page=2&q=dify')

    const overview = await screen.findByRole('region', { name: '资产目录概览' })
    const ledger = screen.getByRole('table', { name: '指纹规则台账' })
    const scope = within(overview).getByRole('group', { name: '当前资源范围' })
    expect(overview.compareDocumentPosition(ledger) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
    expect(scope).toHaveTextContent('匹配资源 45')
    expect(scope).toHaveTextContent('服务器第 2 页')
    expect(within(overview).getByText('本页资产 3')).toBeInTheDocument()
    expect(within(overview).getByText('可治理')).toBeInTheDocument()
    expect(screen.queryByText(/(?:全局|总体).*(?:风险|健康)|(?:风险|健康).*(?:全局|总体)/)).not.toBeInTheDocument()

    const pagination = screen.getByRole('navigation', { name: '指纹规则分页' })
    expect(pagination).toHaveTextContent('共 45 条，第 2 页')
    expect(within(pagination).getByRole('button', { name: '上一页' })).toBeEnabled()
    expect(within(pagination).getByRole('button', { name: '下一页' })).toBeEnabled()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByText('暂无指纹规则')).not.toBeInTheDocument()
    expect(screen.queryByText('当前页没有指纹规则')).not.toBeInTheDocument()
    const fingerprintRequests = fetchMock.mock.calls
      .map(([urlValue]) => new URL(String(urlValue)))
      .filter((url) => url.pathname.endsWith('/fingerprints'))
    expect(fingerprintRequests.some((url) =>
      url.searchParams.get('page') === '2' && url.searchParams.get('size') === '20' && url.searchParams.get('q') === 'dify',
    )).toBe(true)
  })

  it('960/390 窄屏结构只让指纹台账视口承接横向滚动', async () => {
    renderRoute('admin', '/knowledge/fingerprints')

    const ledger = await screen.findByRole('table', { name: '指纹规则台账' })
    const pageRoot = ledger.closest('section')
    const tableViewport = ledger.parentElement
    expect(pageRoot).not.toBeNull()
    expect(tableViewport).not.toBeNull()
    expect(tableViewport).not.toBe(pageRoot)

    const page = pageRoot as HTMLElement
    const viewport = tableViewport as HTMLElement
    // jsdom 不计算 960/390 的真实几何；验证两个断点共用的收缩和滚动容器结构。
    expect(page).toHaveStyle({ minWidth: '0' })
    expect(viewport).toHaveStyle({ minWidth: '0', overflowX: 'auto' })
    const horizontalScrollers = Array.from(page.querySelectorAll<HTMLElement>('*'))
      .filter((element) => ['auto', 'scroll'].includes(window.getComputedStyle(element).overflowX))
    expect(horizontalScrollers).toEqual([viewport])
  })

  it('零总量指纹目录仍保留概览上下文而不显示零值本页资产', async () => {
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/fingerprints')) return Promise.resolve(governedFingerprintPage({ total: 0, page: 1, items: [] }))
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/fingerprints?q=dify')

    const overview = await screen.findByRole('region', { name: '资产目录概览' })
    expect(within(overview).getByRole('group', { name: '当前资源范围' })).toHaveTextContent('匹配资源 0')
    expect(await screen.findByRole('status')).toHaveTextContent('暂无指纹规则')
    expect(screen.queryByText('本页资产 0')).not.toBeInTheDocument()
    const previous = screen.queryByRole('button', { name: '上一页' })
    expect(previous === null || previous.hasAttribute('disabled')).toBe(true)
  })

  it('服务器返回空页时保留总量、返回页码和可用上一页', async () => {
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/fingerprints')) return Promise.resolve(governedFingerprintPage({ page: 3, items: [] }))
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/fingerprints?page=3&q=dify')

    const overview = await screen.findByRole('region', { name: '资产目录概览' })
    const scope = within(overview).getByRole('group', { name: '当前资源范围' })
    expect(scope).toHaveTextContent('匹配资源 45')
    expect(scope).toHaveTextContent('服务器第 3 页')
    expect(await screen.findByRole('status')).toHaveTextContent('当前页没有指纹规则')
    expect(screen.queryByText('暂无指纹规则')).not.toBeInTheDocument()
    expect(screen.queryByText('本页资产 0')).not.toBeInTheDocument()

    const pagination = screen.getByRole('navigation', { name: '指纹规则分页' })
    expect(pagination).toHaveTextContent('共 45 条，第 3 页')
    expect(within(pagination).getByRole('button', { name: '上一页' })).toBeEnabled()
  })

  it('MCP 完整目录移除遗留 URL 分页与筛选语义', async () => {
    const fetchMock = vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => knowledgeFetch(urlValue, init))
    vi.stubGlobal('fetch', fetchMock)

    renderRoute('admin', '/knowledge/mcp?page=2&q=x', { locationProbe: true })

    expect(await screen.findByText('cors')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByLabelText('当前路由地址')).toHaveTextContent(/^\/knowledge\/mcp$/))

    const mcpRequests = fetchMock.mock.calls.filter(([urlValue]) => new URL(String(urlValue)).pathname.endsWith('/mcp'))
    expect(mcpRequests.length).toBeGreaterThan(0)
    expect(mcpRequests.every(([urlValue]) => new URL(String(urlValue)).search === '')).toBe(true)
    expect(screen.getByText('当前目录 1 项')).toBeInTheDocument()
    expect(screen.queryByRole('form', { name: 'MCP 插件筛选' })).not.toBeInTheDocument()
    expect(document.body).not.toHaveTextContent(/本页|服务器第|第\s*\d+\s*页/)
    expect(screen.queryByRole('button', { name: '上一页' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '下一页' })).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'MCP 插件分页' })).not.toBeInTheDocument()
  })

  it('MCP 目录不将 RawData 或令牌样字段投影到概览和台账', async () => {
    renderRoute('admin', '/knowledge/mcp')

    const ledger = await screen.findByRole('table', { name: 'MCP 插件台账' })
    expect(screen.getByText('cors')).toBeInTheDocument()
    expect(ledger).not.toHaveTextContent(mcpRawDataSentinel)
    expect(ledger).not.toHaveTextContent(mcpTokenLikeSentinel)
    expect(document.body).not.toHaveTextContent(mcpRawDataSentinel)
    expect(document.body).not.toHaveTextContent(mcpTokenLikeSentinel)
  })
})

describe('fingerprint catalog cache-safety contracts', () => {
  it.each([403, 500] as const)('目录刷新返回真实 HTTP %i 时不保留旧派生 UI', async (status) => {
    const fingerprintRequestCount = installFingerprintRefetchFailure(status)
    const view = renderRoute('admin', '/knowledge/fingerprints?page=2&q=dify')
    expect(await screen.findByRole('table', { name: '指纹规则台账' })).toBeInTheDocument()
    await waitFor(() => expect(fingerprintRequestCount()).toBe(1))

    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: ['knowledge', 'fingerprints'] })
    })
    await waitFor(() => expect(fingerprintRequestCount()).toBe(2))

    await expectFingerprintFailureState(status)
    expectFingerprintCatalogUIToBeHidden()
  })

  it.each([403, 500] as const)('查看原文后目录刷新返回真实 HTTP %i 仍保留可读原文编辑器', async (status) => {
    const fingerprintRequestCount = installFingerprintRefetchFailure(status)
    const view = renderRoute('admin', '/knowledge/fingerprints?page=2&q=dify')
    fireEvent.click(await screen.findByRole('button', { name: '查看 dify' }))
    const editor = await screen.findByRole('textbox', { name: '指纹规则原文' })
    expect(editor).toHaveValue('# original comment\ninfo:\n  name: dify\n')
    await waitFor(() => expect(fingerprintRequestCount()).toBe(1))

    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: ['knowledge', 'fingerprints'] })
    })
    await waitFor(() => expect(fingerprintRequestCount()).toBe(2))

    await expectFingerprintFailureState(status)
    expect(screen.getByRole('textbox', { name: '指纹规则原文' })).toHaveValue('# original comment\ninfo:\n  name: dify\n')
    expectFingerprintCatalogUIToBeHidden()
  })
})

describe('agent configuration catalog cache-safety contracts', () => {
  it.each([403, 500] as const)('管理员已进入本地工作区后，目录刷新返回真实 HTTP %i 时移除目录派生 UI', async (status) => {
    const agentNameRequestCount = installAgentRefetchFailure(status)
    const view = renderRoute('admin', '/knowledge/agents')

    const overview = await screen.findByRole('region', { name: 'Agent 配置目录概览' })
    const ledger = screen.getByRole('table', { name: 'Agent 配置台账' })
    expect(overview).not.toHaveTextContent('agent-token-sentinel')
    expect(ledger).not.toHaveTextContent('agent-token-sentinel')

    fireEvent.click(screen.getByRole('button', { name: '进入工作区 agent-one' }))
    expect(await screen.findByText('当前工作区')).toBeInTheDocument()
    expect(await screen.findByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('agent-token-sentinel')
    expect(JSON.stringify(view.queryClient.getQueryCache().getAll().map((query) => query.state.data))).not.toContain('agent-token-sentinel')
    await waitFor(() => expect(agentNameRequestCount()).toBe(1))

    await act(async () => {
      await view.queryClient.invalidateQueries({ queryKey: ['knowledge', 'agents'] })
    })
    await waitFor(() => expect(agentNameRequestCount()).toBe(2))

    await expectAgentFailureState(status)
    expectAgentCatalogUIToBeHidden()
    expect(screen.getByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('agent-token-sentinel')
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
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 agent-one' }))
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

  it('Agent 原文加载与失败时只保留局部状态、重试和关闭', async () => {
    const rawResponse = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/agent/names')) return Promise.resolve(legacy(['agent-one']))
      if (url.pathname.endsWith('/agent/agent-one')) return rawResponse.promise
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 agent-one' }))

    expect(await screen.findByText('正在加载 Agent 配置原文')).toBeInTheDocument()
    expectAgentWorkbenchControlsHidden()
    expect(screen.getByRole('button', { name: '关闭' })).toBeEnabled()

    await act(async () => rawResponse.resolve(httpFailure(500)))

    expect(await screen.findByText('Agent 配置原文加载失败')).toBeInTheDocument()
    expectAgentWorkbenchControlsHidden()
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled()
    expect(screen.getByRole('button', { name: '关闭' })).toBeEnabled()
  })

  it('Agent 从已就绪配置切换到下一份原文加载时不继承旧 valid 或操作控件', async () => {
    const nextRaw = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/agent/names')) return Promise.resolve(legacy(['agent-one', 'agent-two']))
      if (url.pathname.endsWith('/agent/agent-one')) return Promise.resolve(legacy('type: http\napi_key: first\n'))
      if (url.pathname.endsWith('/agent/agent-two')) return nextRaw.promise
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 agent-one' }))
    expect(await screen.findByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('type: http\napi_key: first\n')
    await waitFor(() => expect(screen.getByRole('button', { name: '保存 Agent 配置' })).toBeEnabled())

    fireEvent.click(screen.getByRole('button', { name: '进入工作区 agent-two' }))
    expect(await screen.findByText('正在加载 Agent 配置原文')).toBeInTheDocument()
    expectAgentWorkbenchControlsHidden()
    expect(screen.getByRole('button', { name: '关闭' })).toBeEnabled()
  })

  it('Agent 从 ready A 切到原文未返回的 B 时同步撤销 delete 与工作区操作', async () => {
    const nextRaw = deferred<Response>()
    let deleteRequests = 0
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/agent/names')) return Promise.resolve(legacy(['agent-a', 'agent-b']))
      if (url.pathname.endsWith('/agent/agent-a')) return Promise.resolve(legacy('type: http\napi_key: first\n'))
      if (url.pathname.endsWith('/agent/agent-b')) return nextRaw.promise
      if (init?.method === 'DELETE') deleteRequests += 1
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 agent-a' }))
    expect(await screen.findByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('type: http\napi_key: first\n')
    await waitFor(() => expect(screen.getByRole('button', { name: '删除 Agent 配置' })).toBeEnabled())

    act(() => {
      screen.getByRole('button', { name: '进入工作区 agent-b' }).click()
      expectAgentWorkbenchControlsHidden()
      expect(screen.queryByRole('dialog', { name: '确认删除 Agent 配置' })).not.toBeInTheDocument()
    })

    expect(screen.getByText('正在加载 Agent 配置原文')).toBeInTheDocument()
    expect(deleteRequests).toBe(0)
  })

  it('Agent 原文改为无效 YAML 后立即阻断保存确认和连通性请求', async () => {
    let saveRequests = 0
    let connectRequests = 0
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/agent/openai') && init?.method === 'POST') saveRequests += 1
      if (url.pathname.endsWith('/agent/connect') && init?.method === 'POST') connectRequests += 1
      if (url.pathname.endsWith('/agent/openai')) return Promise.resolve(legacy('type: http\napi_key: valid\n'))
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 openai' }))
    const editor = await screen.findByRole('textbox', { name: 'Agent 配置原文' }) as HTMLTextAreaElement
    await waitFor(() => expect(screen.getByRole('button', { name: '保存 Agent 配置' })).toBeEnabled())

    act(() => {
      changeTextAreaWithinCurrentAct(editor, 'type: [unterminated')
      screen.getByRole('button', { name: '保存 Agent 配置' }).click()
      screen.getByRole('button', { name: '测试连通性' }).click()
    })

    expect(screen.queryByRole('dialog', { name: '确认保存 Agent 配置' })).not.toBeInTheDocument()
    expect(saveRequests).toBe(0)
    expect(connectRequests).toBe(0)
  })

  it('Agent 编辑原文或 Prompt 后清除上一轮测试结果', async () => {
    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 openai' }))
    const editor = await screen.findByRole('textbox', { name: 'Agent 配置原文' })
    await waitFor(() => expect(screen.getByRole('button', { name: '测试连通性' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    expect(await screen.findByText('连通性测试通过。')).toBeInTheDocument()

    fireEvent.change(editor, { target: { value: 'type: http\napi_key: changed\n' } })
    expect(screen.queryByText('连通性测试通过。')).not.toBeInTheDocument()

    await waitFor(() => expect(screen.getByRole('button', { name: '测试连通性' })).toBeEnabled())
    fireEvent.change(screen.getByRole('textbox', { name: '测试 Prompt' }), { target: { value: 'first prompt' } })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Prompt 测试' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: 'Prompt 测试' }))
    expect(await screen.findByText('Prompt 测试完成：saved')).toBeInTheDocument()

    fireEvent.change(screen.getByRole('textbox', { name: '测试 Prompt' }), { target: { value: 'changed prompt' } })
    expect(screen.queryByText('Prompt 测试完成：saved')).not.toBeInTheDocument()
  })

  it('Agent 编辑模板字段后清除上一轮测试结果', async () => {
    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '新增 Agent 配置' }))
    expect(await screen.findByRole('combobox', { name: 'Provider 类型' })).toHaveValue('openai')
    fireEvent.change(screen.getByRole('textbox', { name: '配置名称' }), { target: { value: 'created-agent' } })
    fireEvent.change(screen.getByRole('textbox', { name: '服务地址' }), { target: { value: 'https://first.invalid' } })
    fireEvent.change(screen.getByLabelText(/访问密钥/), { target: { value: 'template-key' } })
    await waitFor(() => expect(screen.getByRole('button', { name: '测试连通性' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    expect(await screen.findByText('连通性测试通过。')).toBeInTheDocument()

    fireEvent.change(screen.getByRole('textbox', { name: '服务地址' }), { target: { value: 'https://changed.invalid' } })
    expect(screen.queryByText('连通性测试通过。')).not.toBeInTheDocument()
  })

  it('新增 Agent 配置在模板等待时不显示编辑或操作控件', async () => {
    const templates = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/agent/template')) return templates.promise
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '新增 Agent 配置' }))

    expect(await screen.findByText('正在加载 Agent 配置模板')).toBeInTheDocument()
    expectAgentWorkbenchControlsHidden()
    expect(screen.getByRole('button', { name: '关闭' })).toBeEnabled()
  })

  it('新增 Agent 配置在模板失败时只提供重试和关闭', async () => {
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/agent/template')) return Promise.resolve(httpFailure(500))
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '新增 Agent 配置' }))

    expect(await screen.findByText('Agent 配置模板加载失败')).toBeInTheDocument()
    expectAgentWorkbenchControlsHidden()
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled()
    expect(screen.getByRole('button', { name: '关闭' })).toBeEnabled()
  })

  it('新增 Agent 配置在空模板防御态时只提供重试和关闭', async () => {
    const templates = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/agent/template')) return templates.promise
      return knowledgeFetch(urlValue, init)
    }))

    const view = renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '新增 Agent 配置' }))
    await act(async () => { view.queryClient.setQueryData(['knowledge', 'agent-templates'], []) })

    expect(await screen.findByText('暂无可用 Agent 配置模板')).toBeInTheDocument()
    expectAgentWorkbenchControlsHidden()
    expect(screen.getByRole('button', { name: '重试' })).toBeEnabled()
    expect(screen.getByRole('button', { name: '关闭' })).toBeEnabled()
  })

  it('新增 Agent 配置仅在模板成功且默认模板已选中后保留 Provider 与模板入口', async () => {
    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '新增 Agent 配置' }))

    expect(await screen.findByRole('combobox', { name: 'Provider 类型' })).toHaveValue('openai')
    expect(screen.getByRole('textbox', { name: '配置名称' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '服务地址' })).toBeInTheDocument()
    expect(screen.getByLabelText(/访问密钥/)).toBeInTheDocument()
    expect(screen.getByText('高级设置')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '下载配置模板' })).toBeEnabled()
    expect(screen.getByRole('textbox', { name: 'Agent 配置原文' })).toBeInTheDocument()
  })

  it('关闭 Agent 工作区后不在 DOM 或 Query cache 保留原文、模板值、Prompt 或测试输出', async () => {
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/agent/names')) return Promise.resolve(legacy(['agent-one']))
      if (url.pathname.endsWith('/agent/agent-one')) return Promise.resolve(legacy('raw-config-sentinel'))
      if (url.pathname.endsWith('/agent/prompt_test') && init?.method === 'POST') return Promise.resolve(legacy(null, 0, 'test-output-sentinel'))
      return knowledgeFetch(urlValue, init)
    }))

    const view = renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 agent-one' }))
    expect(await screen.findByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('raw-config-sentinel')
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(screen.queryByRole('textbox', { name: 'Agent 配置原文' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '新增 Agent 配置' }))
    expect(await screen.findByRole('combobox', { name: 'Provider 类型' })).toHaveValue('openai')
    fireEvent.change(screen.getByRole('textbox', { name: '配置名称' }), { target: { value: 'created-agent' } })
    fireEvent.change(screen.getByRole('textbox', { name: '服务地址' }), { target: { value: 'https://template-value-sentinel.invalid' } })
    fireEvent.change(screen.getByLabelText(/访问密钥/), { target: { value: 'template-value-sentinel' } })
    fireEvent.change(screen.getByRole('textbox', { name: '测试 Prompt' }), { target: { value: 'prompt-sentinel' } })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Prompt 测试' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: 'Prompt 测试' }))
    expect(await screen.findByText('Prompt 测试完成：test-output-sentinel')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(screen.queryByRole('textbox', { name: 'Agent 配置原文' })).not.toBeInTheDocument()
    const cachedData = JSON.stringify(view.queryClient.getQueryCache().getAll().map((query) => query.state.data))
    for (const sentinel of ['raw-config-sentinel', 'template-value-sentinel', 'prompt-sentinel', 'test-output-sentinel']) {
      expect(document.body).not.toHaveTextContent(sentinel)
      expect(cachedData).not.toContain(sentinel)
    }
  })

  it.each([
    ['保存', '保存 Agent 配置', 'Agent 配置保存失败，请显式重试。', '/api/v1/knowledge/agent/openai', true],
    ['连通性', '测试连通性', 'Agent 测试失败，请显式重试。', '/api/v1/knowledge/agent/connect', false],
    ['Prompt', 'Prompt 测试', 'Agent 测试失败，请显式重试。', '/api/v1/knowledge/agent/prompt_test', false],
  ] as const)('Agent %s失败会清空局部敏感状态并使旧 valid 失效', async (_, actionLabel, errorMessage, failedPath, needsConfirmation) => {
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (init?.method === 'POST' && url.pathname === failedPath) return Promise.resolve(legacy(null, 1, 'server-secret-sentinel'))
      if (url.pathname.endsWith('/agent/openai')) return Promise.resolve(legacy('type: http\napi_key: action-secret-sentinel\n'))
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 openai' }))
    expect(await screen.findByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('type: http\napi_key: action-secret-sentinel\n')
    await waitFor(() => expect(screen.getByRole('button', { name: '测试连通性' })).toBeEnabled())
    fireEvent.change(screen.getByRole('textbox', { name: '测试 Prompt' }), { target: { value: 'prompt-secret-sentinel' } })
    await waitFor(() => expect(screen.getByRole('button', { name: actionLabel })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: actionLabel }))
    if (needsConfirmation) fireEvent.click(screen.getByRole('button', { name: '确认保存' }))

    expect(await screen.findByText(errorMessage)).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('')
    expect(screen.getByRole('textbox', { name: '测试 Prompt' })).toHaveValue('')
    await waitFor(() => {
      expect(screen.getByRole('button', { name: '保存 Agent 配置' })).toBeDisabled()
      expect(screen.getByRole('button', { name: '测试连通性' })).toBeDisabled()
      expect(screen.getByRole('button', { name: 'Prompt 测试' })).toBeDisabled()
    })
    expect(document.body).not.toHaveTextContent(/action-secret-sentinel|prompt-secret-sentinel|server-secret-sentinel/)
  })

  it('Agent管理员可执行连通性与Prompt测试，审计员只读', async () => {
    const auditor = renderRoute('auditor', '/knowledge/agents')
    expect(await screen.findByText('openai')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '测试连通性' })).not.toBeInTheDocument()
    auditor.unmount()

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 openai' }))
    expect(await screen.findByRole('button', { name: '测试连通性' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Prompt 测试' })).toBeInTheDocument()
  })

  it('Agent 管理员在具名本地工作区中区分原文、配置操作和受控验证', async () => {
    renderRoute('admin', '/knowledge/agents')
    expect(await screen.findByRole('button', { name: '进入工作区 openai' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '进入工作区 openai' }))

    const workspace = await screen.findByRole('region', { name: 'Agent 配置工作区' })
    expect(within(workspace).getByRole('heading', { name: 'openai' })).toBeInTheDocument()
    expect(within(workspace).getByText('原文仅在当前本地工作区显示')).toBeInTheDocument()
    expect(within(workspace).getByText('单次测试不代表持续健康')).toBeInTheDocument()
    expect(within(workspace).getByRole('region', { name: '配置来源与原文' })).toBeInTheDocument()
    expect(within(workspace).getByRole('region', { name: '配置操作' })).toBeInTheDocument()
    expect(within(workspace).getByRole('region', { name: '受控验证' })).toBeInTheDocument()
  })

  it.each(['user', 'auditor'] as const)('Agent %s 在本地工作区内只读原文且没有治理操作区', async (role) => {
    renderRoute(role, '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 openai' }))

    const workspace = await screen.findByRole('region', { name: 'Agent 配置工作区' })
    expect(within(workspace).getByRole('textbox', { name: 'Agent 配置原文' })).toBeDisabled()
    expect(within(workspace).queryByRole('region', { name: '配置操作' })).not.toBeInTheDocument()
    expect(within(workspace).queryByRole('region', { name: '受控验证' })).not.toBeInTheDocument()
  })

  it('Agent 测试反馈仅显示在本地工作区且关闭后清除', async () => {
    let connectionAttempts = 0
    vi.stubGlobal('fetch', vi.fn((urlValue: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(urlValue))
      if (url.pathname.endsWith('/agent/connect') && init?.method === 'POST') {
        connectionAttempts += 1
        return Promise.resolve(connectionAttempts === 1 ? legacy(null) : legacy(null, 1, 'provider://private/raw-error-sentinel'))
      }
      return knowledgeFetch(urlValue, init)
    }))

    renderRoute('admin', '/knowledge/agents')
    fireEvent.click(await screen.findByRole('button', { name: '进入工作区 openai' }))
    const workspace = await screen.findByRole('region', { name: 'Agent 配置工作区' })
    await waitFor(() => expect(within(workspace).getByRole('button', { name: '测试连通性' })).toBeEnabled())

    fireEvent.click(within(workspace).getByRole('button', { name: '测试连通性' }))
    expect(await within(workspace).findByText('连通性测试通过。')).toBeInTheDocument()

    await waitFor(() => expect(within(workspace).getByRole('button', { name: '测试连通性' })).toBeEnabled())
    fireEvent.click(within(workspace).getByRole('button', { name: '测试连通性' }))
    expect(await within(workspace).findByText('Agent 测试失败，请显式重试。')).toBeInTheDocument()
    expect(document.body).not.toHaveTextContent('provider://private/raw-error-sentinel')

    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    expect(screen.queryByRole('region', { name: 'Agent 配置工作区' })).not.toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: 'Agent 配置原文' })).not.toBeInTheDocument()
    expect(screen.queryByText('连通性测试通过。')).not.toBeInTheDocument()
    expect(screen.queryByText('Agent 测试失败，请显式重试。')).not.toBeInTheDocument()
  })
})
