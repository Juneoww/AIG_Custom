/**
 * 功能：定义控制台可安全展示的固定 API 错误类型。
 * 实现：仅按网络状态归类，不携带请求路径、请求体、令牌或响应原文。
 * 输入：HTTP 状态码或网络失败。
 * 输出：可供会话和页面判断的泛化错误实例。
 * 依赖：浏览器 Error 基类。
 */
export type ApiErrorKind =
  | 'bad-request'
  | 'unauthenticated'
  | 'forbidden'
  | 'not-found'
  | 'insecure-transport'
  | 'server'
  | 'unexpected-response'

const ERROR_MESSAGES: Record<ApiErrorKind, string> = {
  'bad-request': '请求内容无效。',
  unauthenticated: '登录状态无效，请重新登录。',
  forbidden: '当前操作未通过安全校验。',
  'not-found': '请求的资源不存在。',
  'insecure-transport': '当前连接不安全，请通过 HTTPS 访问。',
  server: '服务暂时不可用，请稍后重试。',
  'unexpected-response': '服务返回了无法识别的响应。',
}

export class ApiError extends Error {
  readonly name = 'ApiError'

  constructor(
    readonly kind: ApiErrorKind,
    readonly status: number,
  ) {
    super(ERROR_MESSAGES[kind])
  }
}

export class NetworkError extends Error {
  readonly name = 'NetworkError'

  constructor() {
    super('无法连接到服务，请检查网络后重试。')
  }
}

export function apiErrorFromStatus(status: number): ApiError {
  if (status === 400) return new ApiError('bad-request', status)
  if (status === 401) return new ApiError('unauthenticated', status)
  if (status === 403) return new ApiError('forbidden', status)
  if (status === 404) return new ApiError('not-found', status)
  if (status === 426) return new ApiError('insecure-transport', status)
  if (status >= 500) return new ApiError('server', status)
  return new ApiError('unexpected-response', status)
}
