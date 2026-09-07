/**
 * 功能：锁定 MCP 工作台浏览器 DTO 的白名单投影与响应边界。
 * 实现：以未知响应夹具验证固定字段、数组上限、枚举、时间及摘要长度。
 * 输入：MCP 工作台 API 返回的未知 JSON。
 * 输出：安全 DTO 或不携带原文的固定 ApiError。
 * 依赖：Vitest、共享 API 错误类型与 MCP 工作台客户端。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/errors'
import { fetchMCPWorkbench, parseMCPWorkbenchView } from './mcpWorkbenchApi'

const activeTask = {
  task_id: 'task-opaque-1',
  label: 'MCP 扫描 · task-opa',
  source_kind: 'service',
  phase: null,
  status: 'running',
  updated_at: '2026-09-02T01:00:00Z',
} as const

const recentRisk = {
  report_id: 'report-opaque-1',
  task_id: 'task-opaque-1',
  severity: 'high',
  category: 'dangerous_tool',
  summary: '检测到高风险 MCP 工具行为。',
  completed_at: '2026-09-02T01:00:00Z',
} as const

function workbenchView(overrides: Record<string, unknown> = {}) {
  return {
    metrics: { running: 1, pending: 2, high_risk: 3, completed_30d: 4 },
    active_tasks: [activeTask],
    recent_risks: [recentRisk],
    ...overrides,
  }
}

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('MCP 工作台安全响应合同', () => {
  it('只投影固定安全字段，忽略服务端附带的敏感字段', () => {
    const result = parseMCPWorkbenchView(workbenchView({
      content: 'https://private.example/repository.git',
      endpoint: 'https://private.example/mcp',
      raw_result: 'scanner logs',
      model_id: 'secret-model-id',
      headers: { authorization: 'Bearer secret' },
      active_tasks: [{ ...activeTask, endpoint: 'https://private.example/mcp', params: { token: 'secret' } }],
      recent_risks: [{ ...recentRisk, raw_result: 'scanner logs', model_id: 'secret-model-id' }],
    }))

    expect(result).toEqual({
      metrics: { running: 1, pending: 2, high_risk: 3, completed_30d: 4 },
      active_tasks: [activeTask],
      recent_risks: [recentRisk],
    })
    expect(JSON.stringify(result)).not.toMatch(/private\.example|raw_result|secret-model-id|authorization|token/)
  })

  it.each([
    ['超过活跃任务上限', workbenchView({ active_tasks: Array.from({ length: 11 }, () => activeTask) })],
    ['超过风险摘要上限', workbenchView({ recent_risks: Array.from({ length: 6 }, () => recentRisk) })],
    ['未知来源枚举', workbenchView({ active_tasks: [{ ...activeTask, source_kind: 'endpoint' }] })],
    ['未知风险类别', workbenchView({ recent_risks: [{ ...recentRisk, category: 'scanner_title' }] })],
    ['未受信任的阶段文本', workbenchView({ active_tasks: [{ ...activeTask, phase: 'https://private.example/mcp?log=PHASE-SENTINEL' }] })],
    ['非法时间戳', workbenchView({ active_tasks: [{ ...activeTask, updated_at: 'not-a-date' }] })],
    ['溢出日历日期', workbenchView({ active_tasks: [{ ...activeTask, updated_at: '2026-02-31T01:00:00Z' }] })],
    ['过长风险摘要', workbenchView({ recent_risks: [{ ...recentRisk, summary: '高'.repeat(161) }] })],
    ['负数指标', workbenchView({ metrics: { running: -1, pending: 2, high_risk: 3, completed_30d: 4 } })],
  ])('拒绝%s', (_label, payload) => {
    expect(() => parseMCPWorkbenchView(payload)).toThrowError(new ApiError('unexpected-response', 200))
  })

  it('接受日历有效且带 RFC3339 时区偏移的时间', () => {
    const result = parseMCPWorkbenchView(workbenchView({
      active_tasks: [{ ...activeTask, updated_at: '2026-02-28T23:30:00+08:00' }],
      recent_risks: [{ ...recentRisk, completed_at: '2026-02-28T23:30:00-05:30' }],
    }))

    expect(result.active_tasks[0]?.updated_at).toBe('2026-02-28T23:30:00+08:00')
    expect(result.recent_risks[0]?.completed_at).toBe('2026-02-28T23:30:00-05:30')
  })

  it('从固定端点读取并在有效载荷错误时抛出固定响应错误', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(workbenchView({ metrics: { running: 'one', pending: 0, high_risk: 0, completed_30d: 0 } })))
    vi.stubGlobal('fetch', fetchMock)

    await expect(fetchMCPWorkbench()).rejects.toEqual(new ApiError('unexpected-response', 200))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/mcp-workbench')
  })
})
