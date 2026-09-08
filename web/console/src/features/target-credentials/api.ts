/**
 * 功能：提供基础设施凭据的安全 DTO、目标范围校验和同源 API。
 * 实现：只投影明确的元数据字段，写入通过 CSRF 与版本前置条件；密钥不进入查询缓存。
 * 输入：未知服务端响应、凭据表单或目标 URL；输出：安全视图或固定 API 错误。
 * 依赖：共享 Cookie API 客户端；认证输入仅用于当前显式写请求。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'

export type TargetAuthType = 'bearer' | 'api_key' | 'basic' | 'cookie'
export const authLabels: Record<TargetAuthType, string> = { bearer: 'Bearer Token', api_key: 'API Key', basic: 'Basic 用户名和密码', cookie: 'Cookie' }
export interface TargetCredential {
  id: string; name: string; origin: string; auth_type: TargetAuthType; header_name: string
  revision: number; disabled: boolean; allow_insecure_http: boolean; created_at: string; updated_at: string
}
export interface CredentialInput {
  name: string; origin: string; auth_type: TargetAuthType; header_name?: string
  username?: string; secret: string; disabled: boolean
}
const base = '/api/v1/platform/target-credentials'
const malformed = () => new ApiError('unexpected-response', 200)

export function normalizeCredentialOrigin(value: string): string | undefined {
  try {
    const url = new URL(value)
    if (!/^https?:\/\//i.test(value) || /\s|\\/.test(value) || url.username || url.password || url.search || url.hash || !['', '/'].includes(url.pathname)) return undefined
    return url.origin
  } catch { return undefined }
}
export function targetURLsMatch(origin: string, content: string): boolean {
  const normalizedOrigin = normalizeCredentialOrigin(origin)
  if (!normalizedOrigin) return false
  const targets = content.trim().split(/\s+/).filter(Boolean)
  return targets.length > 0 && targets.every((target) => {
    try {
      const url = new URL(target)
      return /^https?:\/\//i.test(target) && !target.includes('\\') && !url.username && !url.password && !url.hash && url.origin === normalizedOrigin
    } catch { return false }
  })
}
export function parseCredential(value: unknown): TargetCredential {
  if (!value || typeof value !== 'object') throw malformed()
  const item = value as Record<string, unknown>
  // HTTP/HTTPS 均直接可用；兼容字段只按实际协议投影，不作为额外的用户开关。
  if (typeof item.id !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(item.id) ||
      typeof item.name !== 'string' || !item.name || item.name.length > 160 ||
      (item.allow_insecure_http !== undefined && typeof item.allow_insecure_http !== 'boolean') ||
      typeof item.origin !== 'string' || item.origin.length > 2048 || !normalizeCredentialOrigin(item.origin) ||
      typeof item.auth_type !== 'string' || !Object.hasOwn(authLabels, item.auth_type) ||
      typeof item.header_name !== 'string' || item.header_name.length > 128 ||
      typeof item.disabled !== 'boolean' || !Number.isSafeInteger(item.revision) || (item.revision as number) < 1 ||
      typeof item.created_at !== 'string' || !Number.isFinite(Date.parse(item.created_at)) ||
      typeof item.updated_at !== 'string' || !Number.isFinite(Date.parse(item.updated_at))) throw malformed()
  return { id: item.id, name: item.name, origin: item.origin, allow_insecure_http: /^http:\/\//i.test(item.origin), auth_type: item.auth_type as TargetAuthType, header_name: item.header_name, disabled: item.disabled, revision: item.revision as number, created_at: item.created_at, updated_at: item.updated_at }
}
export async function fetchCredentials(signal?: AbortSignal): Promise<TargetCredential[]> {
  const response = await apiRequest<unknown>(base, { signal })
  if (!response || typeof response !== 'object' || !Array.isArray((response as { items?: unknown }).items)) throw malformed()
  return (response as { items: unknown[] }).items.map(parseCredential)
}
export async function fetchCredential(id: string, signal?: AbortSignal) {
  return parseCredential(await apiRequest(`${base}/${encodeURIComponent(id)}`, { signal }))
}
export async function saveCredential(input: CredentialInput, current?: TargetCredential, signal?: AbortSignal) {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  if (current) headers['If-Match'] = `"${current.revision}"`
  return parseCredential(await apiRequest(current ? `${base}/${encodeURIComponent(current.id)}` : base, {
    method: current ? 'PUT' : 'POST', headers, body: JSON.stringify(input), signal,
  }, { expectedStatus: current ? 200 : 201 }))
}
export async function deleteCredential(current: TargetCredential, signal?: AbortSignal) {
  await apiRequest(`${base}/${encodeURIComponent(current.id)}`, { method: 'DELETE', headers: { 'If-Match': `"${current.revision}"` }, signal }, { expectedStatus: 204 })
}
export function credentialError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 409) return '凭据已更新，请刷新后重试。'
    if (error.status === 400) return '请检查 HTTP 或 HTTPS 目标地址、认证类型和密钥内容。'
    if (error.status === 403 || error.status === 404) return '凭据不存在或无权访问。'
  }
  return '操作结果未确认，请刷新列表核对后再操作。'
}
