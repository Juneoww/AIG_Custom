/**
 * 功能：验证生产 App 的公共品牌消费及 main 入口的真实 Provider 接线。
 * 实现：渲染可注入 App 和真实生产 Provider 组合，静态锁定全局样式入口。
 * 输入：公共品牌成功或失败响应、匿名或认证会话。
 * 输出：登录页与应用壳一致品牌、回退名称和生产入口回归结果。
 * 依赖：Testing Library、Vitest、Vite raw import 与应用 Provider。
 */
/// <reference types="vite/client" />
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import mainSource from '../main.tsx?raw'
import { useSession, type SessionState } from '../features/auth/session'
import { ThemeProvider, useThemeMode } from '../shared/theme/ThemeProvider'
import { App } from './App'
import { AppProviders, createAppQueryClient } from './providers/AppProviders'
import { ProductionProviders } from './providers/ProductionProviders'

const authenticated: SessionState = {
  status: 'authenticated',
  subject: { id: 'auditor-1', username: 'reviewer', role: 'auditor', must_change_password: false },
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
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

function ProviderProbe({ expectedQueryClient }: { expectedQueryClient: QueryClient }) {
  const location = useLocation()
  const session = useSession()
  const theme = useThemeMode()
  const queryClient = useQueryClient()

  return (
    <output aria-label="生产 Provider 上下文">
      {[
        theme.mode,
        session.state.status,
        `${location.pathname}${location.search}${location.hash}`,
        queryClient === expectedQueryClient ? 'same-query-client' : 'wrong-query-client',
      ].join('|')}
    </output>
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
  it('keeps an in-flight public brand request across fast session restore', async () => {
    const brandResponse = deferred<Response>()
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.endsWith('/api/v1/public/brand')) return brandResponse.promise
      if (url.endsWith('/api/v1/auth/me')) {
        return Promise.resolve(
          jsonResponse({ id: 'user-1', username: 'operator', role: 'user', must_change_password: false }),
        )
      }
      return Promise.resolve(new Response(null, { status: 404 }))
    })
    vi.stubGlobal('fetch', fetchMock)

    render(
      <ThemeProvider initialMode="light">
        <AppProviders queryClient={createAppQueryClient()}>
          <MemoryRouter initialEntries={['/']}>
            <App />
          </MemoryRouter>
        </AppProviders>
      </ThemeProvider>,
    )

    expect(await screen.findByRole('heading', { name: '治理总览' })).toBeInTheDocument()
    brandResponse.resolve(
      jsonResponse({ product_name: '恢复后品牌', primary_color: '#005a9e', logo_data_url: '' }),
    )

    expect((await screen.findAllByLabelText('恢复后品牌')).length).toBeGreaterThanOrEqual(2)
    expect(fetchMock.mock.calls.filter(([input]) => String(input).endsWith('/api/v1/public/brand'))).toHaveLength(1)
  })

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
      vi.fn((input: RequestInfo | URL) =>
        String(input).endsWith('/api/v1/public/brand')
          ? Promise.resolve(jsonResponse({ product_name: '企业安全台账', primary_color: '#005a9e', logo_data_url: '' }))
          : Promise.resolve(new Response(null, { status: 500 })),
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

  it('按真实生产顺序提供主题、查询、会话与浏览器路由上下文', () => {
    const queryClient = createAppQueryClient()
    window.history.replaceState({}, '', '/provider-check?source=main#context')

    render(
      <ProductionProviders
        queryClient={queryClient}
        sessionInitialState={{ status: 'anonymous' }}
        themeInitialMode="dark"
      >
        <ProviderProbe expectedQueryClient={queryClient} />
      </ProductionProviders>,
    )

    expect(screen.getByLabelText('生产 Provider 上下文')).toHaveTextContent(
      'dark|anonymous|/provider-check?source=main#context|same-query-client',
    )
  })

  it('在生产 main 加载全局样式并使用可渲染 Provider 组合', () => {
    expect(mainSource).toContain("import './shared/styles/global.css'")
    expect(mainSource).toContain('<ProductionProviders>')
    expect(mainSource).not.toContain('webLightTheme')
  })
})
