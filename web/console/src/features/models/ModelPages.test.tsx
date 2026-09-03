/**
 * 功能：验证模型台账、配置表单和凭据重加密对话框的治理边界。
 * 实现：在真实 Query、主题、会话与内存路由中驱动 Fluent 交互。
 * 输入：三类角色、同 ID 多来源目录、敏感 Token 和失败/取消网络。
 * 输出：原生台账语义、受限动作、固定错误及已清理敏感输入。
 * 依赖：Testing Library、TanStack Query、React Router、SessionProvider 与 Fluent UI。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { SessionProvider, type SessionState } from '../auth/session'
import type { SubjectRole } from '../../shared/api/types'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import type { ModelCatalogItem } from './api'
import { ModelForm } from './ModelForm'
import { ModelListPage } from './ModelListPage'
import { RotateCredentialDialog } from './RotateCredentialDialog'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function catalogItem(overrides: Record<string, unknown> = {}) {
  return {
    id: 'model-shared', owner_user_id: 'user-1', scope: 'private', name: '生产私有模型',
    provider_model: 'gpt-secure', base_url: 'https://models.invalid/v1', note: '治理用', limit: 4,
    disabled: false, token: '********', source: 'platform', read_only: false,
    created_at: '2026-08-18T01:00:00Z', updated_at: '2026-08-18T02:00:00Z',
    ...overrides,
  }
}

const governancePageItems = [
  catalogItem({ id: 'model-configurable-page-2', name: '可配置私有模型' }),
  catalogItem({ id: 'model-disabled-page-2', name: '已停用可配置模型', disabled: true }),
  catalogItem({ id: 'model-read-only-page-2', name: '受限只读模型', read_only: true }),
]

function sessionFor(role: SubjectRole): SessionState {
  return {
    status: 'authenticated',
    subject: { id: `${role}-1`, username: role === 'user' ? 'operator' : role, role, must_change_password: false },
  }
}

function renderWithProviders(node: React.ReactNode, role: SubjectRole = 'user', path = '/models') {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = render(
    <ThemeProvider initialMode="light">
      <QueryClientProvider client={queryClient}>
        <SessionProvider initialState={sessionFor(role)}>
          <MemoryRouter initialEntries={[path]}>{node}</MemoryRouter>
        </SessionProvider>
      </QueryClientProvider>
    </ThemeProvider>,
  )
  return { ...view, queryClient }
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('ModelListPage', () => {
  it('普通用户仅管理自己的私有platform行，同ID的YAML行仍显式并存', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({
      items: [
        catalogItem(),
        catalogItem({ owner_user_id: '', scope: 'global', name: 'YAML模型', source: 'yaml', read_only: true }),
      ], total: 2, page: 1, page_size: 20,
      raw_token: 'response-sentinel',
    })))
    renderWithProviders(<ModelListPage />)

    const table = await screen.findByRole('table', { name: '受治理模型台账' })
    expect(table.tagName).toBe('TABLE')
    expect(screen.getByRole('columnheader', { name: '来源' }).tagName).toBe('TH')
    expect(screen.getAllByText('model-shared')).toHaveLength(2)
    expect(screen.getByText('数据库')).toBeInTheDocument()
    expect(screen.getByText('YAML 只读')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '新增私有模型' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '编辑 生产私有模型' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '删除 生产私有模型' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /YAML模型/ })).not.toBeInTheDocument()
    expect(document.body).not.toHaveTextContent('response-sentinel')
    expect(document.body).not.toHaveTextContent('********')
  })

  it('管理员只对全局platform行显示编辑、删除与轮换加密', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({
      items: [
        catalogItem({ owner_user_id: '', scope: 'global', name: '全局模型', read_only: false }),
        catalogItem({ id: 'model-private-user', name: '用户私有模型', read_only: true }),
      ], total: 2, page: 1, page_size: 20,
    })))
    renderWithProviders(<ModelListPage />, 'admin')

    expect(await screen.findByRole('button', { name: '编辑 全局模型' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '新增全局模型' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '删除 全局模型' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '轮换加密 全局模型' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /用户私有模型/ })).not.toBeInTheDocument()
  })

  it('审计员目录只读，不出现创建或任何写动作', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({
      items: [catalogItem({ owner_user_id: '', scope: 'global', name: '审计可见模型', read_only: true })],
      total: 1, page: 1, page_size: 20,
    })))
    renderWithProviders(<ModelListPage />, 'auditor')

    await screen.findByText('审计可见模型')
    expect(screen.queryByRole('button', { name: /新增私有|新增全局|编辑|删除|轮换加密/ })).not.toBeInTheDocument()
  })

  it('从可分享URL呈现模型治理态势与原生模型台账', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({
      items: governancePageItems, total: 45, page: 2, page_size: 20,
    }))
    vi.stubGlobal('fetch', fetchMock)
    renderWithProviders(<ModelListPage />, 'user', '/models?page=2')

    const table = await screen.findByRole('table', { name: '受治理模型台账' })
    expect(table.tagName).toBe('TABLE')
    expect(screen.getByRole('columnheader', { name: '来源' }).tagName).toBe('TH')
    const ledger = within(table)
    expect(ledger.getAllByText('数据库')).not.toHaveLength(0)
    expect(ledger.getAllByText('私有')).not.toHaveLength(0)
    expect(ledger.getAllByText('已停用')).not.toHaveLength(0)
    expect(ledger.getAllByText('只读')).not.toHaveLength(0)
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/models?page=2&page_size=20')
    expect(screen.getByRole('button', { name: '编辑 可配置私有模型' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '编辑 已停用可配置模型' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /受限只读模型/ })).not.toBeInTheDocument()
    expect(screen.getByText('共 45 条，第 2 页')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上一页' })).toBeEnabled()

    const summary = await screen.findByRole('region', { name: '模型治理态势' })
    const currentQuery = screen.getByRole('group', { name: '当前查询' })
    const signals = screen.getByRole('group', { name: '本页治理信号' })
    expect(summary.compareDocumentPosition(table) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(currentQuery).toHaveTextContent('当前查询')
    expect(currentQuery).toHaveTextContent(/匹配模型\s*45/)
    expect(currentQuery).toHaveTextContent(/第\s*2\s*页/)
    expect(signals).toHaveTextContent(/可配置模型\s*2/)
    expect(signals).toHaveTextContent(/已停用\s*1/)
    expect(signals).toHaveTextContent(/只读项\s*1/)
    expect(signals).not.toHaveTextContent(/可配置模型\s*45/)

    fireEvent.click(screen.getByRole('button', { name: '上一页' }))
    await waitFor(() => expect(fetchMock.mock.calls.some(
      ([input]) => input === 'http://localhost:3000/api/v1/platform/models?page=1&page_size=20',
    )).toBe(true))
  })

  it('空模型目录保留当前查询但不渲染本页治理信号', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 0, page: 1, page_size: 20 })))
    renderWithProviders(<ModelListPage />, 'auditor')

    const summary = await screen.findByRole('region', { name: '模型治理态势' })
    expect(screen.getByRole('group', { name: '当前查询' })).toHaveTextContent(/匹配模型\s*0/)
    expect(await screen.findByText('暂无可见模型', { selector: '[role="status"] *' })).toBeInTheDocument()
    expect(summary).not.toHaveTextContent(/可配置模型\s*0/)
    expect(summary).not.toHaveTextContent(/已停用\s*0/)
    expect(summary).not.toHaveTextContent(/只读项\s*0/)
    expect(screen.queryByRole('group', { name: '本页治理信号' })).not.toBeInTheDocument()
  })

  it('有匹配模型但当前页为空时保留服务端查询与可用上一页', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 45, page: 3, page_size: 20 })))
    renderWithProviders(<ModelListPage />, 'auditor', '/models?page=3')

    expect(await screen.findByText('当前页没有模型', { selector: '[role="status"] *' })).toBeInTheDocument()
    expect(screen.queryByText('暂无可见模型')).not.toBeInTheDocument()
    const summary = await screen.findByRole('region', { name: '模型治理态势' })
    expect(screen.getByRole('group', { name: '当前查询' })).toHaveTextContent(/匹配模型\s*45/)
    expect(summary).toHaveTextContent(/第\s*3\s*页/)
    expect(summary).not.toHaveTextContent(/可配置模型\s*0/)
    expect(summary).not.toHaveTextContent(/已停用\s*0/)
    expect(summary).not.toHaveTextContent(/只读项\s*0/)
    expect(screen.queryByRole('group', { name: '本页治理信号' })).not.toBeInTheDocument()
    expect(screen.getByText('共 45 条，第 3 页')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上一页' })).toBeEnabled()
  })

  it('规范化恶意页码并分离空、403和失败状态', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 0, page: 1, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)
    renderWithProviders(<ModelListPage />, 'auditor', '/models?page=1001&scope=private')

    expect(await screen.findByText('暂无可见模型', { selector: '[role="status"] *' })).toBeInTheDocument()
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/models?page=1&page_size=20')
  })

  it.each([[403, '无权查看模型目录'], [500, '暂时无法加载模型目录']])(
    '为%s响应显示固定安全状态', async (status, message) => {
      vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status })))
      renderWithProviders(<ModelListPage />, 'auditor')
      expect(await screen.findByText(message)).toBeInTheDocument()
      expect(screen.queryByRole('region', { name: '模型治理态势' })).not.toBeInTheDocument()
      expect(screen.queryByRole('table', { name: '受治理模型台账' })).not.toBeInTheDocument()
      expect(screen.queryByRole('navigation', { name: '模型分页' })).not.toBeInTheDocument()
    },
  )

  it.each([[403, '无权查看模型目录'], [500, '暂时无法加载模型目录']])(
    '缓存模型在重取%s失败后只显示独立安全状态', async (status, message) => {
      const fetchMock = vi.fn()
        .mockResolvedValueOnce(jsonResponse({ items: governancePageItems, total: 45, page: 2, page_size: 20 }))
        .mockResolvedValueOnce(new Response(null, { status }))
      vi.stubGlobal('fetch', fetchMock)
      const { queryClient } = renderWithProviders(<ModelListPage />, 'user', '/models?page=2')

      expect(await screen.findByRole('table', { name: '受治理模型台账' })).toBeInTheDocument()
      await act(async () => {
        await queryClient.invalidateQueries({ queryKey: ['models'] })
      })

      expect(await screen.findByText(message)).toBeInTheDocument()
      expect(fetchMock).toHaveBeenCalledTimes(2)
      expect(screen.queryByRole('region', { name: '模型治理态势' })).not.toBeInTheDocument()
      expect(screen.queryByRole('table', { name: '受治理模型台账' })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: '编辑 可配置私有模型' })).not.toBeInTheDocument()
      expect(screen.queryByText(/匹配模型\s*45/)).not.toBeInTheDocument()
      expect(screen.queryByText('共 45 条，第 2 页')).not.toBeInTheDocument()
      expect(screen.queryByRole('navigation', { name: '模型分页' })).not.toBeInTheDocument()
    },
  )

  it('加载模型目录时不提前渲染治理态势、台账或分页', async () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))
    const view = renderWithProviders(<ModelListPage />, 'auditor')

    expect(await screen.findByText('正在加载模型目录')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: '模型治理态势' })).not.toBeInTheDocument()
    expect(screen.queryByRole('table', { name: '受治理模型台账' })).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '模型分页' })).not.toBeInTheDocument()
    view.unmount()
  })

  it('从编辑A切到B会取消旧写请求并清空A的字段与Token', async () => {
    let resolveOldWrite!: (response: Response) => void
    let oldSignal: AbortSignal | undefined
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({
        items: [
          catalogItem({ id: 'model-a', name: '模型A' }),
          catalogItem({ id: 'model-b', name: '模型B', provider_model: 'provider-b' }),
        ],
        total: 2,
        page: 1,
        page_size: 20,
      }))
      .mockImplementationOnce((_input: RequestInfo | URL, init?: RequestInit) => {
        oldSignal = init?.signal ?? undefined
        return new Promise<Response>((resolve) => { resolveOldWrite = resolve })
      })
    vi.stubGlobal('fetch', fetchMock)
    renderWithProviders(<ModelListPage />)

    fireEvent.click(await screen.findByRole('button', { name: '编辑 模型A' }))
    fireEvent.change(screen.getByLabelText('新 Token（留空保持不变）'), { target: { value: 'model-a-secret' } })
    fireEvent.click(screen.getByRole('button', { name: '保存模型' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))

    fireEvent.click(screen.getByRole('button', { name: '编辑 模型B' }))
    expect(oldSignal?.aborted).toBe(true)
    expect(screen.getByLabelText(/^模型名称/)).toHaveValue('模型B')
    expect(screen.getByLabelText(/^供应商模型/)).toHaveValue('provider-b')
    expect(screen.getByLabelText('新 Token（留空保持不变）')).toHaveValue('')

    resolveOldWrite(jsonResponse(catalogItem({ id: 'model-a', source: undefined, read_only: undefined })))
    await Promise.resolve()
    expect(screen.getByRole('form', { name: '编辑模型' })).toBeInTheDocument()
    expect(screen.getByLabelText(/^模型名称/)).toHaveValue('模型B')
  })

  it('删除204后不让悬挂的目录刷新占住下一次删除锁', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({
        items: [
          catalogItem({ id: 'model-a', name: '模型A' }),
          catalogItem({ id: 'model-b', name: '模型B' }),
        ],
        total: 2,
        page: 1,
        page_size: 20,
      }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockImplementationOnce(() => new Promise<Response>(() => undefined))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    renderWithProviders(<ModelListPage />)

    fireEvent.click(await screen.findByRole('button', { name: '删除 模型A' }))
    fireEvent.click(screen.getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3))

    fireEvent.click(screen.getByRole('button', { name: '删除 模型B' }))
    const secondConfirm = screen.getByRole('button', { name: '确认删除' })
    expect(secondConfirm).toBeEnabled()
    fireEvent.click(secondConfirm)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4))
  })
})

describe('ModelForm', () => {
  it('创建失败不重放，保留非敏感字段但立即清空Token', async () => {
    const secret = 'form-secret-sentinel'
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 500 }))
    vi.stubGlobal('fetch', fetchMock)
    renderWithProviders(<ModelForm role="user" onSaved={vi.fn()} onCancel={vi.fn()} />)

    fireEvent.change(screen.getByLabelText(/^模型名称/), { target: { value: '我的私有模型' } })
    fireEvent.change(screen.getByLabelText(/^供应商模型/), { target: { value: 'gpt-secure' } })
    fireEvent.change(screen.getByLabelText(/^基础 URL/), { target: { value: 'https://models.invalid/v1' } })
    fireEvent.change(screen.getByLabelText(/^访问 Token/), { target: { value: secret } })
    fireEvent.click(screen.getByRole('button', { name: '创建私有模型' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('模型保存失败，请重新输入 Token 后重试。')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(screen.getByLabelText(/^访问 Token/)).toHaveValue('')
    expect(screen.getByLabelText(/^模型名称/)).toHaveValue('我的私有模型')
    expect(document.body).not.toHaveTextContent(secret)
  })

  it('编辑永不回填Token，未输入新值时请求体不含掩码或token字段', async () => {
    const model = catalogItem() as ModelCatalogItem
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(catalogItem({ source: undefined, read_only: undefined })))
    vi.stubGlobal('fetch', fetchMock)
    renderWithProviders(<ModelForm role="user" model={model} onSaved={vi.fn()} onCancel={vi.fn()} />)

    expect(screen.getByLabelText('新 Token（留空保持不变）')).toHaveValue('')
    fireEvent.click(screen.getByRole('button', { name: '保存模型' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    const body = String((fetchMock.mock.calls[0]?.[1] as RequestInit).body)
    expect(JSON.parse(body)).not.toHaveProperty('token')
    expect(body).not.toContain('********')
  })

  it('双击提交仅发一次写请求，卸载会取消未完成请求', async () => {
    let signal: AbortSignal | undefined
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      signal = init?.signal ?? undefined
      return new Promise<Response>(() => undefined)
    })
    vi.stubGlobal('fetch', fetchMock)
    const view = renderWithProviders(<ModelForm role="user" onSaved={vi.fn()} onCancel={vi.fn()} />)
    fireEvent.change(screen.getByLabelText(/^模型名称/), { target: { value: '私有模型' } })
    fireEvent.change(screen.getByLabelText(/^供应商模型/), { target: { value: 'gpt-secure' } })
    fireEvent.change(screen.getByLabelText(/^基础 URL/), { target: { value: 'https://models.invalid/v1' } })
    fireEvent.change(screen.getByLabelText(/^访问 Token/), { target: { value: 'one-use-secret' } })
    const submit = screen.getByRole('button', { name: '创建私有模型' })

    fireEvent.click(submit)
    fireEvent.click(submit)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    view.unmount()
    expect(signal?.aborted).toBe(true)
  })

  it('从启用改为停用必须二次确认，取消确认不发送PUT', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(catalogItem({ source: undefined, read_only: undefined })))
    vi.stubGlobal('fetch', fetchMock)
    renderWithProviders(
      <ModelForm role="user" model={catalogItem({ disabled: false }) as ModelCatalogItem} onSaved={vi.fn()} onCancel={vi.fn()} />,
    )

    fireEvent.click(screen.getByRole('switch', { name: '停用模型' }))
    fireEvent.click(screen.getByRole('button', { name: '保存模型' }))
    expect(await screen.findByRole('dialog', { name: '确认停用模型' })).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '取消停用' }))
    expect(fetchMock).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: '保存模型' }))
    fireEvent.click(await screen.findByRole('button', { name: '确认停用' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(JSON.parse(String((fetchMock.mock.calls[0]?.[1] as RequestInit).body))).toMatchObject({ disabled: true })
  })

  it('从停用恢复启用无需危险确认', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(catalogItem({ disabled: false, source: undefined, read_only: undefined })))
    vi.stubGlobal('fetch', fetchMock)
    renderWithProviders(
      <ModelForm role="user" model={catalogItem({ disabled: true }) as ModelCatalogItem} onSaved={vi.fn()} onCancel={vi.fn()} />,
    )

    fireEvent.click(screen.getByRole('switch', { name: '停用模型' }))
    fireEvent.click(screen.getByRole('button', { name: '保存模型' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('dialog', { name: '确认停用模型' })).not.toBeInTheDocument()
  })
})

describe('RotateCredentialDialog', () => {
  it('准确说明主密钥重加密，不提供Token输入并只发一次无body POST', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    const onCompleted = vi.fn()
    renderWithProviders(
      <RotateCredentialDialog
        open
        model={catalogItem({ owner_user_id: '', scope: 'global', name: '全局模型' }) as ModelCatalogItem}
        onClose={vi.fn()}
        onCompleted={onCompleted}
      />,
      'admin',
    )

    expect(screen.getByText(/使用当前环境主密钥重新加密/)).toBeInTheDocument()
    expect(screen.queryByLabelText(/Token/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '确认轮换加密' }))

    await waitFor(() => expect(onCompleted).toHaveBeenCalledTimes(1))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).method).toBe('POST')
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).body).toBeUndefined()
  })
})
