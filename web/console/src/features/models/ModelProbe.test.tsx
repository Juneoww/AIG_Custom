/**
 * 功能：验证模型配置示例和保存前连通性测试。
 * 实现：通过真实表单与受控 fetch 响应检查提交、取消和密钥边界。
 * 输入：虚构模型参数和成功/失败响应；输出：安全反馈，不写入模型配置。
 * 依赖：Vitest、Testing Library、Fluent 主题。
 */
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { ModelForm } from './ModelForm'
import type { ModelCatalogItem } from './api'

const saved: ModelCatalogItem = { id: 'saved-model', name: '已有模型', provider_model: 'internal-chat', base_url: 'http://models.internal:8000/v1', scope: 'private', source: 'platform', read_only: false, note: '', limit: 0, disabled: false }
const response = (code = 'ok', status = 'success') => new Response(JSON.stringify({ status, code, message: '不可信上游说明', elapsed_ms: 12, token: 'response-secret' }), { headers: { 'Content-Type': 'application/json' } })
function view(model?: ModelCatalogItem) {
  const onSaved = vi.fn(), onCancel = vi.fn()
  return { ...render(<ThemeProvider initialMode="light"><ModelForm role="user" model={model} onSaved={onSaved} onCancel={onCancel} /></ThemeProvider>), onSaved, onCancel }
}
function fillConnection() {
  fireEvent.change(screen.getByLabelText(/^模型ID/), { target: { value: 'internal-chat' } })
  fireEvent.change(screen.getByLabelText(/^基础 URL/), { target: { value: saved.base_url } })
  fireEvent.change(screen.getByLabelText(/^访问 Token/), { target: { value: 'fictional-model-token' } })
}
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })
beforeEach(() => { vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} }) })

