/**
 * 功能：验证不可变报告 DTO 与 PDF 下载不会接纳敏感或越界响应。
 * 实现：驱动真实共享客户端并检查白名单投影、POST/CSRF、状态、MIME 与大小边界。
 * 输入：包含额外敏感字段、畸形字段及受控 PDF 流的服务端响应。
 * 输出：安全报告视图、固定二进制 Blob 或泛化 ApiError。
 * 依赖：Vitest、浏览器 Response/ReadableStream 与报告 API。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/errors'
import { subscribeToUnauthorized } from '../../shared/api/client'
import { exportReportPDF, fetchReportDetail, parseReportDetail, parseReportList } from './api'

const SENTINELS = ['RAW-SENTINEL', 'RENDER-DATA-SENTINEL', 'LOGO-SENTINEL', 'PATH-SENTINEL', 'TOKEN-SENTINEL']

function risk() {
  return { mapping_version: 'risk-v2', high: 2, medium: 1, low: 3, score: 72 }
}

function trend() {
  return Array.from({ length: 30 }, (_, index) => ({
    date: new Date(Date.UTC(2026, 6, 20 + index)).toISOString(),
    completed: 1,
    high: index % 2,
    medium: 1,
    low: 2,
  }))
}

function reportDetail(overrides: Record<string, unknown> = {}) {
  return {
    id: 'report-opaque-1',
    task_id: 'task-opaque-1',
    task_type: 'mcp_scan',
    completed_at: '2026-08-18T01:00:00Z',
    created_at: '2026-08-18T01:01:00Z',
    risk: risk(),
    render: {
      render_version: 'report-render-v2',
      mapping_version: 'risk-v2',
      generated_at: '2026-08-18T01:01:00Z',
      completed_at: '2026-08-18T01:00:00Z',
      task_id: 'task-opaque-1',
      task_type: 'mcp_scan',
      product_name: 'AI 安全治理平台',
      primary_color: '#2457a7',
      watermark: '内部资料',
      risk: risk(),
      score_explanation: '按不可变风险映射计算。',
      risk_trend: trend(),
      risk_distribution: { high: 2, medium: 1, low: 3 },
      top_risks: [
        { severity: 'low', count: 3, impact: '低风险影响', remediation: '持续观察' },
        { severity: 'high', count: 2, impact: '高风险影响', remediation: '立即修复' },
        { severity: 'medium', count: 1, impact: '中风险影响', remediation: '计划修复' },
      ],
      technical_findings: [
        { title: '长中文技术发现', evidence: '证据说明', impact: '影响说明', remediation: '修复建议' },
      ],
      recommendations: ['优先修复高风险项'],
      coverage: '已覆盖可信引擎输出。',
      conclusion: '需要完成修复与复核。',
    },
    raw_result: SENTINELS[0],
    render_data: SENTINELS[1],
    logo_data_url: SENTINELS[2],
    path: SENTINELS[3],
    token: SENTINELS[4],
    ...overrides,
  }
}

afterEach(() => {
  document.cookie = 'aig_csrf=; Max-Age=0; Path=/'
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('报告安全 DTO', () => {
  it('仅投影安全列表字段且不保留响应中的敏感哨兵', () => {
    const parsed = parseReportList({
      items: [{ ...reportDetail(), brand_product_name: '快照品牌' }],
      total: 1,
      page: 1,
      page_size: 20,
      raw_result: SENTINELS[0],
    })

    expect(parsed.items).toHaveLength(1)
    const serialized = JSON.stringify(parsed)
    for (const sentinel of SENTINELS) expect(serialized).not.toContain(sentinel)
    expect(parsed.items[0]).toEqual({
      id: 'report-opaque-1',
      task_id: 'task-opaque-1',
      task_type: 'mcp_scan',
      completed_at: '2026-08-18T01:00:00Z',
      created_at: '2026-08-18T01:01:00Z',
      risk: risk(),
      brand_product_name: '快照品牌',
    })
  })

  it('详情缓存对象只包含已校验的不可变 RenderModel', async () => {
    const log = vi.spyOn(console, 'log').mockImplementation(() => undefined)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify(reportDetail()), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })))

    const parsed = await fetchReportDetail('report-opaque-1')

    const serialized = JSON.stringify(parsed)
    for (const sentinel of SENTINELS) expect(serialized).not.toContain(sentinel)
    expect(log).not.toHaveBeenCalled()
    expect(parsed.render.risk_trend).toHaveLength(30)
  })

  it.each([
    ['空对象', {}],
    ['错误版本', reportDetail({ render: { ...(reportDetail().render as object), render_version: 'future-unsafe' } })],
    ['越界分页', { items: [], total: 0, page: 1001, page_size: 20 }],
    ['不一致任务', reportDetail({ task_id: 'task-other' })],
    ['不完整趋势', reportDetail({ render: { ...(reportDetail().render as object), risk_trend: trend().slice(1) } })],
  ])('拒绝%s的200响应', (_label, value) => {
    expect(() => (_label === '越界分页' ? parseReportList(value) : parseReportDetail(value))).toThrow(ApiError)
  })

  it('按同一时刻比较数据库时间与RenderModel时间表示', () => {
    const value = reportDetail({
      completed_at: '2026-08-18T09:00:00+08:00',
      created_at: '2026-08-18T09:01:00+08:00',
    })
    value.render = {
      ...value.render,
      completed_at: '2026-08-18T01:00:00.000000999Z',
      generated_at: '2026-08-18T01:01:00.000000999Z',
    }

    expect(parseReportDetail(value)).toMatchObject({ id: 'report-opaque-1' })
  })

  it('仍拒绝数据库微秒精度下不同的快照时间', () => {
    const value = reportDetail({ completed_at: '2026-08-18T01:00:00.000001Z' })
    value.render = { ...value.render, completed_at: '2026-08-18T01:00:00.000002Z' }

    expect(() => parseReportDetail(value)).toThrow(ApiError)
  })

  it('拒绝会被URL折叠的点段报告ID且零请求', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    expect(() => parseReportDetail(reportDetail({ id: '.' }))).toThrow(ApiError)
    expect(() => parseReportList({
      items: [{ ...reportDetail(), id: '..', brand_product_name: '快照品牌' }], total: 1, page: 1, page_size: 20,
    })).toThrow(ApiError)
    await expect(exportReportPDF('..')).rejects.toMatchObject({ kind: 'bad-request' })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('不可变 PDF 导出', () => {
  it('使用POST、最新CSRF、精确200和application/pdf，并忽略服务端文件名', async () => {
    document.cookie = 'aig_csrf=fresh-csrf; Path=/'
    const fetchMock = vi.fn().mockResolvedValue(new Response(new Uint8Array([0x25, 0x50, 0x44, 0x46]), {
      status: 200,
      headers: {
        'Content-Type': 'application/pdf',
        'Content-Disposition': 'attachment; filename="../../TOKEN-SENTINEL.pdf"',
      },
    }))
    vi.stubGlobal('fetch', fetchMock)

    const blob = await exportReportPDF('report-opaque-1')

    expect(blob.type).toBe('application/pdf')
    expect(blob.size).toBe(4)
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit
    expect(init.method).toBe('POST')
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('fresh-csrf')
  })

  it.each([
    ['状态错误', new Response(new Uint8Array([1]), { status: 201, headers: { 'Content-Type': 'application/pdf' } })],
    ['MIME错误', new Response(new Uint8Array([1]), { status: 200, headers: { 'Content-Type': 'application/octet-stream' } })],
    ['声明超限', new Response(new Uint8Array([1]), { status: 200, headers: { 'Content-Type': 'application/pdf', 'Content-Length': String(50 * 1024 * 1024 + 1) } })],
  ])('拒绝%s的PDF响应', async (_label, response) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    await expect(exportReportPDF('report-opaque-1')).rejects.toBeInstanceOf(ApiError)
  })

  it('允许50MiB合同内的声明长度并把AbortSignal传给fetch', async () => {
    const controller = new AbortController()
    const fetchMock = vi.fn().mockResolvedValue(new Response(new Uint8Array([1]), {
      status: 200,
      headers: { 'Content-Type': 'application/pdf', 'Content-Length': String(20 * 1024 * 1024 + 1) },
    }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(exportReportPDF('report-opaque-1', controller.signal)).resolves.toMatchObject({ size: 1 })
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).signal).toBe(controller.signal)
  })

  it('PDF的401沿用受保护请求会话失效语义且不读取错误正文', async () => {
    const unauthorized = vi.fn()
    const unsubscribe = subscribeToUnauthorized(unauthorized)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('TOKEN-SENTINEL', { status: 401 })))

    await expect(exportReportPDF('report-opaque-1')).rejects.toMatchObject({ kind: 'unauthenticated' })
    expect(unauthorized).toHaveBeenCalledTimes(1)
    unsubscribe()
  })
})
