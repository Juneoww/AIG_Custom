/**
 * 功能：验证扫描模型选择器仅消费受治理模型目录。
 * 实现：以真实目录请求、QueryClient 和路由驱动加载、失败重试与分页行为。
 * 输入：安全模型目录响应及选择器值。
 * 输出：可访问的原生选择控件，且不渲染目录敏感字段。
 * 依赖：Testing Library、TanStack Query、React Router、Fluent UI。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { StrictMode, useState } from 'react'
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

function requestedURL(fetchMock: ReturnType<typeof vi.fn>, call: number): URL {
  return new URL(fetchMock.mock.calls[call]?.[0] as string)
}

function createQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderSelector(onChange = vi.fn(), queryClient = createQueryClient()) {
  const view = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter><GovernedModelSelector onChange={onChange} /></MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...view, onChange, queryClient }
}

function renderSelectorWithValue(value: string, onChange = vi.fn(), onAvailabilityChange = vi.fn(), strict = false, queryClient = createQueryClient()) {
  const selector = <GovernedModelSelector value={value} onChange={onChange} onAvailabilityChange={onAvailabilityChange} />
  const view = render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>{strict ? <StrictMode>{selector}</StrictMode> : selector}</MemoryRouter>
    </QueryClientProvider>,
  )
  return { ...view, onChange, onAvailabilityChange, queryClient }
}

function cacheFirstCatalogPage(queryClient: QueryClient, items = [catalogItem({ id: 'page-one-model' })], total = 101) {
  queryClient.setQueryData(['governed-model-catalog'], {
    pages: [{ items, total, page: 1, page_size: 100 }],
    pageParams: [1],
  })
}

function ControlledSelector({ initialValue, onAvailabilityChange }: { initialValue: string; onAvailabilityChange: ReturnType<typeof vi.fn> }) {
  const [value, setValue] = useState<string | undefined>(initialValue)
  return <GovernedModelSelector value={value} onChange={setValue} onAvailabilityChange={onAvailabilityChange} />
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
    const fetchMock = vi.fn(() => new Promise<Response>((resolve) => { resolvePage = resolve }))
    vi.stubGlobal('fetch', fetchMock)
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

    await screen.findByRole('option', { name: '受治理私有模型（gpt-secure，私有）' })
    const select = screen.getByRole('combobox', { name: '扫描模型' })
    expect(screen.getByRole('option', { name: '不使用模型' })).toHaveValue('')
    expect(screen.getByRole('option', { name: '受治理私有模型（gpt-secure，私有）' })).toHaveValue('model-1')
    expect(screen.getByRole('option', { name: 'YAML 全局模型（gpt-secure，全局）' })).toHaveValue('yaml-1')
    expect(screen.queryByRole('option', { name: '已停用模型（gpt-secure，私有）' })).not.toBeInTheDocument()
    expect(document.body).not.toHaveTextContent(FIXTURE_BASE_URL)
    expect(document.body).not.toHaveTextContent(FIXTURE_NOTE)
    expect(document.body).not.toHaveTextContent('********')
    expect(document.body).not.toHaveTextContent('user-1')
    expect(requestedURL(fetchMock, 0).search).toBe('?page=1&page_size=100')

    fireEvent.change(select, { target: { value: 'model-1' } })
    fireEvent.change(select, { target: { value: '' } })
    expect(onChange).toHaveBeenNthCalledWith(1, 'model-1')
    expect(onChange).toHaveBeenNthCalledWith(2, undefined)
  })

  it('空目录保留不使用模型并链接到模型配置', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ items: [], total: 0, page: 1, page_size: 100 })))
    renderSelector()

    expect(await screen.findByRole('link', { name: '前往凭证配置 → 模型配置' })).toHaveAttribute('href', '/models')
    expect(screen.getByRole('option', { name: '不使用模型' })).toHaveValue('')
  })

  it('目录耗尽后清除不存在的模型 ID，但不会把它渲染为普通选项', async () => {
    const onChange = vi.fn()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ items: [catalogItem()], total: 1, page: 1, page_size: 100 })))
    renderSelectorWithValue('stale-model-id', onChange)

    expect(await screen.findByText('已选模型不可用，已清除选择。')).toBeInTheDocument()
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(undefined))
    expect(screen.queryByRole('option', { name: /stale-model-id/ })).not.toBeInTheDocument()
  })

  it('受控调用方清除失效 ID 后不保留不可用提示，并最终报告 available', async () => {
    const onAvailabilityChange = vi.fn()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ items: [catalogItem()], total: 1, page: 1, page_size: 100 })))
    const queryClient = createQueryClient()
    render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter><ControlledSelector initialValue="stale-model-id" onAvailabilityChange={onAvailabilityChange} /></MemoryRouter>
      </QueryClientProvider>,
    )

    await waitFor(() => expect(screen.getByRole('combobox', { name: '扫描模型' })).toHaveValue(''))
    expect(screen.queryByText('已选模型不可用，已清除选择。')).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: /stale-model-id/ })).not.toBeInTheDocument()
    await waitFor(() => expect(onAvailabilityChange.mock.calls.map(([availability]) => availability)).toEqual(['pending', 'unavailable', 'available']))
  })

  it('自动加载下一页验证有效预选模型，并在找到后保留该 ID', async () => {
    const onChange = vi.fn()
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: [catalogItem({ id: 'page-one-model' })], total: 101, page: 1, page_size: 100 }))
      .mockResolvedValueOnce(response({ items: [catalogItem({ id: 'preselected-model', name: '预选第二页模型' })], total: 101, page: 2, page_size: 100 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelectorWithValue('preselected-model', onChange, vi.fn(), true)

    expect(await screen.findByRole('option', { name: '预选第二页模型（gpt-secure，私有）' })).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
    expect(requestedURL(fetchMock, 1).search).toBe('?page=2&page_size=100')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('验证预选模型时保留 combobox 的真实 ID，并允许用户选择不使用模型', async () => {
    let resolvePageTwo: ((value: Response) => void) | undefined
    const onChange = vi.fn()
    const onAvailabilityChange = vi.fn()
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: [catalogItem({ id: 'page-one-model' })], total: 101, page: 1, page_size: 100 }))
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { resolvePageTwo = resolve }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelectorWithValue('preselected-model', onChange, onAvailabilityChange)

    const select = await screen.findByRole('combobox', { name: '扫描模型' })
    expect(await screen.findByText('正在验证已选模型')).toBeInTheDocument()
    expect(select).toHaveValue('preselected-model')
    expect(screen.getByRole('option', { name: '已选模型（ID: preselected-model）：正在验证' })).toBeDisabled()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(screen.getByRole('button', { name: '正在加载更多模型…' })).toBeDisabled()
    expect(onAvailabilityChange).toHaveBeenCalledWith('pending')

    fireEvent.change(select, { target: { value: '' } })
    expect(onChange).toHaveBeenCalledWith(undefined)
    await waitFor(() => expect(resolvePageTwo).toBeTypeOf('function'))
    resolvePageTwo?.(response({ items: [catalogItem({ id: 'preselected-model' })], total: 101, page: 2, page_size: 100 }))
  })

  it('缓存首批目录后台刷新结束后，自动验证才请求下一页', async () => {
    let resolveRefresh: ((value: Response) => void) | undefined
    const queryClient = createQueryClient()
    cacheFirstCatalogPage(queryClient)
    const fetchMock = vi.fn()
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveRefresh = resolve }))
      .mockResolvedValueOnce(response({ items: [catalogItem({ id: 'preselected-model', name: '缓存后的预选模型' })], total: 101, page: 2, page_size: 100 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelectorWithValue('preselected-model', vi.fn(), vi.fn(), false, queryClient)

    expect(await screen.findByRole('button', { name: '正在刷新模型目录…' })).toBeDisabled()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(resolveRefresh).toBeTypeOf('function'))
    resolveRefresh?.(response({ items: [catalogItem({ id: 'page-one-model' })], total: 101, page: 1, page_size: 100 }))

    expect(await screen.findByRole('option', { name: '缓存后的预选模型（gpt-secure，私有）' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('缓存目录后台刷新期间将已加载预选模型保持为 pending，成功后恢复 available', async () => {
    let resolveRefresh: ((value: Response) => void) | undefined
    const onAvailabilityChange = vi.fn()
    const queryClient = createQueryClient()
    cacheFirstCatalogPage(queryClient, [catalogItem({ id: 'preselected-model', name: '缓存模型' })], 1)
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { resolveRefresh = resolve })))
    renderSelectorWithValue('preselected-model', vi.fn(), onAvailabilityChange, false, queryClient)

    expect(await screen.findByRole('option', { name: '已选模型（ID: preselected-model）：正在验证' })).toBeDisabled()
    expect(screen.queryByRole('link', { name: '前往凭证配置 → 模型配置' })).not.toBeInTheDocument()
    expect(onAvailabilityChange).toHaveBeenCalledWith('pending')
    await waitFor(() => expect(resolveRefresh).toBeTypeOf('function'))
    resolveRefresh?.(response({ items: [catalogItem({ id: 'preselected-model', name: '刷新后模型' })], total: 1, page: 1, page_size: 100 }))

    expect(await screen.findByRole('option', { name: '刷新后模型（gpt-secure，私有）' })).toBeInTheDocument()
    await waitFor(() => expect(onAvailabilityChange.mock.calls.at(-1)).toEqual(['available']))
    expect(screen.queryByRole('option', { name: /正在验证/ })).not.toBeInTheDocument()
  })

  it.each([
    ['缺失模型', 'missing-model', [catalogItem({ id: 'page-one-model' })]],
    ['停用模型', 'disabled-model', [catalogItem({ id: 'disabled-model', disabled: true })]],
  ])('缓存%s的后台刷新失败时保持 pending 且不清除', async (_label, value, items) => {
    const onChange = vi.fn()
    const onAvailabilityChange = vi.fn()
    const queryClient = createQueryClient()
    cacheFirstCatalogPage(queryClient, items, 1)
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('refresh offline')))
    renderSelectorWithValue(value, onChange, onAvailabilityChange, false, queryClient)

    expect(await screen.findByText('模型目录刷新失败，当前选择待确认')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '扫描模型' })).toHaveValue(value)
    expect(onChange).not.toHaveBeenCalled()
    expect(onAvailabilityChange.mock.calls.at(-1)).toEqual(['pending'])
    expect(screen.queryByRole('link', { name: '前往凭证配置 → 模型配置' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试刷新模型目录' })).toBeInTheDocument()
  })

  it('无预选模型时以通用文案提示缓存目录刷新失败', async () => {
    const queryClient = createQueryClient()
    cacheFirstCatalogPage(queryClient, [catalogItem({ id: 'cached-model' })], 1)
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('refresh offline')))
    renderSelector(vi.fn(), queryClient)

    expect(await screen.findByText('模型目录刷新失败')).toBeInTheDocument()
    expect(screen.queryByText('模型目录刷新失败，当前选择待确认')).not.toBeInTheDocument()
  })

  it('无预选模型时，后台刷新期间禁用加载更多且完成后恢复可用', async () => {
    let resolveRefresh: ((value: Response) => void) | undefined
    const queryClient = createQueryClient()
    cacheFirstCatalogPage(queryClient)
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => { resolveRefresh = resolve })))
    renderSelector(vi.fn(), queryClient)

    expect(await screen.findByRole('button', { name: '正在刷新模型目录…' })).toBeDisabled()
    await waitFor(() => expect(resolveRefresh).toBeTypeOf('function'))
    resolveRefresh?.(response({ items: [catalogItem({ id: 'page-one-model' })], total: 101, page: 1, page_size: 100 }))

    expect(await screen.findByRole('button', { name: '加载更多模型' })).toBeEnabled()
  })

  it('预选模型在下一页验证失败时不清除，并允许重试后保留', async () => {
    const onChange = vi.fn()
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: [catalogItem({ id: 'page-one-model' })], total: 101, page: 1, page_size: 100 }))
      .mockRejectedValueOnce(new Error('page two offline'))
      .mockResolvedValueOnce(response({ items: [catalogItem({ id: 'preselected-model', name: '重试后的预选模型' })], total: 101, page: 2, page_size: 100 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelectorWithValue('preselected-model', onChange)

    expect(await screen.findByText('加载更多模型失败')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
    expect(screen.getByRole('combobox', { name: '扫描模型' })).toHaveValue('preselected-model')
    expect(screen.getByRole('option', { name: '已选模型（ID: preselected-model）：目录加载失败，保留待重试' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '重试加载更多模型' }))
    expect(await screen.findByRole('option', { name: '重试后的预选模型（gpt-secure，私有）' })).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('已观察到 canonical 首项停用时立即清除预选 ID，且不让 YAML 同 ID 绕过', async () => {
    const onChange = vi.fn()
    const fetchMock = vi.fn().mockResolvedValue(response({
      items: [
        catalogItem({ id: 'disabled-shared-model', name: '停用平台模型', disabled: true }),
        catalogItem({ id: 'disabled-shared-model', name: 'YAML 同 ID 模型', source: 'yaml', scope: 'global', owner_user_id: '', read_only: true }),
      ],
      total: 101,
      page: 1,
      page_size: 100,
    }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelectorWithValue('disabled-shared-model', onChange)

    expect(await screen.findByText('已选模型不可用，已清除选择。')).toBeInTheDocument()
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(undefined))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('option', { name: /YAML 同 ID 模型/ })).not.toBeInTheDocument()
  })

  it('在初始加载或首批目录失败时不清除当前模型 ID', async () => {
    let rejectPage: ((reason?: unknown) => void) | undefined
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((_, reject) => { rejectPage = reject })))
    const { onChange } = renderSelectorWithValue('still-selected')

    expect(screen.getByText('正在加载模型…')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
    await waitFor(() => expect(rejectPage).toBeTypeOf('function'))
    rejectPage?.(new Error('offline'))
    expect(await screen.findByText('模型目录加载失败')).toBeInTheDocument()
    expect(onChange).not.toHaveBeenCalled()
  })

  it('拒绝 page_size 与请求页大小不一致的目录响应', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({ items: [catalogItem()], total: 1, page: 1, page_size: 99 })))
    renderSelector()

    expect(await screen.findByText('模型目录加载失败')).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: '受治理私有模型（gpt-secure，私有）' })).not.toBeInTheDocument()
  })

  it('首屏目录失败后允许重试', async () => {
    let resolveRetry: ((value: Response) => void) | undefined
    const fetchMock = vi.fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveRetry = resolve }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelector()

    expect(await screen.findByText('模型目录加载失败')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '重试加载模型' }))
    await waitFor(() => expect(resolveRetry).toBeTypeOf('function'))
    resolveRetry?.(response({ items: [catalogItem()], total: 1, page: 1, page_size: 100 }))
    expect(await screen.findByRole('option', { name: '受治理私有模型（gpt-secure，私有）' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('重复的正确分页内容停止自动验证且保留待确认的预选 ID', async () => {
    const onChange = vi.fn()
    const repeatedItems = [catalogItem({ id: 'page-one-model' })]
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: repeatedItems, total: 300, page: 1, page_size: 100 }))
      .mockResolvedValueOnce(response({ items: repeatedItems, total: 300, page: 2, page_size: 100 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelectorWithValue('missing-model', onChange)

    expect(await screen.findByText('模型目录分页响应重复，无法继续加载；已选模型尚未确认')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '扫描模型' })).toHaveValue('missing-model')
    expect(screen.getByRole('option', { name: '已选模型（ID: missing-model）：模型目录分页响应重复，尚未确认' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '重新加载模型目录' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('同页内容仅顺序不同也会停止自动验证并避免后续请求', async () => {
    const onChange = vi.fn()
    const firstItems = [catalogItem({ id: 'first-model' }), catalogItem({ id: 'second-model', name: '第二模型' })]
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: firstItems, total: 300, page: 1, page_size: 100 }))
      .mockResolvedValueOnce(response({ items: [...firstItems].reverse(), total: 300, page: 2, page_size: 100 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelectorWithValue('missing-model', onChange)

    expect(await screen.findByText('模型目录分页响应重复，无法继续加载；已选模型尚未确认')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(onChange).not.toHaveBeenCalled()
  })

  it('通过加载更多模型展示后续页面，并在后续页面失败时保留选择并允许重试', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: [catalogItem()], total: 101, page: 1, page_size: 100 }))
      .mockRejectedValueOnce(new Error('page two offline'))
      .mockResolvedValueOnce(response({ items: [catalogItem({ id: 'model-2', name: '第二页模型' })], total: 101, page: 2, page_size: 100 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelector()

    expect(await screen.findByRole('button', { name: '加载更多模型' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: '加载更多模型' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(requestedURL(fetchMock, 1).search).toBe('?page=2&page_size=100')
    expect(await screen.findByText('加载更多模型失败')).toBeInTheDocument()
    expect(screen.getByRole('option', { name: '受治理私有模型（gpt-secure，私有）' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '重试加载更多模型' }))
    await waitFor(() => expect(screen.getByRole('option', { name: '第二页模型（gpt-secure，私有）' })).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('拒绝第二页错误回传第一页元数据，避免追加重复选项或循环请求', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: [catalogItem()], total: 201, page: 1, page_size: 100 }))
      .mockResolvedValueOnce(response({ items: [catalogItem()], total: 201, page: 1, page_size: 100 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelector()

    expect(await screen.findByRole('button', { name: '加载更多模型' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '加载更多模型' }))

    expect(await screen.findByText('加载更多模型失败')).toBeInTheDocument()
    expect(screen.getAllByRole('option', { name: '受治理私有模型（gpt-secure，私有）' })).toHaveLength(1)
    expect(requestedURL(fetchMock, 1).search).toBe('?page=2&page_size=100')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('跨页同 ID 碰撞保留第一页 platform 项，并且不产生重复 option', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: [catalogItem({ id: 'shared-model', name: '平台优先模型' })], total: 101, page: 1, page_size: 100 }))
      .mockResolvedValueOnce(response({
        items: [
          catalogItem({ id: 'shared-model', name: 'YAML 同 ID 模型', source: 'yaml', scope: 'global', owner_user_id: '', read_only: true }),
          catalogItem({ id: 'page-two-model', name: '第二页唯一模型' }),
        ],
        total: 101,
        page: 2,
        page_size: 100,
      }))
    vi.stubGlobal('fetch', fetchMock)
    renderSelector()

    fireEvent.click(await screen.findByRole('button', { name: '加载更多模型' }))

    expect(await screen.findByRole('option', { name: '第二页唯一模型（gpt-secure，私有）' })).toBeInTheDocument()
    expect(screen.getAllByRole('option', { name: '平台优先模型（gpt-secure，私有）' })).toHaveLength(1)
    expect(screen.queryByRole('option', { name: 'YAML 同 ID 模型（gpt-secure，全局）' })).not.toBeInTheDocument()
  })
})
