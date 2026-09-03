/**
 * 功能：验证扫描模型选择器仅消费受治理模型目录。
 * 实现：以真实目录请求、QueryClient 和路由驱动加载、失败重试与分页行为。
 * 输入：安全模型目录响应及选择器值。
 * 输出：可访问的原生选择控件，且不渲染目录敏感字段。
 * 依赖：Testing Library、TanStack Query、React Router、Fluent UI。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { GovernedModelSelector } from './GovernedModelSelector'

const FIXTURE_BASE_URL = 'https://model-gateway-secret.example.test/v1'
const FIXTURE_NOTE = 'do-not-render-this-note'

function catalogItem(overrides: Record<string, unknown> = {}) {
  return {
    id: 'model-1',
    owner_user_id: 'user-1',
    scope: 'private',
    name: '受治理私有模型',
    provider_model: 'gpt-secure',
    base_url: FIXTURE_BASE_URL,
    note: FIXTURE_NOTE,
    limit: 4,
    disabled: false,
    token: '********',
    source: 'platform',
    read_only: false,
    ...overrides,
  }
}

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function renderSelector(onChange = vi.fn()) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter><GovernedModelSelector onChange={onChange} /></MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...view, onChange, queryClient }
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('GovernedModelSelector', () => {
  it('在首屏加载时展示状态，且加载后仅列出启用模型和不使用模型选项', async () => {
    let resolvePage: ((value: Response) => void) | undefined
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { resolvePage = resolve })))
    const { onChange } = renderSelector()

    expect(screen.getByText('正在加载模型…')).toBeInTheDocument()
    await waitFor(() => expect(resolvePage).toBeTypeOf('function'))
    resolvePage?.(response({
      items: [
        catalogItem(),
        catalogItem({ id: 'yaml-1', name: 'YAML 全局模型', scope: 'global', source: 'yaml', read_only: true, owner_user_id: '' }),
        catalogItem({ id: 'disabled-1', name: '已停用模型', disabled: true }),
      ],
      total: 3,
      page: 1,
      page_size: 100,
    }))

    const select = await screen.findByRole('combobox', { name: '扫描模型' })
    expect(screen.getByRole('option', { name: '不使用模型' })).toHaveValue('')
    expect(screen.getByRole('option', { name: '受治理私有模型（gpt-secure，私有）' })).toHaveValue('model-1')
    expect(screen.getByRole('option', { name: 'YAML 全局模型（gpt-secure，全局）' })).toHaveValue('yaml-1')
    expect(screen.queryByRole('option', { name: '已停用模型（gpt-secure，私有）' })).not.toBeInTheDocument()
    expect(document.body).not.toHaveTextContent(FIXTURE_BASE_URL)
    expect(document.body).not.toHaveTextContent(FIXTURE_NOTE)
    expect(document.body).not.toHaveTextContent('********')

    fireEvent.change(select, { target: { value: 'model-1' } })
    fireEvent.change(select, { target: { value: '' } })
    expect(onChange).toHaveBeenNthCalledWith(1, 'model-1')
    expect(onChange).toHaveBeenNthCalledWith(2, undefined)
  })

  it('空目录保留不使用模型并链接到模型配置', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ items: [], total: 0, page: 1, page_size: 100 })))
    renderSelector()

    expect(await screen.findByRole('option', { name: '不使用模型' })).toHaveValue('')
    expect(screen.getByRole('link', { name: '前往凭证配置 → 模型配置' })).toHaveAttribute('href', '/models')
  })

  it('首屏目录失败后允许重试', async () => {
    const fetchMock = vi.fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(response({ items: [catalogItem()], total: 1, page: 1, page_size: 100 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelector()

    expect(await screen.findByText('模型目录加载失败')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '重试加载模型' }))
    expect(await screen.findByRole('option', { name: '受治理私有模型（gpt-secure，私有）' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('通过加载更多模型展示后续页面，并在后续页面失败时保留选择并允许重试', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: [catalogItem()], total: 101, page: 1, page_size: 100 }))
      .mockRejectedValueOnce(new Error('page two offline'))
      .mockResolvedValueOnce(response({ items: [catalogItem({ id: 'model-2', name: '第二页模型' })], total: 101, page: 2, page_size: 100 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelector()

    expect(await screen.findByRole('button', { name: '加载更多模型' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '加载更多模型' }))
    expect(await screen.findByText('加载更多模型失败')).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '受治理私有模型（gpt-secure，私有）' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '重试加载更多模型' }))
    await waitFor(() => expect(screen.getByRole('option', { name: '第二页模型（gpt-secure，私有）' })).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })
})
