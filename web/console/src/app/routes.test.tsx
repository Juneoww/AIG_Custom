/**
 * 功能：验证生产路由的会话分流、角色授权、未知页和改密来源链路。
 * 实现：以 MemoryRouter 注入会话状态并渲染真实 AppContent 路由树。
 * 输入：匿名、强制改密、三角色身份与站内深链。
 * 输出：身份页、应用壳、403、404 或安全来源状态。
 * 依赖：Testing Library、React Router、主题与应用 Provider。
 */
import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

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

  it('shows an honest placeholder for known routes and all routes to admin', () => {
    renderRoute(subjectState('admin'), '/admin/users')

    expect(screen.getByRole('heading', { name: '用户管理' })).toBeInTheDocument()
    expect(screen.getByText('用户管理尚未接入')).toBeInTheDocument()
  })
})
