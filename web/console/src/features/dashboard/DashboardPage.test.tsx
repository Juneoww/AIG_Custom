/**
 * 功能：验证治理总览只消费安全聚合 DTO，并完整呈现台账四区与独立状态。
 * 实现：在真实 QueryClient、主题和内存路由中驱动网络响应及重试交互。
 * 输入：成功、空数据、403、网络失败和含额外敏感字段的总览响应。
 * 输出：管理者摘要、管理者信号、30 日趋势、待关注、最近任务与安全状态断言。
 * 依赖：Vitest、Testing Library、React Router 与应用 Provider。
 */
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { createAppQueryClient, AppProviders } from '../../app/providers/AppProviders'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { DashboardPage } from './DashboardPage'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function trend(completed = 1) {
  return Array.from({ length: 30 }, (_, index) => ({
    date: new Date(Date.UTC(2026, 6, 20 + index)).toISOString(),
    completed,
    security_score: completed ? 70 + (index % 5) : null,
    high: completed ? index % 3 : 0,
    medium: completed ? 2 : 0,
    low: completed ? 4 : 0,
  }))
}

function dashboardResponse(overrides: Record<string, unknown> = {}) {
  return {
    has_data: true,
    security_score: 72,
    mapping_versions: ['risk-v1', 'risk-v2'],
    risk: { mapping_version: 'risk-v2', score: 72, high: 3, medium: 6, low: 12 },
    trend: trend(),
    attention: [
      {
        report_id: 'report-opaque-1',
        task_id: 'task-opaque-1',
        task_type: 'mcp_scan',
        completed_at: '2026-08-18T00:00:00Z',
        score: 45,
        high: 3,
        medium: 2,
        low: 1,
      },
    ],
    recent_tasks: [
      {
        id: 'task-opaque-2',
        owner: 'operator',
        task_type: 'ai_infra_scan',
        status: 'running',
        created_at: '2026-08-17T00:00:00Z',
        updated_at: '2026-08-18T01:00:00Z',
      },
    ],
    ...overrides,
  }
}

