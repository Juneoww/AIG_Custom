/** 功能：验证专用 MCP 表单、扫描状态与连接秘密生命周期；通过真实路由和网络边界验证用户操作。 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AppProviders, createAppQueryClient } from '../../app/providers/AppProviders'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { MCPScanCreatePage } from './MCPScanCreatePage'
import { MCPScanDetailPage } from './MCPScanDetailPage'
import { MCPScanListPage } from './MCPScanListPage'
import { MCPConnectionFormPage } from '../mcp-connections/MCPConnectionFormPage'
import { MCPConnectionListPage } from '../mcp-connections/MCPConnectionListPage'

function Location() { const location = useLocation(); return <output aria-label="当前页面">{location.pathname}</output> }
function setup(path = '/tasks/mcp/new', role: 'user' | 'auditor' = 'user') {
  const client = createAppQueryClient()
  const view = render(<ThemeProvider initialMode="light"><AppProviders queryClient={client} sessionInitialState={{ status: 'authenticated', subject: { id: 'u-1', username: 'operator', role, must_change_password: false } }}><MemoryRouter initialEntries={[path]}><Routes><Route path="/tasks/mcp/new" element={<MCPScanCreatePage />} /><Route path="/tasks/mcp/scans" element={<MCPScanListPage />} /><Route path="/tasks/mcp/:taskId" element={<MCPScanDetailPage />} /><Route path="/credentials/mcp-connections" element={<MCPConnectionListPage />} /><Route path="/credentials/mcp-connections/new" element={<MCPConnectionFormPage />} /><Route path="/credentials/mcp-connections/:connectionConfigId" element={<MCPConnectionFormPage />} /></Routes><Location /></MemoryRouter></AppProviders></ThemeProvider>)
  return { ...view, client }
}
const connection = { id: 'conn-1', name: '内网测试', description: '', scope: 'private', current_version: 1, resource_revision: '2', enabled: false, transport: 'auto', probe_status: 'not_tested', authentication_kind: 'bearer', created_at: '2026-09-02T00:00:00Z', updated_at: '2026-09-02T00:00:00Z', server_url: 'https://example.test/mcp', authentication_configured: true, headers: [] }
function mockAPI() {
  const fetcher = vi.fn(async (url: string, init?: RequestInit) => {
    if (url.includes('/platform/models')) return Response.json({ items: [], total: 0, page: 1, page_size: 100 })
    if (url.includes('/mcp-connection-options')) return Response.json({ items: [{ connection_id: 'conn-1', connection_version: 3, name: '已测连接', scope: 'private', transport: 'http', authentication_kind: 'bearer' }] })
    if (url.includes('/mcp-connection-configs') && init?.method === 'GET') return Response.json(connection)
    if (url.includes('/mcp-connection-configs')) return Response.json({ id: 'conn-1', current_version: 1, resource_revision: '2', status: 'created' }, { status: 201 })
    if (url.endsWith('/mcp-scans') && init?.method === 'POST') return Response.json({ task_id: 'scan-1', status: 'pending' }, { status: 202 })
    if (url.includes('/mcp-scans?')) return Response.json({ items: [], total: 0, page: 1, page_size: 20 })
    if (url.includes('/mcp-scans/')) return Response.json({ id: 'scan-1', owner: 'u-1', source_kind: 'service', status: 'succeeded', created_at: '2026-09-02T00:00:00Z', updated_at: '2026-09-02T00:00:00Z', input_summary: { language: 'zh_CN', source_kind: 'service', thread: 4 }, report_id: 'report-1' })
    return new Response(null, { status: 404 })
  })
  vi.stubGlobal('fetch', fetcher)
  return fetcher
}
function unknownResponse(fault: 'network' | 'body' | 'json' | 'dto' | 'gateway'): Response {
	if (fault === 'gateway') return Response.json({ error: 'private diagnostic' }, { status: 502 })
  if (fault === 'network') throw new TypeError('private diagnostic')
  if (fault === 'body') return new Response(new ReadableStream({ start(controller) { controller.error(new Error('private diagnostic')) } }), { status: 202, headers: { 'Content-Type': 'application/json' } })
  if (fault === 'json') return new Response('{private diagnostic', { status: 201, headers: { 'Content-Type': 'application/json' } })
  return Response.json({ status: 'private diagnostic' })
}
beforeEach(() => vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }))
afterEach(() => { cleanup(); vi.unstubAllGlobals() })
describe('dedicated MCP pages', () => {
  it('allows no model and clearly identifies basic-check coverage', async () => {
    const fetcher = mockAPI(); setup()
    expect(await screen.findByRole('option', { name: '不使用模型' })).toBeInTheDocument()
    expect(screen.getByText('模型可选：不选择时仅执行基础检查；选择后增加模型辅助分析，不会自动使用默认模型。')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/Git 仓库地址/), { target: { value: 'https://example.test/repo' } })
    fireEvent.click(screen.getByRole('button', { name: '创建 MCP 扫描' }))
    await waitFor(() => expect(screen.getByLabelText('当前页面')).toHaveTextContent('/tasks/mcp/scan-1'))
    const post = fetcher.mock.calls.find(([url, init]) => url.endsWith('/mcp-scans') && init?.method === 'POST')
    expect(JSON.parse(String(post?.[1]?.body))).not.toHaveProperty('model_id')
    expect(await screen.findByText('分析方式：基础检查（未使用模型）')).toBeInTheDocument()
  })
  it('creates a saved version service scan only after authorization and clears repository input on switching', async () => {
    const fetcher = mockAPI(); setup()
    fireEvent.change(screen.getByLabelText(/Git 仓库地址/), { target: { value: 'https://example.test/repo' } })
    fireEvent.click(screen.getByRole('radio', { name: 'MCP 服务' }))
    await screen.findByRole('option', { name: '已测连接 · 版本 3' })
    fireEvent.change(screen.getByLabelText(/MCP 连接配置/), { target: { value: 'conn-1:3' } })
    expect(screen.getByRole('button', { name: '创建 MCP 扫描' })).toBeDisabled()
    expect(screen.queryByLabelText(/Git 仓库地址/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByLabelText('我已获得此 MCP 服务的安全测试授权'))
    fireEvent.click(screen.getByRole('button', { name: '创建 MCP 扫描' }))
    await waitFor(() => expect(screen.getByLabelText('当前页面')).toHaveTextContent('/tasks/mcp/scan-1'))
    const post = fetcher.mock.calls.find(([url, init]) => url.endsWith('/mcp-scans') && init?.method === 'POST')
    expect(JSON.parse(String(post?.[1]?.body))).toEqual({ source_kind: 'service', thread: 4, connection_config_id: 'conn-1', connection_config_version: 3, authorization_confirmed: true })
    expect(fetcher.mock.calls.some(([url]) => url.includes('/platform/tasks'))).toBe(false)
    expect(await screen.findByRole('link', { name: '查看安全报告' })).toHaveAttribute('href', '/reports/report-1')
  })
  it('shows an empty dedicated ledger and blocks auditors from creating scans', async () => {
    const fetcher = mockAPI(); const view = setup('/tasks/mcp/scans', 'auditor')
    expect(await screen.findByText('暂无 MCP 扫描记录')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '新建 MCP 扫描' })).not.toBeInTheDocument()
    expect(fetcher.mock.calls.every(([url]) => !url.includes('/platform/tasks'))).toBe(true)
    view.unmount(); setup('/tasks/mcp/new', 'auditor')
    expect(screen.getByText('无权创建 MCP 扫描')).toBeInTheDocument()
  })
  it('clears write-only secrets after successful create and never puts them in Query cache', async () => {
    mockAPI(); const { client } = setup('/credentials/mcp-connections/new')
    fireEvent.change(screen.getByLabelText(/配置名称/), { target: { value: '内网测试' } })
    fireEvent.change(screen.getByLabelText(/服务地址/), { target: { value: 'https://example.test/mcp' } })
    fireEvent.change(screen.getByLabelText('认证方式'), { target: { value: 'bearer' } })
    fireEvent.change(screen.getByLabelText('认证密钥'), { target: { value: 'SECRET-NEVER-CACHE' } })
    fireEvent.click(screen.getByRole('button', { name: '保存连接配置' }))
    await waitFor(() => expect(screen.getByLabelText('当前页面')).toHaveTextContent('/credentials/mcp-connections/conn-1'))
    expect(screen.queryByDisplayValue('SECRET-NEVER-CACHE')).not.toBeInTheDocument()
    expect(JSON.stringify(client.getQueryCache().getAll())).not.toContain('SECRET-NEVER-CACHE')
    expect(client.getMutationCache().getAll()).toHaveLength(0)
  })
  it('does not fetch managed connection details for auditors', () => {
    const fetcher = mockAPI(); setup('/credentials/mcp-connections/conn-1', 'auditor')
    expect(screen.getByText('无权管理 MCP 连接配置')).toBeInTheDocument()
    expect(fetcher).not.toHaveBeenCalled()
  })

  it.each(['network', 'body', 'json', 'dto', 'gateway'] as const)('does not auto-repeat a %s-unknown create and explicitly retries the original payload and key', async (fault) => {
    const fetcher = mockAPI(); const base = fetcher.getMockImplementation()!
    let creates = 0
    fetcher.mockImplementation(async (url, init) => {
      if (url.endsWith('/mcp-scans') && init?.method === 'POST' && creates++ === 0) return unknownResponse(fault)
      return base(url, init)
    })
    setup(); fireEvent.change(screen.getByLabelText(/Git 仓库地址/), { target: { value: 'https://example.test/repo' } })
    fireEvent.click(screen.getByRole('button', { name: '创建 MCP 扫描' }))
    const retry = await screen.findByRole('button', { name: '重试同一提交' })
    expect(creates).toBe(1); expect(screen.queryByText('private diagnostic')).not.toBeInTheDocument()
    fireEvent.click(retry)
    await waitFor(() => expect(screen.getByLabelText('当前页面')).toHaveTextContent('/tasks/mcp/scan-1'))
    const calls = fetcher.mock.calls.filter(([url, init]) => url.endsWith('/mcp-scans') && init?.method === 'POST')
    expect(calls).toHaveLength(2)
    expect(calls[0][1]?.body).toBe(calls[1][1]?.body)
    expect(new Headers(calls[0][1]?.headers).get('Idempotency-Key')).toBe(new Headers(calls[1][1]?.headers).get('Idempotency-Key'))
  })

  it.each(['body', 'json', 'dto'] as const)('retains write-only secrets and the same connection operation after a %s-unknown response until explicit retry succeeds', async (fault) => {
    const fetcher = mockAPI(); const base = fetcher.getMockImplementation()!
    let writes = 0
    fetcher.mockImplementation(async (url, init) => {
      if (url.endsWith('/mcp-connection-configs') && init?.method === 'POST' && writes++ === 0) return unknownResponse(fault)
      return base(url, init)
    })
    const { client } = setup('/credentials/mcp-connections/new')
    fireEvent.change(screen.getByLabelText(/配置名称/), { target: { value: '内网测试' } })
    fireEvent.change(screen.getByLabelText(/服务地址/), { target: { value: 'https://example.test/mcp' } })
    fireEvent.change(screen.getByLabelText('认证方式'), { target: { value: 'bearer' } })
    fireEvent.change(screen.getByLabelText('认证密钥'), { target: { value: 'SECRET-RETRY-ONLY' } })
    fireEvent.click(screen.getByRole('button', { name: '保存连接配置' }))
    const retry = await screen.findByRole('button', { name: '重试同一操作' })
    expect(writes).toBe(1)
    expect(screen.getByRole('button', { name: '保存连接配置' })).toBeDisabled()
    expect(screen.getByLabelText('认证密钥')).toHaveValue('SECRET-RETRY-ONLY')
    expect(screen.queryByText('private diagnostic')).not.toBeInTheDocument()
    expect(client.getMutationCache().getAll()).toHaveLength(0)
    fireEvent.click(retry)
    await waitFor(() => expect(screen.getByLabelText('当前页面')).toHaveTextContent('/credentials/mcp-connections/conn-1'))
    const calls = fetcher.mock.calls.filter(([url, init]) => url.endsWith('/mcp-connection-configs') && init?.method === 'POST')
    expect(calls).toHaveLength(2)
    expect(calls[0][1]?.body).toBe(calls[1][1]?.body)
    expect(new Headers(calls[0][1]?.headers).get('Idempotency-Key')).toBe(new Headers(calls[1][1]?.headers).get('Idempotency-Key'))
    expect(screen.queryByDisplayValue('SECRET-RETRY-ONLY')).not.toBeInTheDocument()
    expect(JSON.stringify(client.getQueryCache().getAll())).not.toContain('SECRET-RETRY-ONLY')
  })

  it('revokes authorization after a 409 and only displays the fixed safe conflict message', async () => {
    const fetcher = mockAPI(); const base = fetcher.getMockImplementation()!
    fetcher.mockImplementation(async (url, init) => url.endsWith('/mcp-scans') && init?.method === 'POST' ? Response.json({ code: 'MCP_CONNECTION_VERSION_CONFLICT', error: 'SECRET' }, { status: 409 }) : base(url, init))
    setup(); fireEvent.click(screen.getByRole('radio', { name: 'MCP 服务' }))
    await screen.findByRole('option', { name: '已测连接 · 版本 3' })
    fireEvent.change(screen.getByLabelText(/MCP 连接配置/), { target: { value: 'conn-1:3' } })
    fireEvent.click(screen.getByLabelText('我已获得此 MCP 服务的安全测试授权'))
    fireEvent.click(screen.getByRole('button', { name: '创建 MCP 扫描' }))
    expect(await screen.findByText('配置版本或提交状态已变化，请刷新配置并检查提交后重试。')).toBeInTheDocument()
    expect(screen.queryByText('SECRET')).not.toBeInTheDocument()
    expect(screen.getByLabelText('我已获得此 MCP 服务的安全测试授权')).not.toBeChecked()
    expect(screen.getByRole('button', { name: '创建 MCP 扫描' })).toBeDisabled()
  })

  it('only updates the connection name when existing secrets are retained', async () => {
    const fetcher = mockAPI(); const { client } = setup('/credentials/mcp-connections/conn-1')
    fireEvent.click(await screen.findByRole('button', { name: '编辑配置' }))
    expect(screen.getByLabelText('认证密钥')).toHaveValue('')
    fireEvent.change(screen.getByLabelText(/配置名称/), { target: { value: '更名连接' } })
    fireEvent.click(screen.getByRole('button', { name: '保存连接配置' }))
    await waitFor(() => expect(fetcher.mock.calls.some(([, init]) => init?.method === 'PATCH')).toBe(true))
    const update = fetcher.mock.calls.find(([, init]) => init?.method === 'PATCH')!
    expect(JSON.parse(String(update[1]?.body))).toEqual({ name: '更名连接', description: '' })
    expect(new Headers(update[1]?.headers).get('If-Match')).toBe('"2"')
    expect(client.getMutationCache().getAll()).toHaveLength(0)
  })

  it.each(['network', 'body', 'json', 'dto', 'gateway'] as const)('cancels only after confirmation and retries a %s-unknown cancel with the original key', async (fault) => {
    const fetcher = mockAPI(); const base = fetcher.getMockImplementation()!
    let cancelled = false; let cancels = 0
    fetcher.mockImplementation(async (url, init) => {
      if (url.endsWith('/cancel')) { if (cancels++ === 0) return unknownResponse(fault); cancelled = true; return Response.json({ task_id: 'scan-1', status: 'cancelled' }) }
      if (url.endsWith('/mcp-scans/scan-1')) return Response.json({ id: 'scan-1', owner: 'u-1', source_kind: 'service', status: cancelled ? 'cancelled' : 'running', created_at: '2026-09-02T00:00:00Z', updated_at: '2026-09-02T00:00:00Z', input_summary: { language: 'zh_CN', source_kind: 'service' } })
      return base(url, init)
    })
    setup('/tasks/mcp/scan-1'); fireEvent.click(await screen.findByRole('button', { name: '取消扫描' }))
    expect(cancels).toBe(0)
    fireEvent.click(screen.getByRole('button', { name: '确认取消' }))
    const retry = await screen.findByRole('button', { name: '重试同一取消请求' })
    expect(cancels).toBe(1); fireEvent.click(retry)
    expect(await screen.findByText('已取消')).toBeInTheDocument()
    const calls = fetcher.mock.calls.filter(([url]) => url.endsWith('/cancel'))
    expect(new Headers(calls[0][1]?.headers).get('Idempotency-Key')).toBe(new Headers(calls[1][1]?.headers).get('Idempotency-Key'))
  })

  it('clears configured custom headers when switching to no authentication and explicitly patches an empty header list', async () => {
    const fetcher = mockAPI(); const base = fetcher.getMockImplementation()!
    fetcher.mockImplementation(async (url, init) => url.includes('/mcp-connection-configs') && init?.method === 'GET' ? Response.json({ ...connection, authentication_kind: 'custom_headers', headers: [{ name: 'X-Key', configured: true }] }) : base(url, init))
    setup('/credentials/mcp-connections/conn-1')
    fireEvent.click(await screen.findByRole('button', { name: '编辑配置' }))
    fireEvent.change(screen.getByLabelText('请求头 1 值'), { target: { value: 'TEMP-HEADER-SECRET' } })
    fireEvent.change(screen.getByLabelText('认证方式'), { target: { value: 'none' } })
    expect(screen.queryByLabelText('请求头 1 名称')).not.toBeInTheDocument()
    expect(screen.queryByDisplayValue('TEMP-HEADER-SECRET')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /添加请求头/ })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '保存连接配置' }))
    await waitFor(() => expect(fetcher.mock.calls.some(([, init]) => init?.method === 'PATCH')).toBe(true))
    const patch = fetcher.mock.calls.find(([, init]) => init?.method === 'PATCH')!
    expect(JSON.parse(String(patch[1]?.body))).toEqual({ name: connection.name, description: '', authentication: { kind: 'none' }, headers: [] })
  })

  it('requires at least one complete custom header before saving and keeps no header controls for no authentication', async () => {
    const fetcher = mockAPI(); setup('/credentials/mcp-connections/new')
    fireEvent.change(screen.getByLabelText(/配置名称/), { target: { value: '请求头配置' } })
    fireEvent.change(screen.getByLabelText(/服务地址/), { target: { value: 'https://example.test/mcp' } })
    expect(screen.queryByRole('button', { name: /添加请求头/ })).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('认证方式'), { target: { value: 'custom_headers' } })
    const save = screen.getByRole('button', { name: '保存连接配置' })
    expect(save).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: /添加请求头/ }))
    fireEvent.change(screen.getByLabelText('请求头 1 名称'), { target: { value: 'X-Key' } })
    expect(save).toBeDisabled()
    expect(fetcher.mock.calls.some(([, init]) => init?.method === 'POST')).toBe(false)
    fireEvent.change(screen.getByLabelText('请求头 1 值'), { target: { value: 'NEW-HEADER-SECRET' } })
    expect(save).toBeEnabled()
    fireEvent.click(save)
    await waitFor(() => expect(screen.getByLabelText('当前页面')).toHaveTextContent('/credentials/mcp-connections/conn-1'))
  })

  it('requires a replacement secret when an API key header is renamed', async () => {
    const fetcher = mockAPI(); const base = fetcher.getMockImplementation()!
    fetcher.mockImplementation(async (url, init) => url.includes('/mcp-connection-configs') && init?.method === 'GET' ? Response.json({ ...connection, authentication_kind: 'api_key_header', authentication_header_name: 'X-Key' }) : base(url, init))
    setup('/credentials/mcp-connections/conn-1')
    fireEvent.click(await screen.findByRole('button', { name: '编辑配置' }))
    fireEvent.change(screen.getByLabelText(/认证请求头名称/), { target: { value: 'X-New-Key' } })
    expect(screen.getByRole('button', { name: '保存连接配置' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText('认证密钥'), { target: { value: 'REPLACEMENT-KEY' } })
    fireEvent.click(screen.getByRole('button', { name: '保存连接配置' }))
    await waitFor(() => expect(fetcher.mock.calls.some(([, init]) => init?.method === 'PATCH')).toBe(true))
    const patch = fetcher.mock.calls.find(([, init]) => init?.method === 'PATCH')!
    expect(JSON.parse(String(patch[1]?.body)).authentication).toEqual({ kind: 'api_key_header', header_name: 'X-New-Key', secret: 'REPLACEMENT-KEY' })
  })

  it('hides cached scan facts after a forbidden refresh', async () => {
    const fetcher = mockAPI(); const base = fetcher.getMockImplementation()!
    setup('/tasks/mcp/scan-1'); await screen.findByRole('link', { name: '查看安全报告' })
    fetcher.mockImplementation(async (url, init) => url.endsWith('/mcp-scans/scan-1') ? new Response(null, { status: 403 }) : base(url, init))
    fireEvent.click(screen.getByRole('button', { name: '刷新状态' }))
    expect(await screen.findByText('无法读取 MCP 扫描详情')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '查看安全报告' })).not.toBeInTheDocument()
  })

  it('restores saved form values and clears replacement secrets when cancelling editing', async () => {
    mockAPI(); setup('/credentials/mcp-connections/conn-1')
    fireEvent.click(await screen.findByRole('button', { name: '编辑配置' }))
    fireEvent.change(screen.getByLabelText(/配置名称/), { target: { value: '未保存的名称' } })
    fireEvent.change(screen.getByLabelText('认证密钥'), { target: { value: 'TEMP-SECRET' } })
    fireEvent.click(screen.getByRole('button', { name: '取消编辑' }))
    expect(screen.getByLabelText(/配置名称/)).toHaveValue('内网测试')
    expect(screen.getByLabelText('认证密钥')).toHaveValue('')
  })

  it('cleans unsubmitted attachments and clears repository input when switching to service', async () => {
    const fetcher = mockAPI(); const base = fetcher.getMockImplementation()!
    fetcher.mockImplementation(async (url, init) => {
      if (url.endsWith('/mcp-scan-attachments') && init?.method === 'POST') return Response.json({ id: 'a-1', state: 'ready', size: 4, max_file_bytes: 52428800, max_chunk_bytes: 5242880 })
      if (url.endsWith('/mcp-scan-attachments/a-1')) return new Response(null, { status: 204 })
      return base(url, init)
    })
    setup(); fireEvent.change(screen.getByLabelText('代码来源'), { target: { value: 'upload' } })
    fireEvent.change(screen.getByLabelText('代码附件'), { target: { files: [new File(['code'], 'repo.zip')] } })
    fireEvent.click(screen.getByRole('button', { name: '上传代码附件' }))
    expect(await screen.findByText('repo.zip · 已上传')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('radio', { name: 'MCP 服务' }))
    await waitFor(() => expect(fetcher.mock.calls.some(([url, init]) => url.endsWith('/mcp-scan-attachments/a-1') && init?.method === 'DELETE')).toBe(true))
    fireEvent.click(screen.getByRole('radio', { name: '代码 / 仓库' }))
    expect(screen.queryByText('repo.zip · 已上传')).not.toBeInTheDocument()
    expect(screen.getByLabelText('代码附件')).toHaveValue('')
  })

  it('requires successful testing and a separate explicit enable mutation with the current revision', async () => {
    const fetcher = mockAPI(); const base = fetcher.getMockImplementation()!
    let current = { ...connection }
    fetcher.mockImplementation(async (url, init) => {
      if (url.includes('/mcp-connection-configs') && init?.method === 'GET') return Response.json(current)
      if (url.endsWith('/test')) { current = { ...current, probe_status: 'passed', resource_revision: '4' }; return Response.json({ id: current.id, current_version: 1, resource_revision: '4', status: 'passed' }) }
      if (url.endsWith('/mcp-connection-configs/conn-1') && init?.method === 'PATCH') { current = { ...current, enabled: true, resource_revision: '5' }; return Response.json({ id: current.id, current_version: 1, resource_revision: '5', status: 'enabled' }) }
      return base(url, init)
    })
    setup('/credentials/mcp-connections/conn-1')
    expect(await screen.findByRole('button', { name: '启用连接' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '测试连接' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '启用连接' })).toBeEnabled())
    expect(fetcher.mock.calls.filter(([, init]) => init?.method === 'PATCH')).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: '启用连接' }))
    expect(await screen.findByRole('button', { name: '停用连接' })).toBeInTheDocument()
    const enable = fetcher.mock.calls.find(([, init]) => init?.method === 'PATCH')!
    expect(JSON.parse(String(enable[1]?.body))).toEqual({ enabled: true })
    expect(new Headers(enable[1]?.headers).get('If-Match')).toBe('"4"')
  })

  it('shows unavailable authentication in the safe auditor list without a detail link', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ items: [{ ...connection, authentication_kind: 'unknown', server_url: 'SECRET', secret: 'SECRET' }] })))
    const { client } = setup('/credentials/mcp-connections', 'auditor')
    expect(await screen.findByText('认证状态不可用')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '内网测试' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '新建 MCP 连接' })).not.toBeInTheDocument()
    expect(JSON.stringify(client.getQueryData(['mcp-connections']))).not.toContain('SECRET')
  })
})
