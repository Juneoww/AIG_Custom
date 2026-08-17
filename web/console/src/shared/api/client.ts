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
const MAX_BINARY_BYTES = 50 * 1024 * 1024
const unauthorizedListeners = new Set<(event: UnauthorizedEvent) => void>()

export interface AuthorizationGeneration {
  advance: () => number
  current: () => number
}

export interface UnauthorizedEvent {
  authorization: AuthorizationGeneration
  generation: number
}

export function createAuthorizationGeneration(): AuthorizationGeneration {
  let generation = 0
  return {
    advance: () => {
      generation += 1
      return generation
    },
    current: () => generation,
  }
}

export const defaultAuthorizationGeneration = createAuthorizationGeneration()

export interface ApiRequestPolicy {
  authorization?: AuthorizationGeneration
  expectedStatus?: number
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
    await cancelResponseBody(response)
    throw new ApiError('unexpected-response', response.status)
  }
  if (declaredResponseTooLarge(response.headers.get('Content-Length')) || !response.body) {
    await cancelResponseBody(response)
    throw new ApiError('unexpected-response', response.status)
  }

  let reader: ReadableStreamDefaultReader<Uint8Array>
  try {
    reader = response.body.getReader()
  } catch {
    await cancelResponseBody(response)
    throw new ApiError('unexpected-response', response.status)
  }

  const bytes = new Uint8Array(MAX_JSON_BYTES)
  let total = 0

  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      if (value.byteLength > MAX_JSON_BYTES - total) {
        throw new ApiError('unexpected-response', response.status)
      }
      bytes.set(value, total)
      total += value.byteLength
    }

    const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes.subarray(0, total))
    return JSON.parse(text) as unknown
  } catch {
    try {
      await reader.cancel()
    } catch {
      // 取消失败不改变固定的响应错误，也不暴露底层流信息。
    }
    throw new ApiError('unexpected-response', response.status)
  } finally {
    try {
      reader.releaseLock()
    } catch {
      // 释放失败不改变调用方接收的固定结果。
    }
  }
}

async function cancelResponseBody(response: Response): Promise<void> {
  try {
    await response.body?.cancel()
  } catch {
    // 取消失败不改变固定的响应错误，也不暴露底层流信息。
  }
}

function notifyUnauthorized(event: UnauthorizedEvent) {
  for (const listener of unauthorizedListeners) {
    try {
      listener(event)
    } catch {
      // 单个订阅者失败不能阻止其他会话边界失效。
    }
  }
}

export function subscribeToUnauthorized(listener: (event: UnauthorizedEvent) => void): () => void {
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
  const authorization = policy.authorization ?? defaultAuthorizationGeneration
  const requestGeneration = authorization.current()

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
    if (response.status === 401 && policy.unauthorized !== 'suppress') {
      notifyUnauthorized({ authorization, generation: requestGeneration })
    }
    await cancelResponseBody(response)
    throw apiErrorFromStatus(response.status)
  }

  if (policy.expectedStatus !== undefined && response.status !== policy.expectedStatus) {
    await cancelResponseBody(response)
    throw new ApiError('unexpected-response', response.status)
  }

  if (response.status === 204) return undefined as T

  return (await readBoundedJSON(response)) as T
}

export interface BinaryResponse {
  blob: Blob
  contentDisposition: string | null
}

export async function apiBinaryRequest(
  path: string,
  init: Pick<RequestInit, 'signal'> = {},
  policy: ApiRequestPolicy = {},
): Promise<BinaryResponse> {
  let requestURL: URL
  try {
    requestURL = new URL(path, window.location.origin)
  } catch {
    throw new ApiError('bad-request', 0)
  }
  if (requestURL.origin !== window.location.origin || !['http:', 'https:'].includes(requestURL.protocol)) {
    throw new ApiError('bad-request', 0)
  }

  const authorization = policy.authorization ?? defaultAuthorizationGeneration
  const requestGeneration = authorization.current()
  let response: Response
  try {
    response = await fetch(requestURL.href, { signal: init.signal, method: 'GET', credentials: 'same-origin' })
  } catch {
    throw new NetworkError()
  }
  if (!response.ok) {
    if (response.status === 401 && policy.unauthorized !== 'suppress') {
      notifyUnauthorized({ authorization, generation: requestGeneration })
    }
    await cancelResponseBody(response)
    throw apiErrorFromStatus(response.status)
  }
  if (response.headers.get('Content-Type')?.split(';', 1)[0]?.trim().toLowerCase() !== 'application/octet-stream') {
    await cancelResponseBody(response)
    throw new ApiError('unexpected-response', response.status)
  }
  if (declaredResponseTooLargeFor(response.headers.get('Content-Length'), MAX_BINARY_BYTES) || !response.body) {
    await cancelResponseBody(response)
    throw new ApiError('unexpected-response', response.status)
  }

  let reader: ReadableStreamDefaultReader<Uint8Array>
  try {
    reader = response.body.getReader()
  } catch {
    await cancelResponseBody(response)
    throw new ApiError('unexpected-response', response.status)
  }
  const declaredLength = boundedDeclaredLength(response.headers.get('Content-Length'), MAX_BINARY_BYTES)
  let bytes = new Uint8Array(declaredLength ?? Math.min(64 * 1024, MAX_BINARY_BYTES))
  let total = 0
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      if (value.byteLength > MAX_BINARY_BYTES - total) throw new ApiError('unexpected-response', response.status)
      const required = total + value.byteLength
      if (required > bytes.byteLength) {
        let capacity = Math.max(1, bytes.byteLength)
        while (capacity < required) capacity = Math.min(MAX_BINARY_BYTES, capacity * 2)
        const grown = new Uint8Array(capacity)
        grown.set(bytes.subarray(0, total))
        bytes = grown
      }
      bytes.set(value, total)
      total += value.byteLength
    }
    return {
      blob: new Blob([bytes.subarray(0, total)], { type: 'application/octet-stream' }),
      contentDisposition: response.headers.get('Content-Disposition'),
    }
  } catch {
    try {
      await reader.cancel()
    } catch {
      // 下载流取消失败不改变固定响应错误。
    }
    throw new ApiError('unexpected-response', response.status)
  } finally {
    try {
      reader.releaseLock()
    } catch {
      // 下载流释放失败不向调用方暴露底层信息。
    }
  }
}

function boundedDeclaredLength(value: string | null, maximum: number): number | null {
  const length = value?.trim() ?? ''
  if (!/^\d+$/.test(length)) return null
  const parsed = Number(length)
  return Number.isSafeInteger(parsed) && parsed >= 0 && parsed <= maximum ? parsed : null
}

function declaredResponseTooLargeFor(value: string | null, maximum: number): boolean {
  const length = value?.trim() ?? ''
  if (!/^\d+$/.test(length)) return false
  try {
    return BigInt(length) > BigInt(maximum)
  } catch {
    return false
  }
}
