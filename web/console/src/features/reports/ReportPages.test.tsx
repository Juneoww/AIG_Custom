/**
 * 功能：验证不可变报告列表、详情、角色状态与受审计 PDF 交互。
 * 实现：在真实 Query、主题和内存路由中驱动安全 JSON 与二进制网络响应。
 * 输入：分页深链、不可变 RenderModel、403/404/500 与 PDF 流。
 * 输出：原生台账、完整快照区域、固定下载名和显式恢复操作。
 * 依赖：Testing Library、TanStack Query、React Router、SessionProvider 与 Fluent UI。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ThemeProvider } from '../../shared/theme/ThemeProvider'
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

function renderAt(page: React.ReactNode, path: string, routePath: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <ThemeProvider initialMode="light">
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={[path]}>
          <Routes><Route path={routePath} element={page} /></Routes>
        </MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
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
  it('从可分享URL读取服务端分页并呈现原生报告台账', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({
      items: [{ ...detail(), brand_product_name: '历史快照品牌' }], total: 45, page: 2, page_size: 20,
    }))
    vi.stubGlobal('fetch', fetchMock)
    renderAt(<ReportListPage />, '/reports?page=2', '/reports')

    const table = await screen.findByRole('table', { name: '不可变安全报告台账' })
    expect(table.tagName).toBe('TABLE')
    expect(screen.getByRole('columnheader', { name: '风险分布' }).tagName).toBe('TH')
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/reports?page=2&page_size=20')
    expect(screen.getByRole('link', { name: '查看报告 report-opaque-1' })).toHaveAttribute('href', '/reports/report-opaque-1')
    expect(screen.getByText('共 45 条，第 2 页')).toBeInTheDocument()
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
})
