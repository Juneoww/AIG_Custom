/**
 * 功能：适配受治理用户、审计、品牌与系统状态的真实 HTTP 合同。
 * 实现：所有响应先按 unknown 解析，再投影为最小安全 DTO；审计 metadata、请求标识与 IP 不进入浏览器状态。
 * 输入：服务端 JSON、分页参数、当前 CSRF Cookie 与管理员写请求。
 * 输出：可呈现的安全领域 DTO，或不含响应原文的固定 API 错误。
 * 依赖：共享同源 API 客户端与浏览器 AbortSignal。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import type { SubjectRole } from '../../shared/api/types'

export interface UserView {
  id: string
  username: string
  role: SubjectRole
  active: boolean
  must_change_password: boolean
  created_at: string
  updated_at: string
}

export interface UserPage {
  items: UserView[]
  total: number
  page: number
  page_size: number
}

export interface AuditEventView {
  id: string
  occurred_at: string
  actor_user_id?: string
  actor_username?: string
  actor_role?: string
  action: string
  resource_type?: string
  resource_id?: string
  outcome: 'pending' | 'success' | 'failure'
}

export interface AuditPage {
  items: AuditEventView[]
  total: number
  page: number
  page_size: number
}

export interface PageQuery {
  page: number
  pageSize: number
}

export interface CreateUserInput {
  username: string
  password: string
  role: SubjectRole
}

export interface BrandConfigView {
  product_name: string
  primary_color: string
  logo: string
  logo_mime: '' | 'image/png' | 'image/jpeg'
  watermark: string
}

export interface SystemStatusView {
  running: boolean
  success?: boolean
  started_at?: string
  finished_at?: string
  message: string
  files_updated: number
  ref?: string
}

export interface SafeVersionView {
  version: string
  commit: string
  build_time: string
}

const OPAQUE_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/
const ROLES = new Set<SubjectRole>(['admin', 'auditor', 'user'])
const OUTCOMES = new Set<AuditEventView['outcome']>(['pending', 'success', 'failure'])

function recordOf(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null ? value as Record<string, unknown> : undefined
}

function boundedString(value: unknown, maximum: number, allowEmpty = false): string | undefined {
  return typeof value === 'string' && value.length <= maximum && (allowEmpty || value.length > 0) ? value : undefined
}

function safeOpaqueID(value: unknown): string | undefined {
  const id = boundedString(value, 256)
  return id && id !== '.' && id !== '..' && OPAQUE_ID.test(id) ? id : undefined
}

function safeDate(value: unknown): string | undefined {
  const date = boundedString(value, 64)
  return date && Number.isFinite(Date.parse(date)) ? date : undefined
}

function safeCount(value: unknown, maximum: number): number | undefined {
  return Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= maximum ? value as number : undefined
}

function optionalText(value: unknown, maximum = 256): string | undefined | null {
  return value === undefined || value === '' ? undefined : boundedString(value, maximum) ?? null
}

function parseUser(value: unknown): UserView | undefined {
  const source = recordOf(value)
  const id = safeOpaqueID(source?.id)
  const username = boundedString(source?.username, 256)
  const role = source?.role as SubjectRole | undefined
  const createdAt = safeDate(source?.created_at)
  const updatedAt = safeDate(source?.updated_at)
  if (!id || !username || !role || !ROLES.has(role) || typeof source?.active !== 'boolean' ||
    typeof source?.must_change_password !== 'boolean' || !createdAt || !updatedAt) return undefined
  return { id, username, role, active: source.active, must_change_password: source.must_change_password, created_at: createdAt, updated_at: updatedAt }
}

function parseAuditEvent(value: unknown): AuditEventView | undefined {
  const source = recordOf(value)
  const id = safeOpaqueID(source?.id)
  const occurredAt = safeDate(source?.occurred_at)
  const action = boundedString(source?.action, 256)
  const outcome = source?.outcome as AuditEventView['outcome'] | undefined
  const actorUserID = optionalText(source?.actor_user_id)
  const actorUsername = optionalText(source?.actor_username)
  const actorRole = optionalText(source?.actor_role, 32)
  const resourceType = optionalText(source?.resource_type)
  const resourceID = optionalText(source?.resource_id)
  if (!id || !occurredAt || !action || !outcome || !OUTCOMES.has(outcome) || actorUserID === null || actorUsername === null ||
    actorRole === null || resourceType === null || resourceID === null) return undefined
  return {
    id, occurred_at: occurredAt, action, outcome,
    ...(actorUserID ? { actor_user_id: actorUserID } : {}),
    ...(actorUsername ? { actor_username: actorUsername } : {}),
    ...(actorRole ? { actor_role: actorRole } : {}),
    ...(resourceType ? { resource_type: resourceType } : {}),
    ...(resourceID ? { resource_id: resourceID } : {}),
  }
}

function parsePage<T>(value: unknown, parser: (item: unknown) => T | undefined): { items: T[]; total: number; page: number; page_size: number } {
  const source = recordOf(value)
  const total = safeCount(source?.total, Number.MAX_SAFE_INTEGER)
  const page = safeCount(source?.page, 1_000)
  const pageSize = safeCount(source?.page_size, 100)
  if (!Array.isArray(source?.items) || source.items.length > 100 || total === undefined || !page || !pageSize) {
    throw new ApiError('unexpected-response', 200)
  }
  const items = source.items.map(parser)
  if (!items.every((item): item is T => item !== undefined)) throw new ApiError('unexpected-response', 200)
  return { items, total, page, page_size: pageSize }
}

function pageSearch(query: PageQuery, filters: Record<string, string | undefined> = {}): string {
  if (!safeCount(query.page, 1_000) || !safeCount(query.pageSize, 100)) throw new ApiError('bad-request', 0)
  const values = new URLSearchParams({ page: String(query.page), page_size: String(query.pageSize) })
  for (const [key, value] of Object.entries(filters)) if (value) values.set(key, value)
  return values.toString()
}

function jsonBody(body: object, method: 'POST' | 'PUT', signal?: AbortSignal): RequestInit {
  return { method, signal, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
}

export async function fetchUsers(query: PageQuery, signal?: AbortSignal): Promise<UserPage> {
  return parsePage(await apiRequest<unknown>(`/api/v1/platform/admin/users?${pageSearch(query)}`, { signal }), parseUser)
}

export async function createUser(input: CreateUserInput, signal?: AbortSignal): Promise<UserView> {
  if (!boundedString(input.username, 256) || !input.password || !ROLES.has(input.role)) throw new ApiError('bad-request', 0)
  const user = parseUser(await apiRequest<unknown>(
    '/api/v1/platform/admin/users', jsonBody(input, 'POST', signal), { expectedStatus: 201 },
  ))
  if (!user) throw new ApiError('unexpected-response', 201)
  return user
}

export async function setUserRole(id: string, role: SubjectRole, signal?: AbortSignal): Promise<void> {
  const safeID = safeOpaqueID(id)
  if (!safeID || !ROLES.has(role)) throw new ApiError('bad-request', 0)
  await apiRequest<void>(`/api/v1/platform/admin/users/${encodeURIComponent(safeID)}/role`, jsonBody({ role }, 'PUT', signal), { expectedStatus: 204 })
}

export async function setUserActive(id: string, active: boolean, signal?: AbortSignal): Promise<void> {
  const safeID = safeOpaqueID(id)
  if (!safeID) throw new ApiError('bad-request', 0)
  await apiRequest<void>(`/api/v1/platform/admin/users/${encodeURIComponent(safeID)}/active`, jsonBody({ active }, 'PUT', signal), { expectedStatus: 204 })
}

export async function requestPasswordReset(id: string, signal?: AbortSignal): Promise<void> {
  const safeID = safeOpaqueID(id)
  if (!safeID) throw new ApiError('bad-request', 0)
  await apiRequest<void>(`/api/v1/platform/admin/users/${encodeURIComponent(safeID)}/password-reset`, { method: 'POST', signal }, { expectedStatus: 204 })
}

export async function fetchAuditEvents(query: PageQuery, filters: { action?: string; actorUserID?: string; resourceType?: string; resourceID?: string } = {}, signal?: AbortSignal): Promise<AuditPage> {
  const search = pageSearch(query, {
    action: filters.action, actor_user_id: filters.actorUserID, resource_type: filters.resourceType, resource_id: filters.resourceID,
  })
  return parsePage(await apiRequest<unknown>(`/api/v1/platform/admin/audit-events?${search}`, { signal }), parseAuditEvent)
}

export function parseBrandConfig(value: unknown): BrandConfigView {
  const source = recordOf(value)
  const productName = boundedString(source?.product_name, 128)
  const primaryColor = boundedString(source?.primary_color, 7)
  const logo = boundedString(source?.logo, 1_400_000, true)
  const logoMIME = source?.logo_mime
  const watermark = source?.watermark === undefined ? '' : boundedString(source.watermark, 64, true)
  if (!productName || !/^#[0-9A-Fa-f]{6}$/.test(primaryColor ?? '') || logo === undefined || watermark === undefined ||
    (logo === '' ? logoMIME !== '' && logoMIME !== undefined : logoMIME !== 'image/png' && logoMIME !== 'image/jpeg')) {
    throw new ApiError('unexpected-response', 200)
  }
  const safeLogoMIME: BrandConfigView['logo_mime'] = logo === '' ? '' : logoMIME as 'image/png' | 'image/jpeg'
  return { product_name: productName, primary_color: primaryColor!, logo, logo_mime: safeLogoMIME, watermark }
}

export async function fetchBrandConfig(signal?: AbortSignal): Promise<BrandConfigView> {
  return parseBrandConfig(await apiRequest<unknown>('/api/v1/platform/brand', { signal }))
}

export async function updateBrandConfig(input: BrandConfigView, signal?: AbortSignal): Promise<BrandConfigView> {
  return parseBrandConfig(await apiRequest<unknown>('/api/v1/platform/brand', jsonBody(input, 'PUT', signal), { expectedStatus: 200 }))
}

export function parseSystemStatus(value: unknown): SystemStatusView {
  const envelope = recordOf(value)
  const source = recordOf(envelope?.data)
  const status = envelope?.status
  const running = source?.running
  const message = boundedString(source?.message, 1_024, true)
  const filesUpdated = safeCount(source?.files_updated, 1_000_000)
  const success = source?.success
  const startedAt = source?.started_at === undefined ? undefined : safeDate(source.started_at)
  const finishedAt = source?.finished_at === undefined ? undefined : safeDate(source.finished_at)
  const ref = source?.ref === undefined ? undefined : boundedString(source.ref, 200)
  if ((status !== undefined && status !== 0 && status !== 1) || typeof running !== 'boolean' || message === undefined || filesUpdated === undefined ||
    (success !== undefined && typeof success !== 'boolean') || (source?.started_at !== undefined && !startedAt) ||
    (source?.finished_at !== undefined && !finishedAt) || (source?.ref !== undefined && !ref)) {
    throw new ApiError('unexpected-response', 200)
  }
  const safeMessage = status === 1 || success === false ? '数据同步未完成，请查看受控服务日志。' : message
  return { running, message: safeMessage, files_updated: filesUpdated, ...(success === undefined ? {} : { success }), ...(startedAt ? { started_at: startedAt } : {}), ...(finishedAt ? { finished_at: finishedAt } : {}), ...(ref ? { ref } : {}) }
}

export async function fetchSystemStatus(signal?: AbortSignal): Promise<SystemStatusView> {
  return parseSystemStatus(await apiRequest<unknown>('/api/v1/system/update-data', { signal }))
}

export async function triggerSystemSync(signal?: AbortSignal): Promise<SystemStatusView> {
  return parseSystemStatus(await apiRequest<unknown>('/api/v1/system/update-data', { method: 'POST', signal }))
}

export async function fetchSafeVersion(signal?: AbortSignal): Promise<SafeVersionView> {
  const source = recordOf(await apiRequest<unknown>('/api/v1/version', { signal }, { unauthorized: 'suppress' }))
  const version = boundedString(source?.version, 128)
  const commit = boundedString(source?.commit, 256)
  const buildTime = boundedString(source?.build_time, 128)
  if (!version || !commit || !buildTime) throw new ApiError('unexpected-response', 200)
  return { version, commit, build_time: buildTime }
}
