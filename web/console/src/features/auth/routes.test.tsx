/**
 * 功能：验证身份路由守卫对匿名、已认证和强制改密会话的导航边界。
 * 实现：使用内存路由渲染守卫，检查目标页面和来源位置保存。
 * 输入：不同会话状态与初始路由。
 * 输出：路由重定向和上下文保留断言。
 * 依赖：React Router、Testing Library、Vitest 与身份守卫。
 */
import { act, fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { AppProviders, createAppQueryClient } from '../../app/providers/AppProviders'
import { RequireAnonymous, RequireAuthenticated, RequirePasswordChange } from '../../app/routes'
import { type SessionState, useSession } from './session'

function LocationProbe() {
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? ''
  return <output aria-label="当前位置">{`${location.pathname}${location.search}${location.hash}|${from}`}</output>
}

function ChangeActionProbe() {
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? ''
  const { changePassword } = useSession()
  return (
    <button
      aria-label="触发改密"
      type="button"
      onClick={() => void changePassword('invalid-password', 'replacement-password').catch(() => undefined)}
    >
      {`${location.pathname}${location.search}${location.hash}|${from}`}
    </button>
  )
}

function renderGuard(initialState: SessionState, initialEntry: string | { pathname: string; state?: unknown }) {
  return render(
    <AppProviders queryClient={createAppQueryClient()} sessionInitialState={initialState}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route element={<RequireAuthenticated />}>
            <Route path="/reports/42" element={<p>报告详情</p>} />
            <Route path="/tasks" element={<LocationProbe />} />
          </Route>
          <Route element={<RequirePasswordChange />}>
            <Route path="/change-password" element={<ChangeActionProbe />} />
          </Route>
          <Route element={<RequireAnonymous />}>
            <Route path="/login" element={<LocationProbe />} />
          </Route>
          <Route path="/" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </AppProviders>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'aig_csrf=; Max-Age=0; Path=/'
})

describe('身份路由守卫', () => {
  it('匿名访问受保护深链时回登录并保留路径、查询与锚点', () => {
    renderGuard({ status: 'anonymous' }, '/reports/42?page=2#row')

    expect(screen.getByLabelText('当前位置')).toHaveTextContent('/login|/reports/42?page=2#row')
  })

  it('强制改密账户不能进入普通受保护页面', () => {
    renderGuard(
      {
        status: 'must-change',
        subject: { id: 'user-2', username: 'new-operator', role: 'user', must_change_password: true },
      },
      '/reports/42',
    )

    expect(screen.getByRole('button', { name: '触发改密' })).toHaveTextContent('/change-password|/reports/42')
  })

  it('匿名账户不能进入改密流程', () => {
    renderGuard({ status: 'anonymous' }, '/change-password')

    expect(screen.getByLabelText('当前位置')).toHaveTextContent('/login|/')
  })

  it('已认证账户可主动进入改密流程并保留安全来源', () => {
    renderGuard(
      {
        status: 'authenticated',
        subject: { id: 'user-2', username: 'operator', role: 'user', must_change_password: false },
      },
      { pathname: '/change-password', state: { from: '/models?page=2#credential' } },
    )

    expect(screen.getByRole('button', { name: '触发改密' })).toHaveTextContent(
      '/change-password|/models?page=2#credential',
    )
  })

  it('登录到强制改密再重新登录的链路持续传递合法来源', () => {
    const from = '/tasks?page=2#row'
    const firstView = renderGuard(
      {
        status: 'must-change',
        subject: { id: 'user-2', username: 'new-operator', role: 'user', must_change_password: true },
      },
      { pathname: '/login', state: { from } },
    )

    expect(screen.getByRole('button', { name: '触发改密' })).toHaveTextContent(`/change-password|${from}`)
    firstView.unmount()

    const secondView = renderGuard({ status: 'anonymous' }, { pathname: '/change-password', state: { from } })
    expect(screen.getByLabelText('当前位置')).toHaveTextContent(`/login|${from}`)
    secondView.unmount()

    renderGuard(
      {
        status: 'authenticated',
        subject: { id: 'user-2', username: 'new-operator', role: 'user', must_change_password: false },
      },
      { pathname: '/login', state: { from } },
    )
    expect(screen.getByLabelText('当前位置')).toHaveTextContent(`${from}|`)
  })

  it.each([
    'https://outside.example/tasks',
    '//outside.example/tasks',
    'javascript:alert(1)',
    '/login',
    '/%2e//evil.example',
  ])(
    '拒绝恶意或循环来源 %s 并回到首页',
    (from) => {
      renderGuard(
        {
          status: 'authenticated',
          subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false },
        },
        { pathname: '/login', state: { from } },
      )

      expect(screen.getByLabelText('当前位置')).toHaveTextContent('/|')
    },
  )

  it.each(['/reports/42', '/change-password', '/login'])(
    '恢复失败时守卫 %s 显示安全重试状态而不跳转',
    (path) => {
      renderGuard({ status: 'restore-error', message: '无法验证登录状态，请重试。' }, path)

      expect(screen.getByRole('alert')).toHaveTextContent('无法验证登录状态，请重试。')
      expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
    },
  )

  it('改密请求 403 时保留当前路由及其来源上下文', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 403 })))
    const from = '/tasks?page=2#row'
    renderGuard(
      {
        status: 'must-change',
        subject: { id: 'user-2', username: 'new-operator', role: 'user', must_change_password: true },
      },
      { pathname: '/change-password', state: { from } },
    )

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '触发改密' }))
      await Promise.resolve()
    })

    expect(screen.getByRole('button', { name: '触发改密' })).toHaveTextContent(`/change-password|${from}`)
  })
})