describe('模型配置连通性', () => {
  it('所有空白输入都有灰色示例，调用限制可空白', () => {
    view()
    for (const placeholder of ['例如：内网安全分析模型', '例如：internal-chat', '例如：http://inference.internal:8000/v1', '输入模型服务的 API Key', '例如：5', '例如：用于内网安全扫描分析']) expect(screen.getByPlaceholderText(placeholder)).toBeInTheDocument()
    expect(screen.getByLabelText('调用限制')).toHaveValue(null)
    expect(screen.queryByLabelText(/供应商模型/)).not.toBeInTheDocument()
  })
  it('无需名称即可测试当前输入，结果安全且不自动保存；修改连接参数清除结果', async () => {
    const fetcher = vi.fn().mockResolvedValue(response())
    vi.stubGlobal('fetch', fetcher)
    const current = view()
    fillConnection()
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    expect(await screen.findByRole('status')).toHaveTextContent('连接成功，模型可正常响应')
    expect(screen.getByRole('status')).toHaveTextContent('12 ms')
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(fetcher.mock.calls[0][0]).toBe(`${window.location.origin}/api/v1/platform/models/test`)
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).toEqual({ provider_model: 'internal-chat', base_url: saved.base_url, token: 'fictional-model-token' })
    expect(current.onSaved).not.toHaveBeenCalled()
    expect(document.body).not.toHaveTextContent('不可信上游说明')
    expect(document.body).not.toHaveTextContent('response-secret')
    fireEvent.change(screen.getByLabelText(/^模型ID/), { target: { value: 'another-model' } })
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
  it('编辑时留空 Token 使用已保存凭据，改 URL 后要求重新输入', async () => {
    const fetcher = vi.fn().mockResolvedValue(response())
    vi.stubGlobal('fetch', fetcher)
    view(saved)
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    await screen.findByRole('status')
    expect(fetcher.mock.calls[0][0]).toBe(`${window.location.origin}/api/v1/platform/models/saved-model/test`)
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).not.toHaveProperty('token')
    fireEvent.change(screen.getByLabelText(/^基础 URL/), { target: { value: 'http://other.internal/v1' } })
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('修改基础 URL 后，请重新输入 Token 再测试。')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })
  it('缺少连接参数不发请求，失败反馈使用固定说明且可重试', async () => {
    const fetcher = vi.fn().mockResolvedValue(response('authentication_failed', 'error'))
    vi.stubGlobal('fetch', fetcher)
    view()
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('请填写模型ID、有效的 HTTP/HTTPS 基础 URL 和访问 Token。')
    expect(fetcher).not.toHaveBeenCalled()
    fillConnection()
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('认证失败，请检查访问 Token 和模型权限。')
    expect(screen.getByRole('button', { name: '测试连通性' })).toBeEnabled()
  })
  it('测试中禁止重复请求和保存，卸载取消且忽略迟到响应', async () => {
    let resolve!: (value: Response) => void
    let signal: AbortSignal | undefined
    const fetcher = vi.fn((_url: string, init: RequestInit) => { signal = init.signal ?? undefined; return new Promise<Response>(done => { resolve = done }) })
    vi.stubGlobal('fetch', fetcher)
    const current = view(saved)
    const button = screen.getByRole('button', { name: '测试连通性' })
    fireEvent.click(button); fireEvent.click(button)
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
    expect(screen.getByRole('button', { name: '测试中…' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '保存模型' })).toBeDisabled()
    expect(screen.getByLabelText(/^基础 URL/)).toBeDisabled()
    current.unmount()
    expect(signal?.aborted).toBe(true)
    await act(async () => resolve(response()))
    expect(current.onSaved).not.toHaveBeenCalled()
  })
  it('更改基础 URL 后留空 Token 不能保存，防止再测试时复用旧凭据', async () => {
    const fetcher = vi.fn()
    vi.stubGlobal('fetch', fetcher)
    view(saved)
    fireEvent.change(screen.getByLabelText(/^基础 URL/), { target: { value: 'http://other.internal/v1' } })
    fireEvent.click(screen.getByRole('button', { name: '保存模型' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('修改基础 URL 后，请重新输入 Token 再保存。')
    expect(fetcher).not.toHaveBeenCalled()
  })
  it('空白调用限制保存为默认 0，示例不会被提交', async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ...saved, token: '********' }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetcher)
    const current = view()
    fillConnection()
    fireEvent.change(screen.getByLabelText(/^模型名称/), { target: { value: '我的模型' } })
    fireEvent.click(screen.getByRole('button', { name: '创建私有模型' }))
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
    const body = JSON.parse(fetcher.mock.calls[0][1].body)
    expect(body.limit).toBe(0)
    expect(body.note).toBe('')
    await waitFor(() => expect(current.onSaved).toHaveBeenCalledTimes(1))
  })
  it.each(['保存失败', '无效限制'])('%s 清空 Token 时必须清除旧测试结果', async mode => {
    const fetcher = vi.fn().mockResolvedValueOnce(response()).mockResolvedValueOnce(new Response(null, { status: 500 }))
    vi.stubGlobal('fetch', fetcher)
    view(saved)
    fireEvent.change(screen.getByLabelText('新 Token（留空保持不变）'), { target: { value: 'fictional-new-token' } })
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    await screen.findByRole('status')
    if (mode === '无效限制') fireEvent.change(screen.getByLabelText('调用限制'), { target: { value: '1.5' } })
    fireEvent.submit(screen.getByRole('form', { name: '编辑模型' }))
    await screen.findByRole('alert')
    expect(screen.getByLabelText('新 Token（留空保持不变）')).toHaveValue('')
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
  it('掩码 Token 显示参数错误，不提示网络错误', async () => {
    vi.stubGlobal('fetch', vi.fn())
    view(saved)
    fireEvent.change(screen.getByLabelText('新 Token（留空保持不变）'), { target: { value: '********' } })
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('模型配置无效')
  })
  it('尾斜线和默认端口等价时可以复用 Token，手动改 Token 清除结果', async () => {
    const fetcher = vi.fn().mockResolvedValue(response())
    vi.stubGlobal('fetch', fetcher)
    view({ ...saved, base_url: 'http://models.internal/v1' })
    fireEvent.change(screen.getByLabelText(/^基础 URL/), { target: { value: 'http://models.internal:80/v1/' } })
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    await screen.findByRole('status')
    expect(JSON.parse(fetcher.mock.calls[0][1].body)).not.toHaveProperty('token')
    fireEvent.change(screen.getByLabelText('新 Token（留空保持不变）'), { target: { value: 'fictional-replacement' } })
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
  it('取消按钮取消测试并清空 Token，迟到结果不会显示', async () => {
    let resolve!: (value: Response) => void
    let signal: AbortSignal | undefined
    vi.stubGlobal('fetch', vi.fn((_url: string, init: RequestInit) => { signal = init.signal ?? undefined; return new Promise<Response>(done => { resolve = done }) }))
    const current = view()
    fillConnection()
    fireEvent.click(screen.getByRole('button', { name: '测试连通性' }))
    fireEvent.click(screen.getByRole('button', { name: '取消' }))
    expect(signal?.aborted).toBe(true)
    expect(current.onCancel).toHaveBeenCalledTimes(1)
    expect(screen.getByLabelText(/^访问 Token/)).toHaveValue('')
    await act(async () => resolve(response()))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })
})
