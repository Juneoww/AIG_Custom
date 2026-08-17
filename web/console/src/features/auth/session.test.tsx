/**
 * 功能：验证身份会话恢复、强制改密、退出与查询重试策略。
 * 实现：通过真实上下文状态机和受控 fetch 响应观察状态迁移。
 * 输入：模拟身份端点响应及会话操作。
 * 输出：状态机与请求合同断言。
 * 依赖：React Testing Library、Vitest、TanStack Query 与会话提供器。
 */
import { act, render, screen, waitFor } from '@testing-library/react'
import { useEffect } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { AppProviders, createAppQueryClient } from '../../app/providers/AppProviders'
import { SessionProvider, type SessionContextValue, type SessionState, useSession } from './session'

let observedSession: SessionContextValue | undefined

function SessionProbe() {
  const session = useSession()

  useEffect(() => {
    observedSession = session
  }, [session])

  return <output aria-label="会话状态">{session.state.status}</output>
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

afterEach(() => {
  observedSession = undefined
  vi.unstubAllGlobals()
  document.cookie = 'aig_csrf=; Max-Age=0; Path=/'
})

describe('SessionProvider', () => {
  it('从 /me 恢复普通已认证会话', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({
          id: 'user-1',
          username: 'operator',
          role: 'user',
          must_change_password: false,
        }),
      ),
    )

    render(
      <SessionProvider>
        <SessionProbe />
      </SessionProvider>,
    )

    expect(screen.getByLabelText('会话状态')).toHaveTextContent('restoring')
    await waitFor(() => expect(screen.getByLabelText('会话状态')).toHaveTextContent('authenticated'))
  })

  it('把首次登录账户限制在强制改密状态', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({
          id: 'user-2',
          username: 'new-operator',
          role: 'user',
          must_change_password: true,
        }),
      ),
    )

    render(
      <SessionProvider>
        <SessionProbe />
      </SessionProvider>,
    )

    await waitFor(() => expect(screen.getByLabelText('会话状态')).toHaveTextContent('must-change'))
  })

  it('改密成功清理客户端 Subject 并要求重新登录', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse({
          id: 'user-2',
          username: 'new-operator',
          role: 'user',
          must_change_password: true,
        }),
      )
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    render(
      <SessionProvider>
        <SessionProbe />
      </SessionProvider>,
    )
    await waitFor(() => expect(observedSession?.state.status).toBe('must-change'))

    await act(async () => {
      await observedSession?.changePassword('temporary-password', 'replacement-password')
    })

    expect(observedSession?.state).toEqual({ status: 'anonymous' })
    const request = fetchMock.mock.calls[1]?.[1] as RequestInit
    expect(new Headers(request.headers).get('X-CSRF-Token')).toBe('current-token')
  })

  it('登录先初始化 CSRF，且后续写请求使用服务端轮换后的 Cookie', async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      void init
      const path = String(input)
      if (path.endsWith('/csrf')) {
        document.cookie = 'aig_csrf=bootstrap-token; Path=/'
        return jsonResponse({ csrf_token: 'bootstrap-token' })
      }
      if (path.endsWith('/login')) {
        document.cookie = 'aig_csrf=rotated-token; Path=/'
        return jsonResponse({ must_change_password: true })
      }
      if (path.endsWith('/me')) {
        return jsonResponse({
          id: 'user-2',
          username: 'new-operator',
          role: 'user',
          must_change_password: true,
        })
      }
      return new Response(null, { status: 204 })
    })
    vi.stubGlobal('fetch', fetchMock)

    render(
      <SessionProvider initialState={{ status: 'anonymous' }}>
        <SessionProbe />
      </SessionProvider>,
    )

    await act(async () => {
      await observedSession?.login('new-operator', 'temporary-password')
    })
    await act(async () => {
      await observedSession?.changePassword('temporary-password', 'replacement-password')
    })

    expect(fetchMock.mock.calls.map((call) => String(call[0]))).toEqual([
      '/api/v1/auth/csrf',
      '/api/v1/auth/login',
      '/api/v1/auth/me',
      '/api/v1/auth/change-password',
    ])
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).get('X-CSRF-Token')).toBe('bootstrap-token')
    expect(new Headers(fetchMock.mock.calls[3]?.[1]?.headers).get('X-CSRF-Token')).toBe('rotated-token')
  })

  it('退出使用真实 POST 端点并进入匿名状态', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    render(
      <SessionProvider
        initialState={{
          status: 'authenticated',
          subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false },
        }}
      >
        <SessionProbe />
      </SessionProvider>,
    )

    await act(async () => {
      await observedSession?.logout()
    })

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/auth/logout',
      expect.objectContaining({ method: 'POST', credentials: 'same-origin' }),
    )
    expect(observedSession?.state).toEqual({ status: 'anonymous' })
  })

  it('403 不会清理当前 Subject 或改密上下文', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 403 })))
    const initialState: SessionState = {
      status: 'must-change',
      subject: { id: 'user-2', username: 'new-operator', role: 'user', must_change_password: true },
    }

    render(
      <SessionProvider initialState={initialState}>
        <SessionProbe />
      </SessionProvider>,
    )

    await act(async () => {
      await observedSession?.changePassword('temporary-password', 'replacement-password').catch(() => undefined)
    })

    expect(observedSession?.state).toEqual(initialState)
  })

  it('任意身份请求收到 401 时立即清理当前 Subject', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))

    render(
      <SessionProvider
        initialState={{
          status: 'authenticated',
          subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false },
        }}
      >
        <SessionProbe />
      </SessionProvider>,
    )

    await act(async () => {
      await observedSession?.logout().catch(() => undefined)
    })

    expect(observedSession?.state).toEqual({ status: 'anonymous' })
  })
})

describe('AppProviders', () => {
  it('关闭查询和写请求的自动重试并允许注入隔离客户端', () => {
    const queryClient = createAppQueryClient()

    expect(queryClient.getDefaultOptions().queries?.retry).toBe(false)
    expect(queryClient.getDefaultOptions().mutations?.retry).toBe(false)

    render(
      <AppProviders queryClient={queryClient} sessionInitialState={{ status: 'anonymous' }}>
        <SessionProbe />
      </AppProviders>,
    )
    expect(screen.getByLabelText('会话状态')).toHaveTextContent('anonymous')
  })
})
