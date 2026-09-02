/**
 * 功能：验证 MCP 安全扫描工作台只呈现服务端白名单投影，并在权限与空状态下保持受治理边界。
 * 实现：在真实 Query、Session、主题和内存路由中驱动 MCP 工作台安全响应。
 * 输入：安全 DTO、403、服务错误和附加敏感字段。
 * 输出：指标、扫描入口、活动任务、风险摘要和固定安全状态。
 * 依赖：Vitest、Testing Library、React Router、Fluent UI 与 MCP 工作台客户端。
 */
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { AppProviders, createAppQueryClient } from '../../app/providers/AppProviders'
import type { SubjectRole } from '../../shared/api/types'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { MCPWorkbenchPage } from './MCPWorkbenchPage'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function workbenchResponse(overrides: Record<string, unknown> = {}) {
  return {
    metrics: { running: 2, pending: 1, high_risk: 3, completed_30d: 12 },
    active_tasks: [
      {
        task_id: 'task-opaque-1',
        label: 'MCP 扫描 · task-opa',
        source_kind: 'service',
        phase: null,
        status: 'running',
        updated_at: '2026-09-02T01:00:00Z',
      },
    ],
    recent_risks: [
      {
        report_id: 'report-opaque-1',
        task_id: 'task-opaque-1',
        severity: 'high',
        category: 'dangerous_tool',
        summary: '检测到高风险 MCP 工具行为。',
        completed_at: '2026-09-02T01:00:00Z',
      },
    ],
    ...overrides,
  }
}

function LocationProbe() {
  const location = useLocation()
  return <output aria-label="当前工作台路由">{`${location.pathname}${location.search}${location.hash}`}</output>
}

