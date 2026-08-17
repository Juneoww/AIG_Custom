/**
 * 功能：封装受治理模型目录、配置与存量凭据重加密 API。
 * 实现：将未知 JSON 严格投影为无 Token 白名单 DTO，写请求锁定精确状态。
 * 输入：服务端分页、opaque 模型 ID、模型配置与可选新 Token。
 * 输出：不含 Token 的安全模型 DTO，或不含原始响应的固定 API 错误。
 * 依赖：共享同源 API 客户端、CSRF Cookie 与 AbortSignal。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'

export type ModelScope = 'private' | 'global'
export type ModelSource = 'platform' | 'yaml'

interface SafeModelFields {
  id: string
  owner_user_id?: string
  scope: ModelScope
  name: string
  provider_model: string
  base_url: string
  note: string
  limit: number
  disabled: boolean
  created_at?: string
  updated_at?: string
}

export type GovernedModelView = SafeModelFields

export interface ModelCatalogItem extends SafeModelFields {
  source: ModelSource
  read_only: boolean
}

export interface ModelCatalogPage {
  items: ModelCatalogItem[]
  total: number
  page: number
  page_size: number
}

export interface ModelCatalogQuery {
  page: number
  pageSize: number
}

export interface CreateModelInput {
  name: string
  provider_model: string
  base_url: string
  token: string
  scope: ModelScope
  note?: string
  limit?: number
}

export interface UpdateModelInput {
  name?: string
  provider_model?: string
  base_url?: string
  token?: string
  note?: string
  limit?: number
  disabled?: boolean
}

const MASKED_TOKEN = '********'
const MODEL_SOURCES = new Set<ModelSource>(['platform', 'yaml'])
const MODEL_SCOPES = new Set<ModelScope>(['private', 'global'])
const OPAQUE_ID = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$/

function recordOf(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null ? value as Record<string, unknown> : undefined
}

function boundedString(value: unknown, maximum: number, allowEmpty = false): string | undefined {
  return typeof value === 'string' && value.length <= maximum && (allowEmpty || value.length > 0) ? value : undefined
}

function boundedID(value: unknown): string | undefined {
  const id = boundedString(value, 256)
  return id && id !== '.' && id !== '..' && OPAQUE_ID.test(id) ? id : undefined
}

function safeDate(value: unknown): string | undefined {
  const date = boundedString(value, 64)
  return date && Number.isFinite(Date.parse(date)) ? date : undefined
}

function optionalDate(value: unknown): string | undefined | null {
  return value === undefined ? undefined : safeDate(value) ?? null
}

function boundedInteger(value: unknown): number | undefined {
  return Number.isSafeInteger(value) && Math.abs(value as number) <= 2_147_483_647 ? value as number : undefined
}

function parseSafeFields(value: unknown): SafeModelFields | undefined {
  const source = recordOf(value)
  const id = boundedID(source?.id)
  const owner = source?.owner_user_id === undefined || source.owner_user_id === ''
    ? undefined
    : boundedString(source.owner_user_id, 256)
  const scope = boundedString(source?.scope, 16) as ModelScope | undefined
  const name = boundedString(source?.name, 512)
  const providerModel = boundedString(source?.provider_model, 512, true)
  const baseURL = boundedString(source?.base_url, 2_048, true)
  const note = source?.note === undefined ? '' : boundedString(source.note, 16_384, true)
  const limit = source?.limit === undefined ? 0 : boundedInteger(source.limit)
  const disabled = source?.disabled
  const createdAt = optionalDate(source?.created_at)
  const updatedAt = optionalDate(source?.updated_at)

  if (!id || !scope || !MODEL_SCOPES.has(scope) || !name || providerModel === undefined || baseURL === undefined ||
    note === undefined || limit === undefined || typeof disabled !== 'boolean' || createdAt === null || updatedAt === null ||
    (source?.owner_user_id !== undefined && source.owner_user_id !== '' && !owner) || source?.token !== MASKED_TOKEN) {
    return undefined
  }

  return {
    id,
    ...(owner ? { owner_user_id: owner } : {}),
    scope,
    name,
    provider_model: providerModel,
    base_url: baseURL,
    note,
    limit,
    disabled,
    ...(createdAt ? { created_at: createdAt } : {}),
    ...(updatedAt ? { updated_at: updatedAt } : {}),
  }
}

function parseCatalogItem(value: unknown): ModelCatalogItem | undefined {
  const fields = parseSafeFields(value)
  const source = boundedString(recordOf(value)?.source, 16) as ModelSource | undefined
  const readOnly = recordOf(value)?.read_only
  return fields && source && MODEL_SOURCES.has(source) && typeof readOnly === 'boolean' &&
    (source !== 'yaml' || readOnly && fields.scope === 'global' && fields.owner_user_id === undefined)
    ? { ...fields, source, read_only: readOnly }
    : undefined
}

function safeCount(value: unknown, maximum: number): number | undefined {
  return Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= maximum ? value as number : undefined
}

export function parseModelCatalog(value: unknown): ModelCatalogPage {
  const source = recordOf(value)
  const total = safeCount(source?.total, Number.MAX_SAFE_INTEGER)
  const page = safeCount(source?.page, 1_000)
  const pageSize = safeCount(source?.page_size, 100)
  if (!Array.isArray(source?.items) || source.items.length > 100 || total === undefined || !page || !pageSize) {
    throw new ApiError('unexpected-response', 200)
  }
  const items = source.items.map(parseCatalogItem)
  if (!items.every((item): item is ModelCatalogItem => item !== undefined)) {
    throw new ApiError('unexpected-response', 200)
  }
  return { items, total, page, page_size: pageSize }
}

function parseModelView(value: unknown): GovernedModelView {
  const fields = parseSafeFields(value)
  if (!fields) throw new ApiError('unexpected-response', 200)
  return fields
}

function modelPath(id: string): string {
  const safeID = boundedID(id)
  if (!safeID) throw new ApiError('bad-request', 0)
  return `/api/v1/platform/models/${encodeURIComponent(safeID)}`
}

function jsonBody(body: object, method: 'POST' | 'PUT', signal?: AbortSignal): RequestInit {
  return {
    method,
    signal,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

function validCreateInput(input: CreateModelInput): boolean {
  return input.name.trim().length > 0 && input.provider_model.trim().length > 0 && input.base_url.trim().length > 0 &&
    input.token.length > 0 && input.token !== MASKED_TOKEN && MODEL_SCOPES.has(input.scope) &&
    (input.limit === undefined || boundedInteger(input.limit) !== undefined)
}

export async function fetchModelCatalog(query: ModelCatalogQuery, signal?: AbortSignal): Promise<ModelCatalogPage> {
  if (!safeCount(query.page, 1_000) || !safeCount(query.pageSize, 100)) throw new ApiError('bad-request', 0)
  const search = new URLSearchParams({ page: String(query.page), page_size: String(query.pageSize) })
  return parseModelCatalog(await apiRequest<unknown>(`/api/v1/platform/models?${search}`, { signal }))
}

export async function createModel(input: CreateModelInput, signal?: AbortSignal): Promise<GovernedModelView> {
  if (!validCreateInput(input)) throw new ApiError('bad-request', 0)
  const response = await apiRequest<unknown>(
    '/api/v1/platform/models',
    jsonBody(input, 'POST', signal),
    { expectedStatus: 201 },
  )
  return parseModelView(response)
}

export async function updateModel(id: string, input: UpdateModelInput, signal?: AbortSignal): Promise<GovernedModelView> {
  const body = { ...input }
  if (body.token === '' || body.token === MASKED_TOKEN) delete body.token
  if (body.limit !== undefined && boundedInteger(body.limit) === undefined) throw new ApiError('bad-request', 0)
  const response = await apiRequest<unknown>(modelPath(id), jsonBody(body, 'PUT', signal), { expectedStatus: 200 })
  return parseModelView(response)
}

export async function deleteModel(id: string, signal?: AbortSignal): Promise<void> {
  await apiRequest<void>(modelPath(id), { method: 'DELETE', signal }, { expectedStatus: 204 })
}

export async function rotateModelEncryption(id: string, signal?: AbortSignal): Promise<void> {
  await apiRequest<void>(`${modelPath(id)}/rotate-encryption`, { method: 'POST', signal }, { expectedStatus: 204 })
}
