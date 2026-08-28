/**
 * 功能：验证不可变报告列表、详情、角色状态与受审计 PDF 交互。
 * 实现：在真实 Query、主题和内存路由中驱动安全 JSON 与二进制网络响应。
 * 输入：分页深链、不可变 RenderModel、403/404/500 与 PDF 流。
 * 输出：原生台账、完整快照区域、固定下载名和显式恢复操作。
 * 依赖：Testing Library、TanStack Query、React Router、SessionProvider 与 Fluent UI。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import type { ReportSummaryView } from './api'
import { ReportDetailPage } from './ReportDetailPage'
import { ReportListPage } from './ReportListPage'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function risk() {
  return { mapping_version: 'risk-v2', high: 2, medium: 1, low: 3, score: 72 }
}

function trend() {
  return Array.from({ length: 30 }, (_, index) => ({
    date: new Date(Date.UTC(2026, 6, 20 + index)).toISOString(), completed: 1, high: index % 2, medium: 1, low: 2,
  }))
}

function detail(overrides: Record<string, unknown> = {}) {
  return {
    id: 'report-opaque-1', task_id: 'task-opaque-1', task_type: 'mcp_scan',
    completed_at: '2026-08-18T01:00:00Z', created_at: '2026-08-18T01:01:00Z', risk: risk(),
    render: {
      render_version: 'report-render-v2', mapping_version: 'risk-v2', generated_at: '2026-08-18T01:01:00Z',
      completed_at: '2026-08-18T01:00:00Z', task_id: 'task-opaque-1', task_type: 'mcp_scan',
      product_name: '历史快照品牌', primary_color: '#2457a7', watermark: '历史水印', risk: risk(),
      score_explanation: '使用生成时固化的 risk-v2 映射。', risk_trend: trend(),
      risk_distribution: { high: 2, medium: 1, low: 3 },
      top_risks: [
        { severity: 'low', count: 3, impact: '低风险影响', remediation: '持续观察' },
        { severity: 'high', count: 2, impact: '高风险影响', remediation: '立即修复' },
        { severity: 'medium', count: 1, impact: '中风险影响', remediation: '计划修复' },
      ],
      technical_findings: [{
        title: '超长中文技术发现'.repeat(20), evidence: '可信证据', impact: '业务影响', remediation: '修复措施',
      }],
      recommendations: ['优先修复高风险项'], coverage: '覆盖 1/1 条可信发现', conclusion: '完成修复后复核。',
    },
    ...overrides,
  }
}

const reviewReports: readonly ReportSummaryView[] = [
  {
    id: 'report-high-a',
    task_id: 'task-high-a',
    task_type: 'mcp_scan',
    completed_at: '2026-08-18T01:00:00Z',
    created_at: '2026-08-18T01:01:00Z',
    risk: { mapping_version: 'risk-v2', high: 2, medium: 1, low: 3, score: 72 },
    brand_product_name: '历史快照品牌',
  },
  {
    id: 'report-low-b',
    task_id: 'task-low-b',
    task_type: 'ai_infra_scan',
    completed_at: '2026-08-19T01:00:00Z',
    created_at: '2026-08-19T01:01:00Z',
    risk: { mapping_version: 'risk-v2', high: 0, medium: 2, low: 4, score: 48 },
    brand_product_name: '历史快照品牌',
  },
  {
    id: 'report-high-c',
    task_id: 'task-high-c',
    task_type: 'agent_scan',
    completed_at: '2026-08-20T01:00:00Z',
    created_at: '2026-08-20T01:01:00Z',
    risk: { mapping_version: 'risk-v2', high: 1, medium: 0, low: 1, score: 65 },
    brand_product_name: '历史快照品牌',
  },
]

function renderAt(page: React.ReactNode, path: string, routePath: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = render(
    <ThemeProvider initialMode="light">
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={[path]}>
          <Routes><Route path={routePath} element={page} /></Routes>
        </MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  )
  return { ...view, queryClient }
}

function ReportSwitchHarness() {
  const navigate = useNavigate()
  return (
    <>
      <button type="button" onClick={() => navigate('/reports/report-B')}>切换报告</button>
      <Routes><Route path="/reports/:reportId" element={<ReportDetailPage />} /></Routes>
    </>
  )
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('ReportListPage', () => {
  it('从可分享URL呈现报告复核态势与原生报告台账', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({
      items: reviewReports, total: 45, page: 2, page_size: 20,
    }))
    vi.stubGlobal('fetch', fetchMock)
    renderAt(<ReportListPage />, '/reports?page=2', '/reports')

    const summary = await screen.findByRole('region', { name: '报告复核态势' })
    expect(summary).toHaveTextContent('当前查询')
    expect(summary).toHaveTextContent('全部报告')
    expect(summary).toHaveTextContent('匹配报告 45')
    expect(summary).toHaveTextContent('本页需优先复核 2')
    expect(summary).toHaveTextContent('本页高风险发现 3')
    expect(summary).not.toHaveTextContent('本页高风险发现 45')
    const table = await screen.findByRole('table', { name: '不可变安全报告台账' })
    expect(table.tagName).toBe('TABLE')
    expect(screen.getByRole('columnheader', { name: '风险分布' }).tagName).toBe('TH')
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/reports?page=2&page_size=20')
    expect(screen.getByRole('link', { name: '查看报告 report-high-a' })).toHaveAttribute('href', '/reports/report-high-a')
    expect(table).toHaveTextContent('高 2 / 中 1 / 低 3')
    expect(screen.getByText('共 45 条，第 2 页')).toBeInTheDocument()
  })

  it('空报告库仍展示当前查询但不渲染本页复核信号', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 0, page: 1, page_size: 20 })))
    renderAt(<ReportListPage />, '/reports', '/reports')

    const summary = await screen.findByRole('region', { name: '报告复核态势' })
    expect(summary).toHaveTextContent('当前查询')
    expect(summary).toHaveTextContent('全部报告')
    expect(summary).toHaveTextContent('匹配报告 0')
    expect(await screen.findByText('暂无安全报告')).toBeInTheDocument()
    expect(summary).not.toHaveTextContent('本页需优先复核 0')
    expect(summary).not.toHaveTextContent('本页高风险发现 0')
    expect(screen.queryByRole('group', { name: '本页复核信号' })).not.toBeInTheDocument()
  })

  it('有查询结果但当前页为空时保留总量与可用分页', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 45, page: 3, page_size: 20 })))
    renderAt(<ReportListPage />, '/reports?page=3', '/reports')

    expect(await screen.findByText('当前页没有报告')).toBeInTheDocument()
    expect(screen.queryByText('暂无安全报告')).not.toBeInTheDocument()
    const summary = await screen.findByRole('region', { name: '报告复核态势' })
    expect(summary).toHaveTextContent('当前查询')
    expect(summary).toHaveTextContent('全部报告')
    expect(summary).toHaveTextContent('匹配报告 45')
    expect(summary).not.toHaveTextContent('本页需优先复核 0')
    expect(summary).not.toHaveTextContent('本页高风险发现 0')
    expect(screen.getByText('共 45 条，第 3 页')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上一页' })).toBeEnabled()
  })

  it('恶意分页参数规范化到第一页且不做当前页假筛选', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 0, page: 1, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)
    renderAt(<ReportListPage />, '/reports?page=1001&owner=other', '/reports')

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/reports?page=1&page_size=20')
    expect(await screen.findByText('暂无安全报告')).toBeInTheDocument()
  })

  it.each([
    [403, '无权查看安全报告'],
    [500, '暂时无法加载安全报告'],
  ])('为%s响应显示独立安全状态', async (status, message) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status })))
    renderAt(<ReportListPage />, '/reports', '/reports')
    expect(await screen.findByText(message)).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: '报告复核态势' })).not.toBeInTheDocument()
  })

  it.each([
    [403, '无权查看安全报告'],
    [500, '暂时无法加载安全报告'],
  ])('缓存报告在重取%s失败后只显示独立安全状态', async (status, message) => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ items: reviewReports, total: 45, page: 2, page_size: 20 }))
      .mockResolvedValueOnce(new Response(null, { status }))
    vi.stubGlobal('fetch', fetchMock)
    const { queryClient } = renderAt(<ReportListPage />, '/reports?page=2', '/reports')

    expect(await screen.findByRole('table', { name: '不可变安全报告台账' })).toBeInTheDocument()
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['reports', { page: 2, pageSize: 20 }], exact: true })
    })

    expect(await screen.findByText(message)).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(screen.queryByRole('region', { name: '报告复核态势' })).not.toBeInTheDocument()
    expect(screen.queryByRole('table', { name: '不可变安全报告台账' })).not.toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '报告分页' })).not.toBeInTheDocument()
  })

  it('加载安全报告时不提前渲染报告复核态势', async () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))
    const view = renderAt(<ReportListPage />, '/reports', '/reports')

    expect(await screen.findByText('正在加载安全报告')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: '报告复核态势' })).not.toBeInTheDocument()
    view.unmount()
  })
})

describe('ReportDetailPage', () => {
  it('只呈现同一不可变快照并按高、中、低稳定排序', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(detail())))
    renderAt(<ReportDetailPage />, '/reports/report-opaque-1', '/reports/:reportId')

    expect(await screen.findByRole('heading', { level: 1, name: '安全报告详情' })).toBeInTheDocument()
    expect(screen.getByText('历史快照品牌')).toBeInTheDocument()
    expect(screen.getByText('历史水印')).toBeInTheDocument()
    expect(screen.getByText('安全评分')).toBeInTheDocument()
    expect(screen.getByText('72')).toBeInTheDocument()
    expect(screen.getByText('risk-v2')).toBeInTheDocument()
    expect(screen.getByText('使用生成时固化的 risk-v2 映射。')).toBeInTheDocument()
    const top = screen.getByRole('region', { name: '重点风险' })
    const orderedText = top.textContent ?? ''
    expect(orderedText.indexOf('高风险')).toBeLessThan(orderedText.indexOf('中风险'))
    expect(orderedText.indexOf('中风险')).toBeLessThan(orderedText.indexOf('低风险'))
    expect(screen.getByRole('region', { name: '技术发现' })).toHaveTextContent('可信证据')
    expect(screen.getByRole('region', { name: '覆盖与结论' })).toHaveTextContent('覆盖 1/1 条可信发现')
    expect(screen.getByRole('table', { name: '不可变风险趋势' })).toBeInTheDocument()
    expect(document.body).not.toHaveTextContent('当前品牌')
  })

  it.each([
    [403, '无权查看此安全报告'],
    [404, '安全报告不存在'],
    [500, '暂时无法加载安全报告'],
  ])('为详情%s响应显示独立状态', async (status, message) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status })))
    renderAt(<ReportDetailPage />, '/reports/report-opaque-1', '/reports/:reportId')
    expect(await screen.findByText(message)).toBeInTheDocument()
  })

  it('PDF失败不自动重放，显式重试后以固定文件名下载并延迟释放URL', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse(detail()))
      .mockResolvedValueOnce(new Response(null, { status: 500 }))
      .mockResolvedValueOnce(new Response(new Uint8Array([0x25, 0x50, 0x44, 0x46]), { status: 200, headers: { 'Content-Type': 'application/pdf', 'Content-Disposition': 'attachment; filename=../../bad.pdf' } }))
    vi.stubGlobal('fetch', fetchMock)
    const createObjectURL = vi.fn().mockReturnValue('blob:safe-report')
    const revokeObjectURL = vi.fn()
    const NativeURL = URL
    class TestURL extends NativeURL {
      static createObjectURL = createObjectURL
      static revokeObjectURL = revokeObjectURL
    }
    vi.stubGlobal('URL', TestURL)
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    renderAt(<ReportDetailPage />, '/reports/report-opaque-1', '/reports/:reportId')

    fireEvent.click(await screen.findByRole('button', { name: '导出 PDF' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('PDF 导出失败，请重试。')
    expect(fetchMock).toHaveBeenCalledTimes(2)
    vi.useFakeTimers()
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '重新导出 PDF' }))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(click).toHaveBeenCalledTimes(1)
    expect((click.mock.contexts[0] as unknown as HTMLAnchorElement).download).toBe('安全报告.pdf')
    expect(revokeObjectURL).not.toHaveBeenCalled()
    await act(async () => { await vi.runAllTimersAsync() })
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:safe-report')
  })

  it('卸载会取消仍在等待的PDF请求', async () => {
    let exportSignal: AbortSignal | undefined
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') {
        exportSignal = init.signal ?? undefined
        return new Promise<Response>(() => undefined)
      }
      return Promise.resolve(jsonResponse(detail()))
    })
    vi.stubGlobal('fetch', fetchMock)
    const view = renderAt(<ReportDetailPage />, '/reports/report-opaque-1', '/reports/:reportId')
    fireEvent.click(await screen.findByRole('button', { name: '导出 PDF' }))
    await waitFor(() => expect(exportSignal).toBeInstanceOf(AbortSignal))

    view.unmount()
    expect(exportSignal?.aborted).toBe(true)
  })

  it('即使网络忽略取消并在卸载后完成也不创建下载URL', async () => {
    let resolveExport: ((response: Response) => void) | undefined
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === 'POST') return new Promise<Response>((resolve) => { resolveExport = resolve })
      return Promise.resolve(jsonResponse(detail()))
    })
    vi.stubGlobal('fetch', fetchMock)
    const createObjectURL = vi.fn().mockReturnValue('blob:late-report')
    const NativeURL = URL
    class TestURL extends NativeURL {
      static createObjectURL = createObjectURL
      static revokeObjectURL = vi.fn()
    }
    vi.stubGlobal('URL', TestURL)
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    const view = renderAt(<ReportDetailPage />, '/reports/report-opaque-1', '/reports/:reportId')
    fireEvent.click(await screen.findByRole('button', { name: '导出 PDF' }))
    await waitFor(() => expect(resolveExport).toBeTypeOf('function'))

    view.unmount()
    await act(async () => {
      resolveExport?.(new Response(new Uint8Array([0x25, 0x50, 0x44, 0x46]), {
        status: 200, headers: { 'Content-Type': 'application/pdf' },
      }))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(createObjectURL).not.toHaveBeenCalled()
    expect(click).not.toHaveBeenCalled()
  })

  it('同组件切换报告时隔离旧路由导出且新报告仍可正常导出', async () => {
    let resolveA: ((response: Response) => void) | undefined
    let signalA: AbortSignal | undefined
    const pdf = () => new Response(new Uint8Array([0x25, 0x50, 0x44, 0x46]), {
      status: 200, headers: { 'Content-Type': 'application/pdf' },
    })
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (init?.method === 'POST' && url.includes('/report-A/')) {
        signalA = init.signal ?? undefined
        return new Promise<Response>((resolve) => { resolveA = resolve })
      }
      if (init?.method === 'POST' && url.includes('/report-B/')) return Promise.resolve(pdf())
      if (url.endsWith('/report-B')) return Promise.resolve(jsonResponse(detail({ id: 'report-B' })))
      return Promise.resolve(jsonResponse(detail({ id: 'report-A' })))
    })
    vi.stubGlobal('fetch', fetchMock)
    const createObjectURL = vi.fn().mockReturnValue('blob:current-report')
    const revokeObjectURL = vi.fn()
    const NativeURL = URL
    class TestURL extends NativeURL {
      static createObjectURL = createObjectURL
      static revokeObjectURL = revokeObjectURL
    }
    vi.stubGlobal('URL', TestURL)
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    renderAt(<ReportSwitchHarness />, '/reports/report-A', '*')

    fireEvent.click(await screen.findByRole('button', { name: '导出 PDF' }))
    await waitFor(() => expect(resolveA).toBeTypeOf('function'))
    fireEvent.click(screen.getByRole('button', { name: '切换报告' }))
    expect(await screen.findByText('report-B')).toBeInTheDocument()
    expect.soft(signalA?.aborted).toBe(true)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    const exportB = screen.getByRole('button', { name: /导出/ })
    expect(exportB).toBeEnabled()
    fireEvent.click(exportB)
    await waitFor(() => expect(click).toHaveBeenCalledTimes(1))

    await act(async () => {
      resolveA?.(pdf())
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(createObjectURL).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(revokeObjectURL).toHaveBeenCalledWith('blob:current-report'))
  })
})
