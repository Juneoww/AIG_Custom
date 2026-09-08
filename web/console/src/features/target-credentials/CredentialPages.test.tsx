/** 功能：验证凭据编辑和任务选择行为；实现：真实表单与查询缓存配合模拟 API；输入：安全摘要和临时密钥；输出：不回显、不缓存秘密及版本保护断言。 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { CredentialEditor } from './TargetCredentialFormPage'
import { TargetCredentialSelector } from './TargetCredentialSelector'
import type { TargetCredential } from './api'

const detail: TargetCredential = { id: 'credential-1', name: '推理服务', origin: 'https://inference.example.com', allow_insecure_http: false, auth_type: 'bearer', header_name: '', revision: 1, disabled: false, created_at: '2026-09-07T00:00:00Z', updated_at: '2026-09-07T00:00:00Z' }
function view(node: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(<ThemeProvider><QueryClientProvider client={client}><MemoryRouter>{node}</MemoryRouter></QueryClientProvider></ThemeProvider>)
  return client
}
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })
beforeEach(() => { vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })

describe('基础设施凭据页面', () => {
  it('不显示 HTTP 开关，仍拒绝非 HTTP/HTTPS 目标', () => {
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
    view(<CredentialEditor reload={() => undefined} />)
    expect(screen.queryByRole('checkbox', { name: '允许明文 HTTP' })).not.toBeInTheDocument()
    expect(screen.queryByText(/HTTP 会以明文发送凭据/)).not.toBeInTheDocument()
    fireEvent.change(screen.getByLabelText(/^凭据名称/), { target: { value: detail.name } })
    fireEvent.change(screen.getByLabelText(/^允许访问的目标地址/), { target: { value: 'ftp://inference.example.com' } })
    fireEvent.change(screen.getByLabelText(/^认证密钥/), { target: { value: 'temporary-secret' } })
    fireEvent.submit(screen.getByRole('button', { name: '保存凭据' }).closest('form')!)
    expect(screen.getByText('请填写有效的 HTTP 或 HTTPS 目标地址，不包含路径、查询参数或账号信息。')).toBeInTheDocument()
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('新建 HTTP 凭据直接保存，不发送许可字段或缓存临时密钥', async () => {
    const http = { ...detail, origin: 'http://inference.example.com', allow_insecure_http: true }
    const fetcher = vi.fn().mockResolvedValue(response(http, 201)); vi.stubGlobal('fetch', fetcher)
    const client = view(<CredentialEditor reload={() => undefined} />)
    fireEvent.change(screen.getByLabelText(/^凭据名称/), { target: { value: detail.name } })
    fireEvent.change(screen.getByLabelText(/^允许访问的目标地址/), { target: { value: http.origin } })
    fireEvent.change(screen.getByLabelText(/^认证密钥/), { target: { value: 'temporary-http-secret' } })
    fireEvent.click(screen.getByRole('button', { name: '保存凭据' }))
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual(expect.objectContaining({ origin: http.origin, secret: 'temporary-http-secret' }))
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).not.toHaveProperty('allow_insecure_http')
    await waitFor(() => expect(screen.getByLabelText(/^认证密钥/)).toHaveValue(''))
    expect(JSON.stringify(client.getQueryCache().getAll())).not.toContain('temporary-http-secret')
  })
  it('编辑 HTTP 凭据无需额外开关，直接保留空密钥', async () => {
    const http = { ...detail, origin: 'http://inference.example.com', allow_insecure_http: true }
    const fetcher = vi.fn().mockResolvedValue(response({ ...http, revision: 2 })); vi.stubGlobal('fetch', fetcher)
    view(<CredentialEditor detail={http} reload={() => undefined} />)
    expect(screen.queryByRole('checkbox', { name: '允许明文 HTTP' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('认证密钥')).not.toBeRequired()
    fireEvent.change(screen.getByLabelText(/^允许访问的目标地址/), { target: { value: `${http.origin}:80` } })
    expect(screen.getByLabelText('认证密钥')).not.toBeRequired()
    fireEvent.click(screen.getByRole('button', { name: '保存凭据' }))
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual(expect.objectContaining({ secret: '' }))
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).not.toHaveProperty('allow_insecure_http')
    expect(new Headers(fetcher.mock.calls[0][1].headers).get('If-Match')).toBe('"1"')
    await waitFor(() => expect(screen.getByRole('button', { name: '保存凭据' })).toBeEnabled())
  })
  it.each(['http', 'https'])('从 %s 切换协议不能复用原密钥', (scheme) => {
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
    view(<CredentialEditor detail={{ ...detail, origin: `${scheme}://inference.example.com`, allow_insecure_http: scheme === 'http' }} reload={() => undefined} />)
    fireEvent.change(screen.getByLabelText(/^允许访问的目标地址/), { target: { value: `${scheme === 'http' ? 'https' : 'http'}://inference.example.com` } })
    expect(screen.getByLabelText(/^认证密钥/)).toBeRequired()
    fireEvent.submit(screen.getByRole('button', { name: '保存凭据' }).closest('form')!)
    expect(screen.getByText('请填写完整认证信息；更换目标或认证方式需要新密钥。')).toBeInTheDocument()
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('已选 HTTP 凭据提示实际协议并保留安全元数据', async () => {
    const http = { ...detail, origin: 'http://inference.example.com', allow_insecure_http: true }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ items: [http] })))
    const change = vi.fn()
    view(<TargetCredentialSelector value={http} onChange={change} />)
    await screen.findByRole('option', { name: /推理服务 · http:\/\// })
    expect(screen.getByText(/请手动填写此地址下的 HTTP URL/)).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox'), { target: { value: http.id } })
    expect(change).toHaveBeenCalledWith(http)
  })
  it('编辑时不回显密钥，留空保留并发送版本条件', async () => {
    const fetcher = vi.fn().mockResolvedValue(response({ ...detail, revision: 2, secret: 'unexpected-response-secret' }))
    vi.stubGlobal('fetch', fetcher)
    const client = view(<CredentialEditor detail={detail} reload={() => undefined} />)
    expect(screen.getByLabelText('认证密钥')).toHaveValue('')
    fireEvent.click(screen.getByRole('button', { name: '保存凭据' }))
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
    const init = fetcher.mock.calls[0][1] as RequestInit
    expect(new Headers(init.headers).get('If-Match')).toBe('"1"')
    expect(JSON.parse(init.body as string).secret).toBe('')
    expect(JSON.parse(init.body as string)).not.toHaveProperty('allow_insecure_http')
    await waitFor(() => expect(screen.getByRole('button', { name: '保存凭据' })).toBeEnabled())
    expect(JSON.stringify(client.getQueryCache().getAll())).not.toContain('unexpected-response-secret')
  })
  it('版本冲突清除新密钥并提供刷新入口', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({}, 409)))
    const reload = vi.fn()
    view(<CredentialEditor detail={detail} reload={reload} />)
    fireEvent.change(screen.getByLabelText('认证密钥'), { target: { value: 'temporary-secret' } })
    fireEvent.click(screen.getByRole('button', { name: '保存凭据' }))
    await screen.findByText('凭据已更新，请刷新后重试。')
    expect(screen.getByLabelText('认证密钥')).toHaveValue('')
    fireEvent.click(screen.getByRole('button', { name: '刷新凭据' }))
    expect(reload).toHaveBeenCalledOnce()
  })
  it('新目标不能复用旧密钥', () => {
    const fetcher = vi.fn(); vi.stubGlobal('fetch', fetcher)
    view(<CredentialEditor detail={detail} reload={() => undefined} />)
    fireEvent.change(screen.getByLabelText(/^允许访问的目标地址/), { target: { value: 'https://other.example.com' } })
    expect(screen.getByLabelText(/^认证密钥/)).toBeRequired()
    fireEvent.submit(screen.getByRole('button', { name: '保存凭据' }).closest('form')!)
    expect(screen.getByText('请填写完整认证信息；更换目标或认证方式需要新密钥。')).toBeInTheDocument()
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('IPv4-mapped IPv6 源编辑时可以留空保留原认证', () => {
    vi.stubGlobal('fetch', vi.fn())
    view(<CredentialEditor detail={{ ...detail, origin: 'https://[::ffff:127.0.0.1]' }} reload={() => undefined} />)
    expect(screen.getByLabelText('认证密钥')).not.toBeRequired()
    fireEvent.change(screen.getByLabelText(/^允许访问的目标地址/), { target: { value: 'https://[::ffff:7f00:1]' } })
    expect(screen.getByLabelText('认证密钥')).not.toBeRequired()
  })
  it('扫描任务仅选择启用凭据并返回安全版本引用', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ items: [detail, { ...detail, id: 'disabled', name: '停用凭据', disabled: true }] })))
    const change = vi.fn()
    view(<TargetCredentialSelector onChange={change} />)
    await screen.findByRole('option', { name: /推理服务/ })
    expect(screen.queryByRole('option', { name: /停用凭据/ })).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox'), { target: { value: detail.id } })
    expect(change).toHaveBeenCalledWith(detail)
  })
  it.each(['updated', 'disabled', 'deleted'])('已选凭据 %s 后保留认证意图，必须明确重选或取消', async (state) => {
    const items = state === 'deleted' ? [] : [{ ...detail, revision: state === 'updated' ? 2 : 1, disabled: state === 'disabled' }]
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ items })))
    const change = vi.fn()
    view(<TargetCredentialSelector value={detail} onChange={change} />)
    await screen.findByText('已选凭据已变更、停用或删除，请重新选择；如需匿名扫描，请明确选择“不使用目标凭据”。')
    expect(change).not.toHaveBeenCalled()
    expect(screen.getByRole('combobox')).not.toHaveValue('')
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '' } })
    expect(change).toHaveBeenCalledWith(undefined)
  })
})
