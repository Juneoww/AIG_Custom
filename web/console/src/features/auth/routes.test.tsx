/**
 * 功能：验证身份路由守卫对匿名、已认证和强制改密会话的导航边界。
 * 实现：使用内存路由渲染守卫，检查目标页面和来源位置保存。
 * 输入：不同会话状态与初始路由。
 * 输出：路由重定向和上下文保留断言。
 * 依赖：React Router、Testing Library、Vitest 与身份守卫。
 */
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { AppProviders, createAppQueryClient } from '../../app/providers/AppProviders'
import { RequireAuthenticated, RequirePasswordChange } from '../../app/routes'
import type { SessionState } from './session'

function LocationProbe() {
  const location = useLocation()
  const from = (location.state as { from?: string } | null)?.from ?? ''
  return <output aria-label="当前位置">{`${location.pathname}|${from}`}</output>
}

function renderGuard(initialState: SessionState, initialPath: string) {
  return render(
    <AppProviders queryClient={createAppQueryClient()} sessionInitialState={initialState}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route element={<RequireAuthenticated />}>
            <Route path="/reports/42" element={<p>报告详情</p>} />
          </Route>
          <Route element={<RequirePasswordChange />}>
            <Route path="/change-password" element={<p>修改密码表单</p>} />
          </Route>
          <Route path="/login" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>
    </AppProviders>,
  )
}

describe('身份路由守卫', () => {
  it('匿名访问受保护页面时回登录并保留来源路由', () => {
    renderGuard({ status: 'anonymous' }, '/reports/42')

    expect(screen.getByLabelText('当前位置')).toHaveTextContent('/login|/reports/42')
  })

  it('强制改密账户不能进入普通受保护页面', () => {
    renderGuard(
      {
        status: 'must-change',
        subject: { id: 'user-2', username: 'new-operator', role: 'user', must_change_password: true },
      },
      '/reports/42',
    )

    expect(screen.getByText('修改密码表单')).toBeInTheDocument()
  })

  it('只有强制改密账户能进入改密流程', () => {
    renderGuard({ status: 'anonymous' }, '/change-password')

    expect(screen.getByLabelText('当前位置')).toHaveTextContent('/login|/change-password')
  })
})
