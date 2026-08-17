/**
 * 功能：提供同源 Cookie 会话的安全 fetch 客户端。
 * 实现：写请求在调用时读取双提交 Cookie，响应错误仅按固定类别暴露。
 * 输入：同源 API 路径、方法、请求头与可选请求体。
 * 输出：解码后的成功 DTO，或不含敏感上下文的固定错误。
 * 依赖：浏览器 fetch、Headers、document.cookie 与共享错误类型。
 */
import { ApiError, NetworkError, apiErrorFromStatus } from './errors'

const CSRF_COOKIE_NAME = 'aig_csrf'
const CSRF_HEADER_NAME = 'X-CSRF-Token'
const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])
const unauthorizedListeners = new Set<() => void>()

function readCookie(name: string): string | undefined {
  const prefix = `${name}=`
  const pair = document.cookie
    .split(';')
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix))

  if (!pair) return undefined
  const value = pair.slice(prefix.length)
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

function isAllowlistedErrorEnvelope(value: unknown): value is { error: string } {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const keys = Object.keys(value)
  return keys.length === 1 && keys[0] === 'error' && typeof (value as { error?: unknown }).error === 'string'
}

async function consumeSafeErrorEnvelope(response: Response): Promise<void> {
  if (!response.headers.get('Content-Type')?.toLowerCase().includes('application/json')) return
  try {
    const value: unknown = await response.json()
    if (isAllowlistedErrorEnvelope(value)) {
      return
    }
  } catch {
    return
  }
}

function notifyUnauthorized() {
  for (const listener of unauthorizedListeners) listener()
}

export function subscribeToUnauthorized(listener: () => void): () => void {
  unauthorizedListeners.add(listener)
  return () => {
    unauthorizedListeners.delete(listener)
  }
}

export async function apiRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  let requestURL: URL
  try {
    requestURL = new URL(path, window.location.origin)
  } catch {
    throw new ApiError('bad-request', 0)
  }
  if (requestURL.origin !== window.location.origin || !['http:', 'https:'].includes(requestURL.protocol)) {
    throw new ApiError('bad-request', 0)
  }

  const method = (init.method ?? 'GET').toUpperCase()
  const headers = new Headers(init.headers)

  if (!SAFE_METHODS.has(method) && init.body !== undefined && init.body !== null) {
    const csrfToken = readCookie(CSRF_COOKIE_NAME)
    if (csrfToken) headers.set(CSRF_HEADER_NAME, csrfToken)
  }

  let response: Response
  try {
    response = await fetch(path, {
      ...init,
      method,
      headers,
      credentials: 'same-origin',
    })
  } catch {
    throw new NetworkError()
  }

  if (!response.ok) {
    await consumeSafeErrorEnvelope(response)
    if (response.status === 401) notifyUnauthorized()
    throw apiErrorFromStatus(response.status)
  }

  if (response.status === 204) return undefined as T

  try {
    return (await response.json()) as T
  } catch {
    throw new ApiError('unexpected-response', response.status)
  }
}
