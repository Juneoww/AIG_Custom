/**
 * 功能：验证生产 App 的公共品牌消费及 main 入口的真实 Provider 接线。
 * 实现：在可注入路由和会话边界中渲染 App，并静态锁定生产入口依赖顺序。
 * 输入：公共品牌成功或失败响应、匿名或认证会话。
 * 输出：登录页与应用壳一致品牌、回退名称和生产入口回归结果。
 * 依赖：Testing Library、Vitest、Vite raw import 与应用 Provider。
 */
/// <reference types="vite/client" />
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import mainSource from '../main.tsx?raw'
import type { SessionState } from '../features/auth/session'
import { ThemeProvider } from '../shared/theme/ThemeProvider'
import { App } from './App'
import { AppProviders, createAppQueryClient } from './providers/AppProviders'

const authenticated: SessionState = {
  status: 'authenticated',
  subject: { id: 'auditor-1', username: 'reviewer', role: 'auditor', must_change_password: false },
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function renderApp(state: SessionState, path = '/') {
  return render(
    <ThemeProvider initialMode="light">
      <AppProviders queryClient={createAppQueryClient()} sessionInitialState={state}>
        <MemoryRouter initialEntries={[path]}>
          <App />
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

afterEach(() => vi.unstubAllGlobals())

describe('App production tree', () => {
  it('uses configured public branding before login without old branding', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({ product_name: '华北区域 AI 安全监管平台', primary_color: '#005a9e', logo_data_url: '' }),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderApp({ status: 'anonymous' }, '/login')

    expect(await screen.findByText('华北区域 AI 安全监管平台')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(screen.queryByText(/^A\.I\.G$/)).not.toBeInTheDocument()
  })

  it('shares configured brand with the authenticated shell', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({ product_name: '企业安全台账', primary_color: '#005a9e', logo_data_url: '' }),
      ),
    )
    renderApp(authenticated)

    expect((await screen.findAllByLabelText('企业安全台账')).length).toBeGreaterThanOrEqual(2)
    expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument()
  })

  it('falls back safely when public branding is unavailable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 500 })))
    renderApp({ status: 'anonymous' }, '/login')

    expect(await screen.findByText('AI 安全治理平台')).toBeInTheDocument()
  })

  it('wires global styles, theme, query/session and browser routing in production main', () => {
    expect(mainSource).toContain("import './shared/styles/global.css'")
    expect(mainSource).toContain('<ThemeProvider>')
    expect(mainSource).toContain('<AppProviders>')
    expect(mainSource).toContain('<BrowserRouter>')
    expect(mainSource).not.toContain('webLightTheme')
  })
})
