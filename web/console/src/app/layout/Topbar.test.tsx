/**
 * 功能：验证顶栏身份信息、三主题切换、主动改密入口和退出安全语义。
 * 实现：在真实主题与会话 Provider 中模拟选择和退出请求。
 * 输入：已认证管理员会话、主题选项及退出响应。
 * 输出：中文角色、唯一主题存储、会话状态与固定失败提示。
 * 依赖：Testing Library、Vitest、React Router 和应用 Provider。
 */
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useSession, type SessionState } from '../../features/auth/session'
import { PublicBrandProvider } from '../../shared/brand/PublicBrandProvider'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { AppProviders, createAppQueryClient } from '../providers/AppProviders'
import { Topbar } from './Topbar'

const authenticated: SessionState = {
  status: 'authenticated',
  subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false },
}

function StatusProbe() {
  const { state } = useSession()
  const location = useLocation()
  return <output>{`${state.status}|${location.pathname}${location.search}${location.hash}`}</output>
}

function renderTopbar() {
  return render(
    <ThemeProvider initialMode="light">
      <AppProviders queryClient={createAppQueryClient()} sessionInitialState={authenticated}>
        <MemoryRouter initialEntries={['/models?page=2#credential']}>
          <PublicBrandProvider
            initialConfig={{ product_name: '企业治理平台', primary_color: '#005a9e', logo_data_url: '' }}
          >
            <Topbar />
            <StatusProbe />
          </PublicBrandProvider>
        </MemoryRouter>
      </AppProviders>
    </ThemeProvider>,
  )
}

beforeEach(() => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
  window.localStorage.clear()
})

describe('Topbar', () => {
  it('shows Chinese identity, all theme modes and a safe active-password link', () => {
    renderTopbar()

    expect(screen.getByText('security-admin')).toBeInTheDocument()
    expect(screen.getByText('管理员')).toBeInTheDocument()
    const theme = screen.getByRole('combobox', { name: '主题模式' })
    expect(Array.from((theme as HTMLSelectElement).options).map(({ text }) => text)).toEqual([
      '浅色',
      '深色',
      '跟随系统',
    ])
    fireEvent.change(theme, { target: { value: 'dark' } })
    expect(window.localStorage.getItem('aig-console-theme')).toBe('dark')
    expect(Object.keys(window.localStorage)).toEqual(['aig-console-theme'])
    expect(screen.getByRole('link', { name: '修改密码' })).toHaveAttribute('href', '/change-password')
    expect(screen.queryByText('帮助中心')).not.toBeInTheDocument()
    expect(screen.queryByText('English')).not.toBeInTheDocument()
  })

  it('moves to anonymous only after a successful logout', async () => {
    document.cookie = 'aig_csrf=current-csrf; path=/'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
    renderTopbar()

    fireEvent.click(screen.getByRole('button', { name: '退出登录' }))

    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('anonymous|/models?page=2#credential'))
  })

  it('keeps the subject and shows a fixed message when logout fails', async () => {
    document.cookie = 'aig_csrf=current-csrf; path=/'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 500 })))
    renderTopbar()

    fireEvent.click(screen.getByRole('button', { name: '退出登录' }))

    expect(await screen.findByText('退出失败，请稍后重试。')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('authenticated|/models?page=2#credential')
    expect(screen.getByText('security-admin')).toBeInTheDocument()
  })
})