function renderWorkbench(role: SubjectRole = 'user', initialPath = '/tasks/mcp') {
  return render(
    <ThemeProvider initialMode="light">
      <AppProviders
        queryClient={createAppQueryClient()}
        sessionInitialState={{
          status: 'authenticated',
          subject: { id: `${role}-1`, username: `${role}-operator`, role, must_change_password: false },
        }}
      >
        <MemoryRouter initialEntries={[initialPath]}>
          <MCPWorkbenchPage />
          <LocationProbe />
        </MemoryRouter>
      </AppProviders>
    </ThemeProvider>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('MCPWorkbenchPage', () => {
  it('announces the loading state while the safe workbench query is pending', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))

    renderWorkbench()

    expect(screen.getByRole('heading', { name: 'MCP 安全扫描' })).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: '正在加载 MCP 安全扫描工作台' })).toBeInTheDocument()
  })

  it('keeps header navigation inside the current router', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))

    renderWorkbench()

    fireEvent.click(screen.getByRole('link', { name: '新建 MCP 扫描' }))
    expect(screen.getByLabelText('当前工作台路由')).toHaveTextContent('/tasks/new?task_type=mcp_scan&source_kind=repository')

    fireEvent.click(screen.getByRole('link', { name: '扫描任务' }))
    expect(screen.getByLabelText('当前工作台路由')).toHaveTextContent('/tasks')
  })

  it('renders metrics, governed entry points, active work and risks from the safe projection only', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          workbenchResponse({
            endpoint: 'ENDPOINT-SENTINEL',
            raw_result: 'RAW-RESULT-SENTINEL',
            model_id: 'MODEL-SENTINEL',
            active_tasks: [{
              ...workbenchResponse().active_tasks[0],
              params: { authorization: 'TOKEN-SENTINEL' },
              endpoint: 'ACTIVE-ENDPOINT-SENTINEL',
            }],
            recent_risks: [{
              ...workbenchResponse().recent_risks[0],
              raw_result: 'RISK-RAW-SENTINEL',
            }],
          }),
        ),
      ),
    )

    renderWorkbench()

    expect(await screen.findByRole('group', { name: '正在执行' })).toHaveTextContent('2')
    expect(screen.getByRole('group', { name: '等待执行' })).toHaveTextContent('1')
    expect(screen.getByRole('group', { name: '高风险' })).toHaveTextContent('3')
    expect(screen.getByRole('group', { name: '30 日已完成' })).toHaveTextContent('12')
    expect(screen.getByRole('navigation', { name: '页面路径' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '扫描任务' })).toHaveAttribute('href', '/tasks')
    expect(screen.getByRole('link', { name: '新建 MCP 扫描' })).toHaveAttribute(
      'href',
      '/tasks/new?task_type=mcp_scan&source_kind=repository',
    )
    expect(screen.getByRole('link', { name: '创建仓库 MCP 扫描' })).toHaveAttribute(
      'href',
      '/tasks/new?task_type=mcp_scan&source_kind=repository',
    )
    expect(screen.getByRole('link', { name: '创建受控服务 MCP 扫描' })).toHaveAttribute(
      'href',
      '/tasks/new?task_type=mcp_scan&source_kind=service',
    )
    expect(screen.getByRole('table', { name: '进行中的 MCP 扫描' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '查看任务 MCP 扫描 · task-opa' })).toHaveAttribute('href', '/tasks/task-opaque-1')
    expect(screen.getByText('阶段未提供')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '最近风险' })).toHaveTextContent('危险工具调用')
    expect(screen.getByRole('link', { name: '查看安全报告 report-opaque-1' })).toHaveAttribute('href', '/reports/report-opaque-1')
    expect(screen.getByRole('link', { name: '查看 MCP 扫描历史' })).toHaveAttribute('href', '/tasks?task_type=mcp_scan')
    expect(screen.getByRole('link', { name: '查看 MCP 知识库' })).toHaveAttribute('href', '/knowledge/mcp')
    expect(document.body).not.toHaveTextContent(/ENDPOINT-SENTINEL|RAW-RESULT-SENTINEL|MODEL-SENTINEL|TOKEN-SENTINEL|RISK-RAW-SENTINEL/)
  })

  it('provides a labelled horizontal viewport for the active task table', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(workbenchResponse())))

    renderWorkbench()

    const table = await screen.findByRole('table', { name: '进行中的 MCP 扫描' })
    const viewport = screen.getByRole('region', { name: '进行中的 MCP 扫描表格' })
    expect(viewport).toHaveAttribute('tabindex', '0')
    expect(getComputedStyle(viewport).overflowX).toBe('auto')
    expect(getComputedStyle(table).minWidth).toBe('680px')
  })

  it('keeps the auditor workbench read-only while preserving safe operational visibility', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(workbenchResponse())))

    renderWorkbench('auditor')

    expect(await screen.findByRole('table', { name: '进行中的 MCP 扫描' })).toBeInTheDocument()
    expect(screen.getByRole('navigation', { name: '页面路径' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '扫描任务' })).toHaveAttribute('href', '/tasks')
    expect(screen.queryByRole('link', { name: '新建 MCP 扫描' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '创建仓库 MCP 扫描' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: '创建受控服务 MCP 扫描' })).not.toBeInTheDocument()
  })

  it('uses scoped empty states when the safe projection has no active work or recent risks', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(workbenchResponse({ active_tasks: [], recent_risks: [] }))))

    renderWorkbench()

    expect(await screen.findByText('暂无进行中的 MCP 扫描')).toBeInTheDocument()
    expect(screen.getByText('暂无近期 MCP 风险')).toBeInTheDocument()
  })

  it('uses a non-disclosing forbidden state for a 403 response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: 'PRIVATE-SENTINEL' }, 403)))

    renderWorkbench()

    expect(await screen.findByText('无权查看 MCP 安全扫描工作台')).toBeInTheDocument()
    expect(document.body).not.toHaveTextContent('PRIVATE-SENTINEL')
  })

  it('rejects a non-null phase value without exposing endpoint or log text', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse(
          workbenchResponse({
            active_tasks: [{
              ...workbenchResponse().active_tasks[0],
              phase: 'https://private.example/mcp?log=PHASE-SENTINEL',
              endpoint: 'ENDPOINT-SENTINEL',
              raw_result: 'RAW-LOG-SENTINEL',
            }],
          }),
        ),
      ),
    )

    renderWorkbench()

    expect(await screen.findByRole('alert')).toHaveTextContent('暂时无法加载 MCP 安全扫描工作台')
    expect(document.body).not.toHaveTextContent(/PHASE-SENTINEL|ENDPOINT-SENTINEL|RAW-LOG-SENTINEL|private\.example/)
  })

  it('offers an explicit retry after a service error without auto-replay', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 500 }))
      .mockResolvedValueOnce(jsonResponse(workbenchResponse()))
    vi.stubGlobal('fetch', fetchMock)

    renderWorkbench()

    expect(await screen.findByRole('alert')).toHaveTextContent('暂时无法加载 MCP 安全扫描工作台')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(await screen.findByRole('table', { name: '进行中的 MCP 扫描' })).toBeInTheDocument()
  })
})
