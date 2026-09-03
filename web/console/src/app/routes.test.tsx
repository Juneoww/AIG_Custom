/**
 * 功能：验证生产路由的会话分流、角色授权、未知页和改密来源链路。
 * 实现：以 MemoryRouter 注入会话状态并渲染真实 AppContent 路由树。
 * 输入：匿名、强制改密、三角色身份与站内深链。
 * 输出：身份页、应用壳、403、404 或安全来源状态。
 * 依赖：Testing Library、React Router、主题与应用 Provider。
 */
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { SessionState } from '../features/auth/session'
import type { SubjectRole } from '../shared/api/types'
import { ThemeProvider } from '../shared/theme/ThemeProvider'
import { AppContent } from './App'
import { AppProviders, createAppQueryClient } from './providers/AppProviders'

function subjectState(role: SubjectRole): SessionState {
  return {
    status: 'authenticated',
    subject: { id: `${role}-1`, username: `${role}-operator`, role, must_change_password: false },
  }
}

function LocationProbe() {
  const location = useLocation()
  return <output aria-label="当前位置">{JSON.stringify({ path: `${location.pathname}${location.search}${location.hash}`, state: location.state })}</output>
}

function renderRoute(initialState: SessionState, path: string) {
  return render(
    <ThemeProvider initialMode="light">
      <AppProviders queryClient={createAppQueryClient()} sessionInitialState={initialState}>
        <MemoryRouter initialEntries={[path]}>
          <AppContent />
          <LocationProbe />
        </MemoryRouter>
      </AppProviders>
    </ThemeProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('production routes', () => {
  it('allows anonymous users to open password reset outside the shell', () => {
    renderRoute({ status: 'anonymous' }, '/reset-password')

    expect(screen.getByRole('heading', { name: '重置密码' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '主导航' })).not.toBeInTheDocument()
  })

  it('redirects must-change sessions from password reset to required password change', () => {
    renderRoute(
      {
        status: 'must-change',
        subject: { id: 'user-1', username: 'first-login', role: 'user', must_change_password: true },
      },
      '/reset-password',
    )

    expect(screen.getByRole('heading', { name: '更新初始密码' })).toBeInTheDocument()
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('"path":"/change-password"')
  })

  it('redirects authenticated sessions away from password reset to overview', () => {
    renderRoute(subjectState('admin'), '/reset-password')

    expect(screen.getByRole('heading', { name: '治理总览' })).toBeInTheDocument()
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('"path":"/"')
  })

  it.each([
    ['user', '/admin/users'],
    ['auditor', '/admin/brand'],
  ] as const)('renders a non-disclosing 403 for %s direct access', (role, path) => {
    renderRoute(subjectState(role), path)

    expect(screen.getByRole('heading', { name: '无权访问' })).toBeInTheDocument()
    expect(screen.queryByText('用户管理')).not.toBeInTheDocument()
    expect(screen.queryByText('品牌设置')).not.toBeInTheDocument()
    expect(screen.getByLabelText('当前位置')).toHaveTextContent(`"path":"${path}"`)
  })

  it('renders an authenticated unknown route as 404 with an overview action', () => {
    renderRoute(subjectState('admin'), '/not-a-console-route')

    expect(screen.getByRole('heading', { name: '页面不存在' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回治理总览' })).toHaveAttribute('href', '/')
  })

  it('protects a deep link and preserves pathname, search and hash for login', () => {
    renderRoute({ status: 'anonymous' }, '/tasks?page=2#row')

    expect(screen.getByRole('heading', { name: '登录平台' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '主导航' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('当前位置')).toHaveTextContent(
      '"path":"/login","state":{"from":"/tasks?page=2#row"}',
    )
  })

  it('sends must-change sessions to the required flow outside the shell', () => {
    renderRoute(
      {
        status: 'must-change',
        subject: { id: 'user-1', username: 'first-login', role: 'user', must_change_password: true },
      },
      '/reports',
    )

    expect(screen.getByRole('heading', { name: '更新初始密码' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '主导航' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('当前位置')).toHaveTextContent('"state":{"from":"/reports"}')
  })

  it('keeps restore errors safe and retryable without routing to login', () => {
    renderRoute({ status: 'restore-error', message: '无法验证登录状态，请重试。' }, '/tasks')

    expect(screen.getByRole('alert')).toHaveTextContent('无法验证登录状态，请重试。')
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '登录平台' })).not.toBeInTheDocument()
  })

  it('opens active password change outside the shell with its full return target', () => {
    renderRoute(subjectState('admin'), '/models?page=2#credential')

    fireEvent.click(screen.getByRole('link', { name: '修改密码' }))

    expect(screen.getByRole('heading', { name: '修改登录密码' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '主导航' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('当前位置')).toHaveTextContent(
      '"path":"/change-password","state":{"from":"/models?page=2#credential"}',
    )
  })

  it('renders the governed user-management page for administrators', () => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))
    renderRoute(subjectState('admin'), '/admin/users')

    expect(screen.getByRole('heading', { name: '用户管理' })).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: '正在加载用户台账' })).toBeInTheDocument()
    expect(screen.queryByText('用户管理尚未接入')).not.toBeInTheDocument()
  })

  it.each(['auditor', 'admin'] as const)('让%s角色读取真实审计与系统路由', (role) => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))
    const audit = renderRoute(subjectState(role), '/admin/audit')
    expect(screen.getByRole('heading', { name: '审计事件' })).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: '正在加载审计事件' })).toBeInTheDocument()
    audit.unmount()
    renderRoute(subjectState(role), '/system')
    expect(screen.getByRole('heading', { name: '系统信息' })).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: '正在加载同步状态' })).toBeInTheDocument()
  })

  it('offers profile and about pages to each authenticated role', () => {
    renderRoute(subjectState('user'), '/profile')
    expect(screen.getByRole('heading', { name: '个人资料' })).toBeInTheDocument()
    renderRoute(subjectState('user'), '/about')
    expect(screen.getByRole('heading', { name: '关于' })).toBeInTheDocument()
  })

  it('replaces overview and task placeholders with governed Task12 pages', () => {
    renderRoute(subjectState('user'), '/')
    expect(screen.getByRole('heading', { name: '治理总览' })).toBeInTheDocument()
    expect(screen.queryByText('治理总览尚未接入')).not.toBeInTheDocument()

    renderRoute(subjectState('user'), '/tasks/new')
    expect(screen.getByRole('heading', { name: '创建扫描任务' })).toBeInTheDocument()
    expect(screen.queryByText('扫描任务尚未接入')).not.toBeInTheDocument()
  })

  it('keeps task creation as a known role-guarded route', () => {
    renderRoute(subjectState('auditor'), '/tasks/new')

    expect(screen.getByRole('heading', { name: '无权访问' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '页面不存在' })).not.toBeInTheDocument()
  })

  it('keeps the generic task list route and its type filter unchanged', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [], total: 0, page: 1, page_size: 20 }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)
    renderRoute(subjectState('user'), '/tasks')

    expect(await screen.findByRole('heading', { name: '扫描任务' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '任务类型' })).toBeInTheDocument()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/tasks?page=1&page_size=20')
  })

  it.each(['user', 'auditor', 'admin'] as const)('routes %s to the static AI infrastructure task list', async (role) => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ items: [], total: 0, page: 1, page_size: 20 }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)
    renderRoute(subjectState(role), '/tasks/ai-infra')

    expect(await screen.findByRole('heading', { name: 'AI 基础设施扫描' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '任务详情' })).not.toBeInTheDocument()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      'http://localhost:3000/api/v1/platform/tasks?page=1&page_size=20&task_type=ai_infra_scan',
    )
  })

  it.each(['user', 'auditor', 'admin'] as const)('让%s角色读取真实报告列表与详情路由', (role) => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))
    const list = renderRoute(subjectState(role), '/reports')
    expect(screen.getByRole('progressbar', { name: '正在加载安全报告' })).toBeInTheDocument()
    expect(screen.queryByText('安全报告尚未接入')).not.toBeInTheDocument()
    list.unmount()

    renderRoute(subjectState(role), '/reports/report-opaque-1')
    expect(screen.getByRole('progressbar', { name: '正在加载安全报告' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: '页面不存在' })).not.toBeInTheDocument()
  })

  it.each(['user', 'auditor', 'admin'] as const)('让%s角色读取真实模型治理路由', (role) => {
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(() => undefined)))
    renderRoute(subjectState(role), '/models?page=2')

    expect(screen.getByRole('heading', { name: '模型与凭据' })).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: '正在加载模型目录' })).toBeInTheDocument()
    expect(screen.queryByText('模型与凭据尚未接入')).not.toBeInTheDocument()
  })
})
