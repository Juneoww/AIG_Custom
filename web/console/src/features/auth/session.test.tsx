/**
 * 功能：验证身份会话恢复、强制改密、退出与查询重试策略。
 * 实现：通过真实上下文状态机和受控 fetch 响应观察状态迁移。
 * 输入：模拟身份端点响应及会话操作。
 * 输出：状态机与请求合同断言。
 * 依赖：React Testing Library、Vitest、TanStack Query 与会话提供器。
 */
import { QueryClientProvider } from '@tanstack/react-query'
import { act, render, screen, waitFor } from '@testing-library/react'
import { useEffect } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { AppProviders, createAppQueryClient } from '../../app/providers/AppProviders'
import { apiRequest } from '../../shared/api/client'
import {
  confirmPasswordReset,
  SessionProvider,
  type SessionContextValue,
  type SessionState,
  useSession,
} from './session'

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

function renderSession(initialState?: SessionState, queryClient = createAppQueryClient()) {
  const result = render(
    <QueryClientProvider client={queryClient}>
      <SessionProvider initialState={initialState}>
        <SessionProbe />
      </SessionProvider>
    </QueryClientProvider>,
  )
  return { ...result, queryClient }
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, reject, resolve }
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

    const queryClient = createAppQueryClient()
    queryClient.setQueryData(['previous-user'], { private: true })
    renderSession(undefined, queryClient)

    expect(screen.getByLabelText('会话状态')).toHaveTextContent('restoring')
    await waitFor(() => expect(screen.getByLabelText('会话状态')).toHaveTextContent('authenticated'))
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0)
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

    renderSession()

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

    const queryClient = createAppQueryClient()
    queryClient.setQueryData(['previous-user'], { private: true })
    renderSession(undefined, queryClient)
    await waitFor(() => expect(observedSession?.state.status).toBe('must-change'))

    await act(async () => {
      await observedSession?.changePassword('temporary-password', 'replacement-password')
    })

    expect(observedSession?.state).toEqual({ status: 'anonymous' })
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0)
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

    const queryClient = createAppQueryClient()
    queryClient.setQueryData(['previous-user'], { private: true })
    renderSession({ status: 'anonymous' }, queryClient)

    await act(async () => {
      await observedSession?.login('new-operator', 'temporary-password')
    })
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0)
    queryClient.setQueryData(['logged-in-user'], { private: true })
    await act(async () => {
      await observedSession?.changePassword('temporary-password', 'replacement-password')
    })

    expect(fetchMock.mock.calls.map((call) => String(call[0]))).toEqual([
      `${window.location.origin}/api/v1/auth/csrf`,
      `${window.location.origin}/api/v1/auth/login`,
      `${window.location.origin}/api/v1/auth/me`,
      `${window.location.origin}/api/v1/auth/change-password`,
    ])
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).get('X-CSRF-Token')).toBe('bootstrap-token')
    expect(new Headers(fetchMock.mock.calls[3]?.[1]?.headers).get('X-CSRF-Token')).toBe('rotated-token')
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0)
  })

  it('退出使用真实 POST 端点并进入匿名状态', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    const queryClient = createAppQueryClient()
    queryClient.setQueryData(['current-user'], { private: true })
    renderSession(
      {
        status: 'authenticated',
        subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false },
      },
      queryClient,
    )

    await act(async () => {
      await observedSession?.logout()
    })

    expect(fetchMock).toHaveBeenCalledWith(
      `${window.location.origin}/api/v1/auth/logout`,
      expect.objectContaining({ method: 'POST', credentials: 'same-origin' }),
    )
    expect(observedSession?.state).toEqual({ status: 'anonymous' })
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0)
  })

  it('403 不会清理当前 Subject 或改密上下文', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 403 })))
    const initialState: SessionState = {
      status: 'must-change',
      subject: { id: 'user-2', username: 'new-operator', role: 'user', must_change_password: true },
    }

    const queryClient = createAppQueryClient()
    queryClient.setQueryData(['current-user'], { private: true })
    renderSession(initialState, queryClient)

    await act(async () => {
      await observedSession?.changePassword('temporary-password', 'replacement-password').catch(() => undefined)
    })

    expect(observedSession?.state).toEqual(initialState)
    expect(queryClient.getQueryData(['current-user'])).toEqual({ private: true })
  })

  it('受保护请求收到 401 时立即清理 Subject 和身份查询缓存', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))

    const queryClient = createAppQueryClient()
    queryClient.setQueryData(['current-user'], { private: true })
    renderSession(
      {
        status: 'authenticated',
        subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false },
      },
      queryClient,
    )

    await act(async () => {
      await apiRequest('/api/v1/tasks').catch(() => undefined)
    })

    expect(observedSession?.state).toEqual({ status: 'anonymous' })
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0)
  })

  it('改密凭据错误不会把 must-change 会话误清理', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })))
    const initialState: SessionState = {
      status: 'must-change',
      subject: { id: 'user-2', username: 'new-operator', role: 'user', must_change_password: true },
    }
    renderSession(initialState)

    await act(async () => {
      await expect(
        observedSession?.changePassword('invalid-password', 'replacement-password'),
      ).rejects.toMatchObject({ kind: 'unauthenticated' })
    })

    expect(observedSession?.state).toEqual(initialState)
  })

  it('无效重置令牌不会清理页面背后的已有 Subject', async () => {
    vi.stubGlobal(
      'fetch',
      vi
        .fn()
        .mockResolvedValueOnce(jsonResponse({ csrf_token: 'bootstrap-token' }))
        .mockResolvedValueOnce(new Response(null, { status: 401 })),
    )
    const initialState: SessionState = {
      status: 'authenticated',
      subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false },
    }
    renderSession(initialState)

    await act(async () => {
      await expect(
        confirmPasswordReset({ token: 'invalid-token', temporary_password: 'temporary-password' }),
      ).rejects.toMatchObject({ kind: 'unauthenticated' })
    })

    expect(observedSession?.state).toEqual(initialState)
  })

  it.each([
    ['网络错误', () => Promise.reject(new TypeError('network unavailable'))],
    ['服务错误', () => Promise.resolve(new Response(null, { status: 503 }))],
    [
      '异常响应',
      () => Promise.resolve(new Response('untrusted response', { headers: { 'Content-Type': 'text/plain' } })),
    ],
  ])('恢复遇到%s时保留明确错误态，并允许安全重试', async (_label, firstRequest) => {
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(firstRequest)
      .mockResolvedValueOnce(
        jsonResponse({
          id: 'admin-1',
          username: 'security-admin',
          role: 'admin',
          must_change_password: false,
        }),
      )
    vi.stubGlobal('fetch', fetchMock)
    renderSession()

    await waitFor(() => expect(observedSession?.state.status).toBe('restore-error'))
    expect(observedSession?.state).toMatchObject({
      status: 'restore-error',
      message: '无法验证登录状态，请重试。',
    })

    await act(async () => {
      await observedSession?.restore()
    })
    expect(observedSession?.state.status).toBe('authenticated')
  })

  it('较旧恢复结果不会覆盖较新的恢复结果', async () => {
    const older = deferred<Response>()
    const newer = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn().mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise))
    renderSession({ status: 'anonymous' })

    let olderRestore!: Promise<void>
    let newerRestore!: Promise<void>
    act(() => {
      olderRestore = observedSession!.restore()
      newerRestore = observedSession!.restore()
    })
    newer.resolve(
      jsonResponse({ id: 'newer', username: 'newer-user', role: 'user', must_change_password: false }),
    )
    await act(async () => newerRestore)
    older.resolve(
      jsonResponse({ id: 'older', username: 'older-user', role: 'user', must_change_password: false }),
    )
    await act(async () => olderRestore)

    expect(observedSession?.state).toMatchObject({ status: 'authenticated', subject: { id: 'newer' } })
  })

  it('401 通知会使尚未完成的旧恢复结果失效', async () => {
    const pendingRestore = deferred<Response>()
    const fetchMock = vi
      .fn()
      .mockReturnValueOnce(pendingRestore.promise)
      .mockResolvedValueOnce(new Response(null, { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)
    renderSession()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    await act(async () => {
      await apiRequest('/api/v1/tasks').catch(() => undefined)
    })
    pendingRestore.resolve(
      jsonResponse({ id: 'stale', username: 'stale-user', role: 'user', must_change_password: false }),
    )
    await act(async () => {
      await pendingRestore.promise
      await Promise.resolve()
    })

    expect(observedSession?.state).toEqual({ status: 'anonymous' })
  })

  it('401 通知会使尚未完成的旧登录结果失效并清理缓存', async () => {
    const pendingSubject = deferred<Response>()
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(() => {
        document.cookie = 'aig_csrf=bootstrap-token; Path=/'
        return Promise.resolve(jsonResponse({ csrf_token: 'bootstrap-token' }))
      })
      .mockResolvedValueOnce(jsonResponse({ must_change_password: false }))
      .mockReturnValueOnce(pendingSubject.promise)
      .mockResolvedValueOnce(new Response(null, { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)
    const queryClient = createAppQueryClient()
    queryClient.setQueryData(['previous-user'], { private: true })
    renderSession({ status: 'anonymous' }, queryClient)

    let loginPromise!: Promise<void>
    act(() => {
      loginPromise = observedSession!.login('operator', 'temporary-password')
    })
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3))
    await act(async () => {
      await apiRequest('/api/v1/tasks').catch(() => undefined)
    })
    pendingSubject.resolve(
      jsonResponse({ id: 'stale', username: 'stale-user', role: 'user', must_change_password: false }),
    )
    await act(async () => loginPromise)

    expect(observedSession?.state).toEqual({ status: 'anonymous' })
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0)
  })

  it('卸载后旧恢复结果不会提交状态或清理查询缓存', async () => {
    const pendingRestore = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(pendingRestore.promise))
    const queryClient = createAppQueryClient()
    queryClient.setQueryData(['current-user'], { private: true })
    const view = renderSession(undefined, queryClient)
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
    view.unmount()

    pendingRestore.resolve(
      jsonResponse({ id: 'stale', username: 'stale-user', role: 'user', must_change_password: false }),
    )
    await act(async () => {
      await pendingRestore.promise
      await Promise.resolve()
    })

    expect(queryClient.getQueryData(['current-user'])).toEqual({ private: true })
  })

  it('退出失败时保留当前 Subject 并向调用方暴露错误', async () => {
    document.cookie = 'aig_csrf=current-token; Path=/'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 503 })))
    const initialState: SessionState = {
      status: 'authenticated',
      subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false },
    }
    renderSession(initialState)

    await act(async () => {
      await expect(observedSession?.logout()).rejects.toMatchObject({ kind: 'server' })
    })

    expect(observedSession?.state).toEqual(initialState)
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
