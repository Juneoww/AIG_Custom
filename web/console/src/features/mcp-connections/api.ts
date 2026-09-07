/**
 * 功能：封装 MCP 连接配置和可选连接的专属安全合同。
 * 实现：严格校验字段后白名单重建 DTO；写操作只在内存保留同次提交以支持显式幂等重试。
 * 输入：连接表单、版本与同源响应；输出：安全摘要、配置标志与写入确认；不缓存秘密或错误原文。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError, NetworkError } from '../../shared/api/errors'

export type Transport = 'auto' | 'http' | 'sse'
export type AuthenticationKind = 'none' | 'bearer' | 'api_key_header' | 'custom_headers'
export interface ConnectionSummary {
  id: string; name: string; description: string; scope: 'private' | 'global'; current_version: number; resource_revision: string
  enabled: boolean; transport: Transport; detected_transport?: Transport; probe_status: 'not_tested' | 'passed' | 'failed'
  authentication_kind: AuthenticationKind | 'unknown'; created_at: string; updated_at: string
}
export interface ConnectionDetail extends ConnectionSummary {
  server_url: string; authentication_header_name?: string; authentication_configured: boolean; headers: { name: string; configured: boolean }[]
}
export interface ConnectionOption { connection_id: string; connection_version: number; name: string; scope: 'private' | 'global'; transport: Transport; authentication_kind: AuthenticationKind }
export interface ConnectionWrite { name?: string; description?: string; server_url?: string; transport?: Transport; authentication?: { kind: AuthenticationKind; header_name?: string; secret?: string }; headers?: { name: string; value?: string }[]; enabled?: boolean }
export interface ConnectionResult { id: string; current_version: number; resource_revision: string; status: string }
export interface MCPMutation<T> { submit: (signal?: AbortSignal) => Promise<T>; clear: () => void }
class MCPUnknownResponseError extends Error {
  constructor() { super('MCP 操作结果尚未确认。') }
}
// 网关或服务端 5xx 不能证明事务未提交；与断网一样保留原键，避免重复扫描。
export function isMCPUnknownOutcome(error: unknown): boolean { return error instanceof NetworkError || error instanceof MCPUnknownResponseError || (error instanceof ApiError && error.status >= 500 && error.status <= 599) }
export const connectionBase = '/api/v1/platform/mcp-connection-configs'
export function invalidResponse(): never { throw new ApiError('unexpected-response', 200) }
export function record(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return invalidResponse()
  return value as Record<string, unknown>
}
export function textField(value: unknown, maximum = 256, empty = false): string {
  if (typeof value !== 'string' || (!empty && !value) || Array.from(value).length > maximum) return invalidResponse()
  return value
}
export function opaqueID(value: unknown): string {
  const id = textField(value)
  if (!/^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/.test(id) || id === '.' || id === '..') return invalidResponse()
  return id
}
export function integer(value: unknown, minimum = 1, maximum = Number.MAX_SAFE_INTEGER): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum) return invalidResponse()
  return value as number
}
export function enumeration<T extends string>(value: unknown, choices: readonly T[]): T {
  if (typeof value !== 'string' || !choices.includes(value as T)) return invalidResponse()
  return value as T
}
export function booleanField(value: unknown): boolean { if (typeof value !== 'boolean') return invalidResponse(); return value }
export function dateField(value: unknown): string {
  const result = textField(value, 64)
  if (!/^\d{4}-\d{2}-\d{2}T/.test(result) || !Number.isFinite(Date.parse(result))) return invalidResponse()
  return result
}
export function arrayField<T>(value: unknown, parse: (item: unknown) => T, maximum = 1000): T[] {
  if (!Array.isArray(value) || value.length > maximum) return invalidResponse()
  return value.map(parse)
}
const transports = ['auto', 'http', 'sse'] as const
const authenticationKinds = ['none', 'bearer', 'api_key_header', 'custom_headers'] as const
function resourceRevision(value: unknown): string {
  const revision = textField(value, 32)
  if (!/^[1-9][0-9]*$/.test(revision)) return invalidResponse()
  return revision
}
function parseSummary(value: unknown): ConnectionSummary {
  const s = record(value)
  return {
    id: opaqueID(s.id), name: textField(s.name, 80), description: textField(s.description ?? '', 500, true), scope: enumeration(s.scope, ['private', 'global']),
    current_version: integer(s.current_version), resource_revision: resourceRevision(s.resource_revision), enabled: booleanField(s.enabled), transport: enumeration(s.transport, transports),
    ...(s.detected_transport ? { detected_transport: enumeration(s.detected_transport, transports) } : {}),
    probe_status: enumeration(s.probe_status, ['not_tested', 'passed', 'failed']), authentication_kind: enumeration(s.authentication_kind, [...authenticationKinds, 'unknown']),
    created_at: dateField(s.created_at), updated_at: dateField(s.updated_at),
  }
}
export function parseConnectionList(value: unknown): ConnectionSummary[] { return arrayField(record(value).items, parseSummary) }
export function parseConnectionDetail(value: unknown): ConnectionDetail {
  const s = record(value)
  return { ...parseSummary(s), server_url: textField(s.server_url, 4096), authentication_configured: booleanField(s.authentication_configured),
    ...(s.authentication_header_name ? { authentication_header_name: textField(s.authentication_header_name, 128) } : {}),
    headers: arrayField(s.headers ?? [], (item) => { const h = record(item); return { name: textField(h.name, 128), configured: booleanField(h.configured) } }, 10) }
}
export function parseConnectionOptions(value: unknown): ConnectionOption[] {
  return arrayField(record(value).items, (item) => {
    const s = record(item)
    return { connection_id: opaqueID(s.connection_id), connection_version: integer(s.connection_version), name: textField(s.name, 80), scope: enumeration(s.scope, ['private', 'global']), transport: enumeration(s.transport, transports), authentication_kind: enumeration(s.authentication_kind, authenticationKinds) }
  })
}
function parseResult(value: unknown): ConnectionResult {
  const s = record(value)
  return { id: opaqueID(s.id), current_version: integer(s.current_version), resource_revision: resourceRevision(s.resource_revision), status: enumeration(s.status, ['created', 'updated', 'enabled', 'disabled', 'passed', 'failed']) }
}
export function createMCPMutation<T>(path: string, method: string, input: unknown, parse: (value: unknown) => T, revision?: string): MCPMutation<T> {
  let body: string | undefined = JSON.stringify(input)
  let key: string | undefined = crypto.randomUUID()
  return {
    async submit(signal) {
      if (!key || body === undefined) throw new ApiError('bad-request', 0)
      const headers = { 'Content-Type': 'application/json', 'Idempotency-Key': key, ...(revision === undefined ? {} : { 'If-Match': `"${resourceRevision(revision)}"` }) }
      try {
        return parse(await apiRequest<unknown>(path, { method, signal, body, headers }))
      } catch (error) {
        // 请求已获得成功状态但无法确认安全 DTO 时，必须保留同次提交；本地校验不进入此边界。
        if (error instanceof ApiError && error.kind === 'unexpected-response' && error.status >= 200 && error.status < 300) throw new MCPUnknownResponseError()
        throw error
      }
    },
    clear() { body = undefined; key = undefined },
  }
}
export function createConnectionMutation(method: 'POST' | 'PATCH' | 'TEST', id: string | undefined, input: ConnectionWrite, revision?: string): MCPMutation<ConnectionResult> {
  const path = id ? `${connectionBase}/${encodeURIComponent(opaqueID(id))}${method === 'TEST' ? '/test' : ''}` : connectionBase
  return createMCPMutation(path, method === 'TEST' ? 'POST' : method, input, parseResult, revision)
}
export async function fetchConnections(signal?: AbortSignal) { return parseConnectionList(await apiRequest(connectionBase, { signal })) }
export async function fetchConnection(id: string, signal?: AbortSignal) { return parseConnectionDetail(await apiRequest(`${connectionBase}/${encodeURIComponent(opaqueID(id))}`, { signal })) }
export async function fetchConnectionOptions(signal?: AbortSignal) { return parseConnectionOptions(await apiRequest('/api/v1/platform/mcp-connection-options', { signal })) }
export function mcpErrorMessage(error: unknown): string {
  if (isMCPUnknownOutcome(error)) return '操作结果尚未确认。请检查网络，显式重试将复用同一幂等键。'
  if (error instanceof ApiError && error.status === 409) return '配置版本或提交状态已变化，请刷新配置并检查提交后重试。'
  if (error instanceof ApiError && error.status === 403) return '当前身份无权执行此操作。'
  if (error instanceof ApiError && error.status === 400) return '请求内容无效，请检查所填信息。'
  return '操作未完成，请稍后重试。'
}
