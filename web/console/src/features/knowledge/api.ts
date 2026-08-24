/**
 * 功能：适配六类规则与知识库的 legacy API，并提供安全治理写请求。
 * 实现：unknown 响应仅投影白名单 DTO，原始 message 永不外泄，opaque 标识统一校验。
 * 输入：分页筛选、资源标识、原始 YAML/JSON 与可选 AbortSignal。
 * 输出：安全领域 DTO、原文字节或固定 ApiError。
 * 依赖：共享同源 API 客户端、Cookie CSRF 与浏览器 AbortSignal。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'

export const MAX_KNOWLEDGE_FILE_BYTES = 1024 * 1024

export interface KnowledgePageQuery {
  page: number
  size: number
  query: string
}

export interface KnowledgePage<T> {
  items: T[]
  total: number
  page: number
  size: number
}

export interface FingerprintSummary {
  name: string
  author: string
  description: string
  severity: string
  recommendation: number
}

export interface VulnerabilitySummary {
  cve: string
  fingerprint: string
  summary: string
  details: string
  cvss: string
  severity: string
  advice: string
  references: string[]
  author: string
  rule: string
}

export interface EvaluationSummary {
  name: string
  description: string
  descriptionZh: string
  author: string
  sources: string[]
  count: number
  isDefault: boolean
  tags: string[]
  recommendation: number
  language: string
}

export interface MCPPluginSummary {
  id: string
  name: string
  description: string
  author: string
  categories: string[]
}

export interface PromptCollection {
  id: string
  product: string
  affiliation: string
  modelVersion: string
  prompt: string
  codeExec: boolean
  uploadFile: boolean
  multiModal: boolean
  webSearch: boolean
  securityPolicies: boolean
}

export interface AgentConfig {
  name: string
  content: string
}

export type AgentTemplateFieldType = 'text' | 'password' | 'number' | 'select' | 'json' | 'textarea'

export interface AgentTemplateOption {
  label: string
  value: string
}

export interface AgentTemplateField {
  field: string
  label: string
  type: AgentTemplateFieldType
  required: boolean
  placeholder?: string
  description?: string
  minimum?: number
  maximum?: number
  step?: number
  defaultValue?: string | number | boolean
  options?: AgentTemplateOption[]
}

export interface AgentTemplate {
  id: string
  name: string
  description: string
  fields: AgentTemplateField[]
}

export type RawKnowledgeKind = 'fingerprint' | 'vulnerability' | 'evaluation'

const OPAQUE_ID = /^[A-Za-z0-9][A-Za-z0-9._ -]{0,255}$/
const RAW_PATHS: Record<RawKnowledgeKind, string> = {
  fingerprint: 'fingerprints',
  vulnerability: 'vulnerabilities',
  evaluation: 'evaluations',
}

function recordOf(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined
}

function unexpected(): never {
  throw new ApiError('unexpected-response', 200)
}

function boundedString(value: unknown, maximum: number, allowEmpty = true): string | undefined {
  return typeof value === 'string' && value.length <= maximum && (allowEmpty || value.length > 0) ? value : undefined
}

function safeInteger(value: unknown, maximum: number): number | undefined {
  return Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= maximum ? value as number : undefined
}

function safeBoolean(value: unknown): boolean | undefined {
  return typeof value === 'boolean' ? value : undefined
}

function safeStringArray(value: unknown, maximumItems = 128, maximumLength = 2_048): string[] | undefined {
  if (!Array.isArray(value) || value.length > maximumItems) return undefined
  const result = value.map((item) => boundedString(item, maximumLength, false))
  return result.every((item): item is string => item !== undefined) ? result : undefined
}

export function isSafeKnowledgeID(value: unknown): value is string {
  return typeof value === 'string' && value !== '.' && value !== '..' && !value.includes('..') && OPAQUE_ID.test(value)
}

function legacyEnvelope(value: unknown): { data: unknown; message: string } {
  const source = recordOf(value)
  const status = safeInteger(source?.status, 2_147_483_647)
  const message = boundedString(source?.message, 262_144)
  if (status === undefined || message === undefined || !Object.prototype.hasOwnProperty.call(source ?? {}, 'data')) unexpected()
  if (status !== 0) unexpected()
  return { data: source?.data, message }
}

function legacyWriteSuccess(value: unknown): void {
  const source = recordOf(value)
  const status = safeInteger(source?.status, 2_147_483_647)
  const message = boundedString(source?.message, 262_144)
  if (status !== 0 || message === undefined) unexpected()
}

function parsePage<T>(value: unknown, parseItem: (item: unknown) => T | undefined): KnowledgePage<T> {
  const source = recordOf(legacyEnvelope(value).data)
  const total = safeInteger(source?.total, Number.MAX_SAFE_INTEGER)
  const page = safeInteger(source?.page, 1_000)
  const size = safeInteger(source?.size, 100)
  if (!Array.isArray(source?.items) || source.items.length > 100 || total === undefined || !page || !size) unexpected()
  const items = source.items.map(parseItem)
  if (!items.every((item): item is T => item !== undefined)) unexpected()
  return { items, total, page, size }
}

function parseFingerprint(value: unknown): FingerprintSummary | undefined {
  const info = recordOf(recordOf(value)?.info)
  const name = boundedString(info?.name, 256, false)
  const author = boundedString(info?.author, 512)
  const description = boundedString(info?.desc, 8_192)
  const severity = boundedString(info?.severity, 32)
  const recommendation = info?.recommendation === undefined ? 0 : safeInteger(info.recommendation, 1_000_000)
  if (!name || !isSafeKnowledgeID(name) || author === undefined || description === undefined || severity === undefined || recommendation === undefined) return undefined
  return { name, author, description, severity, recommendation }
}

function parseVulnerability(value: unknown): VulnerabilitySummary | undefined {
  const source = recordOf(value)
  const info = recordOf(source?.info)
  const cve = boundedString(info?.cve, 256, false)
  const fingerprint = boundedString(info?.name, 256)
  const summary = boundedString(info?.summary, 16_384)
  const details = boundedString(info?.details, 131_072)
  const cvss = boundedString(info?.cvss, 64)
  const severity = boundedString(info?.severity, 32)
  const advice = boundedString(info?.security_advise, 131_072)
  const author = info?.author === undefined ? '' : boundedString(info.author, 512)
  const rule = boundedString(source?.rule, 16_384)
  const infoReferences = info?.references === undefined ? [] : safeStringArray(info.references)
  const outerReferences = source?.references === undefined ? [] : safeStringArray(source.references)
  if (!cve || !isSafeKnowledgeID(cve) || fingerprint === undefined || summary === undefined || details === undefined ||
    cvss === undefined || severity === undefined || advice === undefined || author === undefined || rule === undefined ||
    !infoReferences || !outerReferences) return undefined
  return { cve, fingerprint, summary, details, cvss, severity, advice, references: [...new Set([...infoReferences, ...outerReferences])], author, rule }
}

function parseEvaluation(value: unknown): EvaluationSummary | undefined {
  const source = recordOf(value)
  const name = boundedString(source?.name, 256, false)
  const description = boundedString(source?.description, 16_384)
  const descriptionZh = source?.description_zh === undefined ? '' : boundedString(source.description_zh, 16_384)
  const author = source?.author === undefined ? '' : boundedString(source.author, 512)
  const sources = source?.source === undefined ? [] : safeStringArray(source.source)
  const count = safeInteger(source?.count, 10_000_000)
  const isDefault = safeBoolean(source?.default)
  const tags = source?.tags === undefined ? [] : safeStringArray(source.tags, 128, 512)
  const recommendation = source?.recommendation === undefined ? 0 : safeInteger(source.recommendation, 1_000_000)
  const language = source?.language === undefined ? '' : boundedString(source.language, 64)
  if (!name || !isSafeKnowledgeID(name) || description === undefined || descriptionZh === undefined || author === undefined ||
    !sources || count === undefined || isDefault === undefined || !tags || recommendation === undefined || language === undefined) return undefined
  return { name, description, descriptionZh, author, sources, count, isDefault, tags, recommendation, language }
}

function parseMCP(value: unknown): MCPPluginSummary | undefined {
  const source = recordOf(value)
  const info = recordOf(source?.info)
  const id = boundedString(info?.id, 256, false)
  const name = boundedString(info?.name, 512)
  const description = boundedString(info?.description, 16_384)
  const author = boundedString(info?.author, 512)
  const categories = info?.category === undefined ? [] : safeStringArray(info.category, 128, 512)
  if (!id || !isSafeKnowledgeID(id) || name === undefined || description === undefined || author === undefined || !categories) return undefined
  return { id, name, description, author, categories }
}

function parsePrompt(value: unknown): PromptCollection | undefined {
  const source = recordOf(value)
  const id = boundedString(source?.id, 256, false)
  const product = boundedString(source?.product, 512)
  const affiliation = boundedString(source?.affiliation, 512)
  const modelVersion = boundedString(source?.model_version, 512)
  const prompt = boundedString(source?.prompt, MAX_KNOWLEDGE_FILE_BYTES)
  const codeExec = safeBoolean(source?.code_exec)
  const uploadFile = safeBoolean(source?.upload_file)
  const multiModal = safeBoolean(source?.multi_modal)
  const webSearch = safeBoolean(source?.web_search)
  const securityPolicies = safeBoolean(source?.sec_policies)
  if (!id || !isSafeKnowledgeID(id) || product === undefined || affiliation === undefined || modelVersion === undefined || prompt === undefined ||
    codeExec === undefined || uploadFile === undefined || multiModal === undefined || webSearch === undefined || securityPolicies === undefined) return undefined
  return { id, product, affiliation, modelVersion, prompt, codeExec, uploadFile, multiModal, webSearch, securityPolicies }
}

const AGENT_FIELD_TYPES = new Set<AgentTemplateFieldType>(['text', 'password', 'number', 'select', 'json', 'textarea'])
const DANGEROUS_FIELD_SEGMENTS = new Set(['__proto__', 'prototype', 'constructor'])

function parseAgentTemplateOption(value: unknown): AgentTemplateOption | undefined {
  const source = recordOf(value)
  const label = boundedString(source?.label, 256, false)
  const optionValue = boundedString(source?.value, 256, false)
  return label && optionValue !== undefined ? { label, value: optionValue } : undefined
}

function safeAgentFieldPath(value: unknown): value is string {
  if (typeof value !== 'string' || value.length > 256) return false
  const segments = value.split('.')
  return segments.length <= 8 && segments.every((segment) =>
    /^[A-Za-z][A-Za-z0-9_]*$/.test(segment) && !DANGEROUS_FIELD_SEGMENTS.has(segment.toLowerCase()),
  )
}

function parseAgentTemplateField(value: unknown): AgentTemplateField | undefined {
  const source = recordOf(value)
  const field = source?.field
  const label = boundedString(source?.label, 256, false)
  const type = boundedString(source?.type, 32, false)
  const required = safeBoolean(source?.required)
  const placeholder = source?.placeholder === undefined ? undefined : boundedString(source.placeholder, 8_192)
  const description = source?.description === undefined ? undefined : boundedString(source.description, 8_192)
  const minimum = source?.min === undefined ? undefined : typeof source.min === 'number' && Number.isFinite(source.min) ? source.min : undefined
  const maximum = source?.max === undefined ? undefined : typeof source.max === 'number' && Number.isFinite(source.max) ? source.max : undefined
  const step = source?.step === undefined ? undefined : typeof source.step === 'number' && Number.isFinite(source.step) && source.step > 0 ? source.step : undefined
  const defaultValue = source?.defaultValue
  const safeDefault = defaultValue === undefined || typeof defaultValue === 'string' || typeof defaultValue === 'number' || typeof defaultValue === 'boolean'
  const rawOptions = source?.options
  const options = rawOptions === undefined ? undefined : Array.isArray(rawOptions) && rawOptions.length <= 128
    ? rawOptions.map(parseAgentTemplateOption)
    : undefined
  if (!safeAgentFieldPath(field) || !label || !type || !AGENT_FIELD_TYPES.has(type as AgentTemplateFieldType) || required === undefined ||
    placeholder === undefined && source?.placeholder !== undefined || description === undefined && source?.description !== undefined ||
    minimum === undefined && source?.min !== undefined || maximum === undefined && source?.max !== undefined ||
    step === undefined && source?.step !== undefined || !safeDefault || options?.some((option) => option === undefined) ||
    rawOptions !== undefined && options === undefined || type === 'select' && (!options || options.length === 0)) return undefined
  return {
    field,
    label,
    type: type as AgentTemplateFieldType,
    required,
    ...(placeholder !== undefined ? { placeholder } : {}),
    ...(description !== undefined ? { description } : {}),
    ...(minimum !== undefined ? { minimum } : {}),
    ...(maximum !== undefined ? { maximum } : {}),
    ...(step !== undefined ? { step } : {}),
    ...(defaultValue !== undefined ? { defaultValue: defaultValue as string | number | boolean } : {}),
    ...(options !== undefined ? { options: options as AgentTemplateOption[] } : {}),
  }
}

function parseAgentTemplates(value: unknown): AgentTemplate[] {
  const source = recordOf(value)
  if (!source || Object.keys(source).length > 64) unexpected()
  const commonSource = recordOf(source.common)
  const commonRawFields = commonSource?.fields
  const commonFields = commonRawFields === undefined ? [] : Array.isArray(commonRawFields) && commonRawFields.length <= 128
    ? commonRawFields.map(parseAgentTemplateField)
    : undefined
  if (!commonFields || commonFields.some((field) => field === undefined)) unexpected()

  const templates = Object.entries(source)
    .filter(([id]) => id !== 'common')
    .map(([id, value]) => {
      const template = recordOf(value)
      const name = boundedString(template?.name, 256, false)
      const description = boundedString(template?.description, 8_192)
      const rawFields = template?.fields
      const fields = Array.isArray(rawFields) && rawFields.length <= 128 ? rawFields.map(parseAgentTemplateField) : undefined
      if (!isSafeKnowledgeID(id) || !name || description === undefined || !fields || fields.some((field) => field === undefined) || fields.length + commonFields.length > 192) unexpected()
      const combined = [...fields, ...commonFields] as AgentTemplateField[]
      if (new Set(combined.map((field) => field.field)).size !== combined.length) unexpected()
      return { id, name, description, fields: combined }
    })
  if (templates.length === 0) unexpected()
  return templates
}

function validPageQuery(query: KnowledgePageQuery): boolean {
  return Boolean(safeInteger(query.page, 1_000) && safeInteger(query.size, 100) && query.query.length <= 200)
}

function pagePath(resource: string, query: KnowledgePageQuery): string {
  if (!validPageQuery(query)) throw new ApiError('bad-request', 0)
  const search = new URLSearchParams({ page: String(query.page), size: String(query.size) })
  if (query.query.trim()) search.set('q', query.query.trim())
  return `/api/v1/knowledge/${resource}?${search}`
}

function opaquePath(resource: string, id: string, suffix = ''): string {
  if (!isSafeKnowledgeID(id)) throw new ApiError('bad-request', 0)
  return `/api/v1/knowledge/${resource}/${encodeURIComponent(id)}${suffix}`
}

function jsonWrite(method: 'POST' | 'PUT' | 'DELETE', body: object, signal?: AbortSignal): RequestInit {
  return { method, signal, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
}

async function legacyWrite(path: string, init: RequestInit): Promise<void> {
  legacyWriteSuccess(await apiRequest<unknown>(path, init, { expectedStatus: 200 }))
}

function parseUnpaged<T>(value: unknown, parseItem: (item: unknown) => T | undefined): T[] {
  const source = recordOf(legacyEnvelope(value).data)
  const total = safeInteger(source?.total, 100_000)
  if (!Array.isArray(source?.items) || source.items.length > 100_000 || total === undefined || total !== source.items.length) unexpected()
  const items = source.items.map(parseItem)
  if (!items.every((item): item is T => item !== undefined)) unexpected()
  return items
}

export async function fetchFingerprintPage(query: KnowledgePageQuery, signal?: AbortSignal): Promise<KnowledgePage<FingerprintSummary>> {
  return parsePage(await apiRequest<unknown>(pagePath('fingerprints', query), { signal }), parseFingerprint)
}

export async function fetchVulnerabilityPage(query: KnowledgePageQuery, signal?: AbortSignal): Promise<KnowledgePage<VulnerabilitySummary>> {
  return parsePage(await apiRequest<unknown>(pagePath('vulnerabilities', query), { signal }), parseVulnerability)
}

export async function fetchEvaluationPage(query: KnowledgePageQuery, signal?: AbortSignal): Promise<KnowledgePage<EvaluationSummary>> {
  return parsePage(await apiRequest<unknown>(pagePath('evaluations', query), { signal }), parseEvaluation)
}

export async function fetchMCPPlugins(signal?: AbortSignal): Promise<MCPPluginSummary[]> {
  return parseUnpaged(await apiRequest<unknown>('/api/v1/knowledge/mcp', { signal }), parseMCP)
}

export async function fetchMCPRaw(id: string, signal?: AbortSignal): Promise<string> {
  if (!isSafeKnowledgeID(id)) unexpected()
  const data = legacyEnvelope(await apiRequest<unknown>('/api/v1/knowledge/mcp', { signal })).data
  const source = recordOf(data)
  if (!source || !Array.isArray(source.items)) unexpected()
  for (const value of source.items) {
    const record = recordOf(value)
    const info = recordOf(record?.info)
    if (boundedString(info?.id, 256, false) !== id) continue
    const content = boundedString(record?.RawData, MAX_KNOWLEDGE_FILE_BYTES, false)
    if (content === undefined) unexpected()
    return content
  }
  unexpected()
}

export async function fetchPromptCollections(signal?: AbortSignal): Promise<PromptCollection[]> {
  return parseUnpaged(await apiRequest<unknown>('/api/v1/knowledge/prompt_collections', { signal }), parsePrompt)
}

export async function fetchAgentNames(signal?: AbortSignal): Promise<string[]> {
  const data = legacyEnvelope(await apiRequest<unknown>('/api/v1/knowledge/agent/names', { signal })).data
  const names = safeStringArray(data, 10_000, 256)
  if (!names || !names.every(isSafeKnowledgeID)) unexpected()
  return names
}

export async function fetchAgentConfig(name: string, signal?: AbortSignal): Promise<AgentConfig> {
  const data = legacyEnvelope(await apiRequest<unknown>(opaquePath('agent', name), { signal })).data
  const content = boundedString(data, MAX_KNOWLEDGE_FILE_BYTES, false)
  if (content === undefined) unexpected()
  return { name, content }
}

export async function fetchAgentTemplates(signal?: AbortSignal): Promise<AgentTemplate[]> {
  return parseAgentTemplates(await apiRequest<unknown>('/api/v1/knowledge/agent/template?language=zh', { signal }))
}

export async function fetchRawKnowledge(kind: RawKnowledgeKind, id: string, signal?: AbortSignal): Promise<string> {
  const data = recordOf(legacyEnvelope(await apiRequest<unknown>(opaquePath(RAW_PATHS[kind], id, '/raw'), { signal })).data)
  const content = boundedString(data?.content, MAX_KNOWLEDGE_FILE_BYTES, false)
  if (content === undefined) unexpected()
  return content
}

export function createFingerprint(content: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite('/api/v1/knowledge/fingerprints', jsonWrite('POST', { file_content: content }, signal))
}

export function updateFingerprint(id: string, content: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite(opaquePath('fingerprints', id), jsonWrite('PUT', { file_content: content }, signal))
}

export function deleteFingerprint(id: string, signal?: AbortSignal): Promise<void> {
  if (!isSafeKnowledgeID(id)) return Promise.reject(new ApiError('bad-request', 0))
  return legacyWrite('/api/v1/knowledge/fingerprints', jsonWrite('DELETE', { name: [id] }, signal))
}

export function createVulnerability(content: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite('/api/v1/knowledge/vulnerabilities', jsonWrite('POST', { file_content: content }, signal))
}

export function updateVulnerability(id: string, content: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite(opaquePath('vulnerabilities', id), jsonWrite('PUT', { file_content: content }, signal))
}

export function deleteVulnerability(id: string, signal?: AbortSignal): Promise<void> {
  if (!isSafeKnowledgeID(id)) return Promise.reject(new ApiError('bad-request', 0))
  return legacyWrite('/api/v1/knowledge/vulnerabilities', jsonWrite('DELETE', { cves: [id] }, signal))
}

export function createEvaluation(content: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite('/api/v1/knowledge/evaluations', jsonWrite('POST', { file_content: content }, signal))
}

export function updateEvaluation(id: string, content: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite(opaquePath('evaluations', id), jsonWrite('PUT', { file_content: content }, signal))
}

export function deleteEvaluation(id: string, signal?: AbortSignal): Promise<void> {
  if (!isSafeKnowledgeID(id)) return Promise.reject(new ApiError('bad-request', 0))
  return legacyWrite('/api/v1/knowledge/evaluations', jsonWrite('DELETE', { names: [id] }, signal))
}

export function createMCPPlugin(content: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite('/api/v1/knowledge/mcp', jsonWrite('POST', { content }, signal))
}

export function updateMCPPlugin(id: string, content: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite(opaquePath('mcp', id), jsonWrite('PUT', { content }, signal))
}

export function deleteMCPPlugin(id: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite(opaquePath('mcp', id), { method: 'DELETE', signal })
}

function promptContent(prompt: PromptCollection): string {
  if (!isSafeKnowledgeID(prompt.id)) throw new ApiError('bad-request', 0)
  return JSON.stringify({
    id: prompt.id,
    product: prompt.product,
    affiliation: prompt.affiliation,
    model_version: prompt.modelVersion,
    prompt: prompt.prompt,
    code_exec: prompt.codeExec,
    upload_file: prompt.uploadFile,
    multi_modal: prompt.multiModal,
    web_search: prompt.webSearch,
    sec_policies: prompt.securityPolicies,
  }, null, 2)
}

export function createPromptCollection(prompt: PromptCollection, signal?: AbortSignal): Promise<void> {
  return legacyWrite('/api/v1/knowledge/prompt_collections', jsonWrite('POST', { content: promptContent(prompt) }, signal))
}

export function updatePromptCollection(id: string, prompt: PromptCollection, signal?: AbortSignal): Promise<void> {
  return legacyWrite(opaquePath('prompt_collections', id), jsonWrite('PUT', { content: promptContent(prompt) }, signal))
}

export function deletePromptCollection(id: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite(opaquePath('prompt_collections', id), { method: 'DELETE', signal })
}

export function saveAgentConfig(name: string, content: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite(opaquePath('agent', name), jsonWrite('POST', { content }, signal))
}

export function deleteAgentConfig(name: string, signal?: AbortSignal): Promise<void> {
  return legacyWrite(opaquePath('agent', name), { method: 'DELETE', signal })
}

export async function testAgentConnection(content: string, signal?: AbortSignal): Promise<void> {
  await legacyWrite('/api/v1/knowledge/agent/connect', jsonWrite('POST', { content }, signal))
}

export async function testAgentPrompt(content: string, prompt: string, signal?: AbortSignal): Promise<string> {
  const response = await apiRequest<unknown>('/api/v1/knowledge/agent/prompt_test', jsonWrite('POST', { content, prompt }, signal), { expectedStatus: 200 })
  const envelope = legacyEnvelope({ ...recordOf(response), data: null })
  const output = boundedString(envelope.message, 262_144)
  if (output === undefined || new TextEncoder().encode(output).byteLength > 256 * 1024) unexpected()
  return output
}
