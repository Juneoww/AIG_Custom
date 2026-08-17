/**
 * 功能：验证身份请求的同源、CSRF、错误脱敏与无重试边界。
 * 实现：以可观察的 fetch 替身记录请求，不记录任何凭据值。
 * 输入：模拟 Cookie、响应状态与身份 API 调用。
 * 输出：客户端安全行为断言。
 * 依赖：Vitest、jsdom 与共享 API 客户端。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError, NetworkError } from '../../shared/api/errors'
import { apiRequest, subscribeToUnauthorized } from '../../shared/api/client'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'aig_csrf=; Max-Age=0; Path=/'
})

describe('apiRequest', () => {
  it('每次写请求都从当前 Cookie 读取 CSRF 且使用同源凭据', async () => {
    document.cookie = 'aig_csrf=first-token; Path=/'
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ ok: true })))
    vi.stubGlobal('fetch', fetchMock)

    await apiRequest('/api/v1/auth/login', { method: 'POST', body: '{}' })
    document.cookie = 'aig_csrf=rotated-token; Path=/'
    await apiRequest('/api/v1/auth/change-password', { method: 'POST', body: '{}' })

    expect(fetchMock).toHaveBeenCalledTimes(2)
    const first = fetchMock.mock.calls[0]?.[1] as RequestInit
    const second = fetchMock.mock.calls[1]?.[1] as RequestInit
    expect(first.credentials).toBe('same-origin')
    expect(new Headers(first.headers).get('X-CSRF-Token')).toBe('first-token')
    expect(new Headers(second.headers).get('X-CSRF-Token')).toBe('rotated-token')
  })

  it('GET 与无请求体调用不附加 CSRF 并正确处理 204', async () => {
    document.cookie = 'aig_csrf=present-token; Path=/'
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ id: 'subject-1' }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await apiRequest('/api/v1/auth/me')
    await expect(apiRequest<void>('/api/v1/auth/logout', { method: 'POST' })).resolves.toBeUndefined()

    for (const call of fetchMock.mock.calls) {
      expect(new Headers((call[1] as RequestInit).headers).has('X-CSRF-Token')).toBe(false)
    }
  })

  it('只接受白名单错误外形且不会把响应原文写入错误', async () => {
    const secret = 'sensitive-server-detail'
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ error: secret, debug: 'private' }, 500)))

    const error = await apiRequest('/api/v1/auth/me').catch((caught: unknown) => caught)

    expect(error).toBeInstanceOf(ApiError)
    expect(String(error)).not.toContain(secret)
    expect(JSON.stringify(error)).not.toContain(secret)
    expect((error as ApiError).kind).toBe('server')
  })

  it('401 通知会话失效而 403 仅返回禁止错误', async () => {
    const onUnauthorized = vi.fn()
    const unsubscribe = subscribeToUnauthorized(onUnauthorized)
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response(null, { status: 401 }))
      .mockResolvedValueOnce(new Response(null, { status: 403 }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(apiRequest('/api/v1/auth/me')).rejects.toMatchObject({ kind: 'unauthenticated' })
    await expect(apiRequest('/api/v1/auth/change-password', { method: 'POST', body: '{}' })).rejects.toMatchObject({
      kind: 'forbidden',
    })

    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    unsubscribe()
  })

  it('网络失败不会自动重放写请求', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError('failed to fetch'))
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      apiRequest('/api/v1/auth/login', { method: 'POST', body: '{"username":"operator"}' }),
    ).rejects.toBeInstanceOf(NetworkError)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('拒绝把身份请求发送到跨源地址', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    await expect(apiRequest('https://outside.example/api/v1/auth/me')).rejects.toMatchObject({
      kind: 'bad-request',
    })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
