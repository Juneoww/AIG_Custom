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
  window.history.replaceState({}, '', '/')
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

  it('GET 不附加 CSRF，而无请求体 POST 仍携带当前 CSRF 并处理 204', async () => {
    document.cookie = 'aig_csrf=present-token; Path=/'
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ id: 'subject-1' }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await apiRequest('/api/v1/auth/me')
    await expect(apiRequest<void>('/api/v1/auth/logout', { method: 'POST' })).resolves.toBeUndefined()

    expect(new Headers((fetchMock.mock.calls[0]?.[1] as RequestInit).headers).has('X-CSRF-Token')).toBe(false)
    expect(new Headers((fetchMock.mock.calls[1]?.[1] as RequestInit).headers).get('X-CSRF-Token')).toBe(
      'present-token',
    )
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

  it.each(['//outside.example/api', 'javascript:alert(1)', 'data:application/json,{}'])(
    '拒绝非站内 HTTP 路径 %s',
    async (path) => {
      const fetchMock = vi.fn()
      vi.stubGlobal('fetch', fetchMock)

      await expect(apiRequest(path)).rejects.toMatchObject({ kind: 'bad-request' })
      expect(fetchMock).not.toHaveBeenCalled()
    },
  )

  it('从嵌套路由解析相同站内 URL 并把已验证 href 交给 fetch', async () => {
    window.history.replaceState({}, '', '/tasks/active?page=2')
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ok: true }))
    vi.stubGlobal('fetch', fetchMock)

    await apiRequest('api/v1/auth/me')

    expect(fetchMock.mock.calls[0]?.[0]).toBe(`${window.location.origin}/api/v1/auth/me`)
  })

  it('拒绝非 JSON 成功响应', async () => {
    let cancelled = false
    const body = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('{"ok":true}'))
      },
      cancel() {
        cancelled = true
      },
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(body, { headers: { 'Content-Type': 'text/plain' } })))

    await expect(apiRequest('/api/v1/auth/me')).rejects.toMatchObject({ kind: 'unexpected-response' })
    expect(cancelled).toBe(true)
  })

  it('在读取前拒绝并取消声明超过 2MiB 的 JSON', async () => {
    let cancelled = false
    const body = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('{"ok":true}'))
      },
      cancel() {
        cancelled = true
      },
    })
    const response = new Response(body, {
      headers: {
        'Content-Length': String(2 * 1024 * 1024 + 1),
        'Content-Type': 'application/json',
      },
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))

    await expect(apiRequest('/api/v1/auth/me')).rejects.toMatchObject({ kind: 'unexpected-response' })
    expect(cancelled).toBe(true)
  })

  it('取消累计超过 2MiB 的分块 JSON 响应', async () => {
    const chunk = new TextEncoder().encode(`{"value":"${'x'.repeat(1024 * 1024)}`)
    let cancelled = false
    let chunkIndex = 0
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (chunkIndex < 2) {
          controller.enqueue(chunk)
          chunkIndex += 1
          return
        }
        controller.enqueue(new TextEncoder().encode('"}'))
        controller.close()
      },
      cancel() {
        cancelled = true
      },
    })
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(body, { headers: { 'Content-Type': 'application/json' } })),
    )

    await expect(apiRequest('/api/v1/auth/me')).rejects.toMatchObject({ kind: 'unexpected-response' })
    expect(cancelled).toBe(true)
  })

  it('接受 application/*+json 成功响应', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('{"kind":"ok"}', { headers: { 'Content-Type': 'application/problem+json; charset=utf-8' } }),
      ),
    )

    await expect(apiRequest<{ kind: string }>('/api/v1/auth/me')).resolves.toEqual({ kind: 'ok' })
  })

  it('读取器拒绝时取消并释放锁，且只返回固定错误', async () => {
    const reader = {
      cancel: vi.fn().mockResolvedValue(undefined),
      read: vi.fn().mockRejectedValue(new Error('private stream failure')),
      releaseLock: vi.fn(),
    }
    const response = jsonResponse({ ok: true })
    Object.defineProperty(response, 'body', {
      configurable: true,
      value: { getReader: () => reader },
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))

    const error = await apiRequest('/api/v1/auth/me').catch((caught: unknown) => caught)

    expect(error).toMatchObject({ kind: 'unexpected-response' })
    expect(String(error)).not.toContain('private stream failure')
    expect(reader.cancel).toHaveBeenCalledTimes(1)
    expect(reader.releaseLock).toHaveBeenCalledTimes(1)
  })

  it('大量单字节分块仍在固定上限缓冲区内完成 JSON 解码', async () => {
    const encoded = new TextEncoder().encode(JSON.stringify({ value: 'x'.repeat(4096) }))
    let offset = 0
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (offset >= encoded.byteLength) {
          controller.close()
          return
        }
        controller.enqueue(encoded.subarray(offset, offset + 1))
        offset += 1
      },
    })
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(body, { headers: { 'Content-Type': 'application/json' } })),
    )

    await expect(apiRequest<{ value: string }>('/api/v1/auth/me')).resolves.toEqual({ value: 'x'.repeat(4096) })
  })

  it('错误响应先通知受保护 401，再取消且不读取响应体', async () => {
    const order: string[] = []
    const body = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('{"error":"private-detail"}'))
      },
      cancel() {
        order.push('cancel')
      },
    })
    const response = new Response(body, { status: 401, headers: { 'Content-Type': 'application/json' } })
    const onUnauthorized = vi.fn()
    const unsubscribe = subscribeToUnauthorized(() => {
      order.push('notify')
      onUnauthorized()
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))

    await expect(apiRequest('/api/v1/platform/tasks')).rejects.toMatchObject({ kind: 'unauthenticated' })

    expect(onUnauthorized).toHaveBeenCalledTimes(1)
    expect(order).toEqual(['notify', 'cancel'])
    unsubscribe()
  })
})