function renderDashboard() {
  return render(
    <ThemeProvider initialMode="light">
      <AppProviders
        queryClient={createAppQueryClient()}
        sessionInitialState={{
          status: 'authenticated',
          subject: { id: 'user-1', username: 'operator', role: 'user', must_change_password: false },
        }}
      >
        <MemoryRouter>
          <DashboardPage />
        </MemoryRouter>
      </AppProviders>
    </ThemeProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('DashboardPage', () => {
  it('在单屏语义区域展示管理者摘要、管理者信号、30 日趋势、高风险待办和最近任务', async () => {
    let requestSignal: AbortSignal | undefined
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      requestSignal = init?.signal ?? undefined
      return Promise.resolve(
        jsonResponse(
          dashboardResponse({
            raw_result: 'RAW-SENTINEL',
            token: 'TOKEN-SENTINEL',
            path: 'PATH-SENTINEL',
          }),
        ),
      )
    })
    vi.stubGlobal('fetch', fetchMock)

    renderDashboard()

    expect(await screen.findByRole('heading', { level: 1, name: '治理总览' })).toBeInTheDocument()
    const summary = await screen.findByRole('region', { name: '管理者摘要' })
    expect(summary).toHaveTextContent('当前总体安全分72')
    expect(summary).toHaveTextContent('高风险3')
    expect(summary).toHaveTextContent('中风险6')
    expect(summary).toHaveTextContent('低风险12')
    expect(summary).toHaveTextContent('近 30 日已完成扫描30')
    expect(summary).toHaveTextContent('执行中任务1')
    expect(summary).toHaveTextContent('待调度任务0')
    const trendRegion = screen.getByRole('region', { name: '最近 30 日趋势' })
    const signals = screen.getByRole('region', { name: '管理者信号' })
    expect(summary.compareDocumentPosition(trendRegion) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING,
    )
    expect(signals).toHaveTextContent('高风险3')
    expect(signals).toHaveTextContent('执行中任务1')
    expect(signals).toHaveTextContent('risk-v1')
    expect(signals).toHaveTextContent('risk-v2')
    expect(summary).not.toContainElement(signals)
    expect(screen.queryByRole('region', { name: '核心指标' })).not.toBeInTheDocument()
    expect(within(trendRegion).getAllByRole('listitem')).toHaveLength(30)
    const trendFigure = within(trendRegion).getByRole('figure', {
      name: '最近 30 日快照平均安全分',
    })
    expect(within(trendFigure).getAllByRole('listitem')).toHaveLength(30)
    expect(screen.getByRole('region', { name: '高风险待办' })).toHaveTextContent('MCP 扫描')
    expect(screen.getByRole('link', { name: '查看报告 report-opaque-1' })).toHaveAttribute(
      'href',
      '/reports/report-opaque-1',
    )
    expect(screen.getByRole('region', { name: '最近任务' })).toHaveTextContent('AI 基础设施扫描')
    expect(screen.getByRole('link', { name: '查看任务 task-opaque-2' })).toHaveAttribute(
      'href',
      '/tasks/task-opaque-2',
    )
    expect(screen.getByText('当前总览包含多个风险映射版本')).toBeInTheDocument()
    expect(document.body).not.toHaveTextContent('RAW-SENTINEL')
    expect(document.body).not.toHaveTextContent('TOKEN-SENTINEL')
    expect(document.body).not.toHaveTextContent('PATH-SENTINEL')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`${window.location.origin}/api/v1/platform/dashboard`)
    expect(requestSignal).toBeInstanceOf(AbortSignal)
  })

  it('管理者摘要按任务状态矩阵计算治理动态', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          dashboardResponse({
            trend: trend(2),
            recent_tasks: [
              {
                id: 'task-pending',
                owner: 'operator',
                task_type: 'mcp_scan',
                status: 'pending',
                created_at: '2026-08-17T00:00:00Z',
                updated_at: '2026-08-18T01:00:00Z',
              },
              {
                id: 'task-dispatching',
                owner: 'operator',
                task_type: 'mcp_scan',
                status: 'dispatching',
                created_at: '2026-08-17T00:00:00Z',
                updated_at: '2026-08-18T01:00:00Z',
              },
              {
                id: 'task-running',
                owner: 'operator',
                task_type: 'mcp_scan',
                status: 'running',
                created_at: '2026-08-17T00:00:00Z',
                updated_at: '2026-08-18T01:00:00Z',
              },
              {
                id: 'task-succeeded',
                owner: 'operator',
                task_type: 'mcp_scan',
                status: 'succeeded',
                created_at: '2026-08-17T00:00:00Z',
                updated_at: '2026-08-18T01:00:00Z',
              },
            ],
          }),
        ),
      ),
    )

    renderDashboard()

    const summary = await screen.findByRole('region', { name: '管理者摘要' })
    expect(summary).toHaveTextContent('近 30 日已完成扫描60')
    expect(summary).toHaveTextContent('执行中任务3')
    expect(summary).toHaveTextContent('待调度任务2')
  })

  it('加载期间显示专用状态且不提前显示空数据', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))

    renderDashboard()

    expect(screen.getByRole('status')).toHaveTextContent('正在加载治理总览')
    expect(screen.queryByText('暂无已完成报告')).not.toBeInTheDocument()
  })

  it('空数据不伪装成满分，也不展示管理者摘要或信号', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          dashboardResponse({
            has_data: false,
            security_score: null,
            mapping_versions: [],
            risk: { mapping_version: '', score: 0, high: 0, medium: 0, low: 0 },
            trend: trend(0),
            attention: [],
            recent_tasks: [],
          }),
        ),
      ),
    )

    renderDashboard()

    expect(await screen.findByText('暂无已完成报告')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: '管理者摘要' })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: '管理者信号' })).not.toBeInTheDocument()
    expect(screen.getByRole('region', { name: '最近 30 日趋势' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '高风险待办' })).toHaveTextContent('暂无待关注事项')
    expect(screen.getByRole('region', { name: '最近任务' })).toHaveTextContent('暂无扫描任务')
    expect(document.body).not.toHaveTextContent('100')
  })

  it('拒绝空态标记与分数风险数据互相矛盾的 200 响应', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse(dashboardResponse({ has_data: false }))),
    )

    renderDashboard()

    expect(await screen.findByRole('alert')).toHaveTextContent('暂时无法加载治理总览')
    expect(screen.queryByText('暂无已完成报告')).not.toBeInTheDocument()
    expect(document.body).not.toHaveTextContent('快照平均安全分72')
  })

  it('拒绝有数据标记却缺少安全分的 200 响应', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse(dashboardResponse({ security_score: null }))),
    )

    renderDashboard()

    expect(await screen.findByRole('alert')).toHaveTextContent('暂时无法加载治理总览')
  })

  it('403 使用独立无权限状态且不跳转或泄露响应内容', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: 'PRIVATE-SENTINEL' }, 403)))

    renderDashboard()

    expect(await screen.findByText('无权查看治理总览')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('无权查看治理总览')
    expect(document.body).not.toHaveTextContent('PRIVATE-SENTINEL')
    expect(screen.queryByRole('heading', { name: '登录平台' })).not.toBeInTheDocument()
  })

  it('失败状态允许显式重试且查询本身不自动重放', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 500 }))
      .mockResolvedValueOnce(jsonResponse(dashboardResponse()))
    vi.stubGlobal('fetch', fetchMock)

    renderDashboard()

    expect(await screen.findByRole('alert')).toHaveTextContent('暂时无法加载治理总览')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(await screen.findByRole('region', { name: '管理者摘要' })).toBeInTheDocument()
  })
})
