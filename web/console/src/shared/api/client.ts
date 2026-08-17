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
const MAX_JSON_BYTES = 2 * 1024 * 1024
const unauthorizedListeners = new Set<() => void>()

export interface ApiRequestPolicy {
  unauthorized?: 'notify' | 'suppress'
}

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

function isJSONContentType(value: string | null): boolean {
  const mime = value?.split(';', 1)[0]?.trim().toLowerCase() ?? ''
  return mime === 'application/json' || /^application\/[a-z0-9!#$&^_.+-]+\+json$/.test(mime)
}

function declaredResponseTooLarge(value: string | null): boolean {
  const length = value?.trim() ?? ''
  if (!/^\d+$/.test(length)) return false
  try {
    return BigInt(length) > BigInt(MAX_JSON_BYTES)
  } catch {
    return false
  }
}

async function readBoundedJSON(response: Response): Promise<unknown> {
  if (!isJSONContentType(response.headers.get('Content-Type'))) {
    throw new ApiError('unexpected-response', response.status)
  }
  if (declaredResponseTooLarge(response.headers.get('Content-Length')) || !response.body) {
    throw new ApiError('unexpected-response', response.status)
  }

  const reader = response.body.getReader()
  const chunks: Uint8Array[] = []
  let total = 0

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    total += value.byteLength
    if (total > MAX_JSON_BYTES) {
      try {
        await reader.cancel()
      } catch {
        // 取消失败不改变固定的响应错误，也不暴露底层流信息。
      }
      throw new ApiError('unexpected-response', response.status)
    }
    chunks.push(value)
  }

  const bytes = new Uint8Array(total)
  let offset = 0
  for (const chunk of chunks) {
    bytes.set(chunk, offset)
    offset += chunk.byteLength
  }

  try {
    const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
    return JSON.parse(text) as unknown
  } catch {
    throw new ApiError('unexpected-response', response.status)
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

export async function apiRequest<T>(
  path: string,
  init: RequestInit = {},
  policy: ApiRequestPolicy = {},
): Promise<T> {
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

  if (!SAFE_METHODS.has(method)) {
    const csrfToken = readCookie(CSRF_COOKIE_NAME)
    if (csrfToken) headers.set(CSRF_HEADER_NAME, csrfToken)
  }

  let response: Response
  try {
    response = await fetch(requestURL.href, {
      ...init,
      method,
      headers,
      credentials: 'same-origin',
    })
  } catch {
    throw new NetworkError()
  }

  if (!response.ok) {
    if (response.status === 401 && policy.unauthorized !== 'suppress') notifyUnauthorized()
    throw apiErrorFromStatus(response.status)
  }

  if (response.status === 204) return undefined as T

  return (await readBoundedJSON(response)) as T
}
