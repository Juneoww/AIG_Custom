/**
 * 功能：提供 MCP 专属创建、查询、取消与代码附件上传。
 * 实现：严格构造互斥输入与安全输出；分片失败清理；写入仅支持用户显式幂等重试。
 * 输入：仓库或已保存连接引用、File；输出：安全任务与附件 DTO，不含原始连接材料或文件名。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import type { TaskStatus } from '../../shared/api/types'
import { arrayField, createMCPMutation, dateField, enumeration, integer, opaqueID, record, textField } from '../mcp-connections/api'

export interface MCPScanInput { source_kind: 'repository' | 'service'; repository_url?: string; attachment_ids?: string[]; connection_config_id?: string; connection_config_version?: number; authorization_confirmed?: boolean; model_id?: string; thread?: number }
export interface MCPScanSummary { id: string; owner: string; status: TaskStatus; source_kind: 'repository' | 'service' | 'legacy_unknown'; created_at: string; updated_at: string }
export interface MCPScanDetail extends MCPScanSummary { input_summary: { language: 'zh_CN'; source_kind: MCPScanSummary['source_kind']; model_id?: string; thread?: number }; report_id?: string }
export interface MCPScanResult { task_id: string; status: TaskStatus }
export interface MCPAttachment { id: string; state: 'uploading' | 'ready'; size: number; max_file_bytes: number; max_chunk_bytes: number }
const scanBase = '/api/v1/platform/mcp-scans'
const attachmentBase = '/api/v1/platform/mcp-scan-attachments'
const taskStatuses = ['pending', 'dispatching', 'running', 'succeeded', 'failed', 'dispatch_failed', 'dispatch_unknown', 'cancelled'] as const
export const mcpSourceLabels = { repository: '代码 / 仓库', service: 'MCP 服务', legacy_unknown: '历史扫描' }
export const mcpStatusLabels: Record<TaskStatus, string> = { pending: '等待调度', dispatching: '正在调度', running: '执行中', succeeded: '已完成', failed: '执行失败', dispatch_failed: '调度失败', dispatch_unknown: '调度状态待确认', cancelled: '已取消' }
export function isMCPScanTerminal(status: TaskStatus): boolean { return ['succeeded', 'failed', 'cancelled'].includes(status) }
export function validateMCPScanInput(input: unknown): MCPScanInput {
  const s = record(input)
  const allowed = ['source_kind', 'repository_url', 'attachment_ids', 'connection_config_id', 'connection_config_version', 'authorization_confirmed', 'model_id', 'thread']
  if (Object.keys(s).some((key) => !allowed.includes(key))) throw new ApiError('bad-request', 0)
  const source_kind = enumeration(s.source_kind, ['repository', 'service'])
  const common = { source_kind, ...(s.model_id ? { model_id: opaqueID(s.model_id) } : {}), thread: integer(s.thread ?? 4, 1, 32) }
  if (source_kind === 'service') {
    if (s.repository_url !== undefined || s.attachment_ids !== undefined || s.authorization_confirmed !== true) throw new ApiError('bad-request', 0)
    return { ...common, connection_config_id: opaqueID(s.connection_config_id), connection_config_version: integer(s.connection_config_version), authorization_confirmed: true }
  }
  if (s.connection_config_id !== undefined || s.connection_config_version !== undefined || s.authorization_confirmed !== undefined || Boolean(s.repository_url) === Boolean(s.attachment_ids)) throw new ApiError('bad-request', 0)
  if (s.repository_url) {
    const repository_url = textField(s.repository_url, 4096)
    let parsed: URL
    try { parsed = new URL(repository_url) } catch { throw new ApiError('bad-request', 0) }
    if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.search || parsed.hash) throw new ApiError('bad-request', 0)
    return { ...common, repository_url }
  }
  const attachment_ids = arrayField(s.attachment_ids, opaqueID, 10)
  if (attachment_ids.length === 0 || new Set(attachment_ids).size !== attachment_ids.length) throw new ApiError('bad-request', 0)
  return { ...common, attachment_ids }
}
function parseSummary(value: unknown): MCPScanSummary {
  const s = record(value)
  return { id: opaqueID(s.id), owner: textField(s.owner, 256), status: enumeration(s.status, taskStatuses), source_kind: enumeration(s.source_kind, ['repository', 'service', 'legacy_unknown']), created_at: dateField(s.created_at), updated_at: dateField(s.updated_at) }
}
export function parseMCPScanDetail(value: unknown): MCPScanDetail {
  const s = record(value); const summary = parseSummary(s); const input = record(s.input_summary)
  const source_kind = enumeration(input.source_kind, ['repository', 'service', 'legacy_unknown'])
  if (source_kind !== summary.source_kind) throw new ApiError('unexpected-response', 200)
  return { ...summary, input_summary: { language: enumeration(input.language, ['zh_CN']), source_kind, ...(input.model_id ? { model_id: opaqueID(input.model_id) } : {}), ...(input.thread === undefined ? {} : { thread: integer(input.thread, 1, 32) }) }, ...(s.report_id ? { report_id: opaqueID(s.report_id) } : {}) }
}
function parseResult(value: unknown): MCPScanResult { const s = record(value); return { task_id: opaqueID(s.task_id), status: enumeration(s.status, taskStatuses) } }
export function createMCPScanSubmission(input: MCPScanInput) { return createMCPMutation(scanBase, 'POST', validateMCPScanInput(input), parseResult) }
export function createMCPCancelSubmission(id: string) { return createMCPMutation(`${scanBase}/${encodeURIComponent(opaqueID(id))}/cancel`, 'POST', {}, parseResult) }
export async function fetchMCPScan(id: string, signal?: AbortSignal) { return parseMCPScanDetail(await apiRequest(`${scanBase}/${encodeURIComponent(opaqueID(id))}`, { signal })) }
export async function fetchMCPScans(page = 1, status = '', signal?: AbortSignal) {
  const query = new URLSearchParams({ page: String(integer(page)), page_size: '20' })
  if (status) query.set('status', enumeration(status, taskStatuses))
  const s = record(await apiRequest(`${scanBase}?${query}`, { signal }))
  return { items: arrayField(s.items, parseSummary, 20), total: integer(s.total, 0), page: integer(s.page), page_size: integer(s.page_size, 1, 100) }
}
function parseAttachment(value: unknown): MCPAttachment {
  const s = record(value)
  return { id: opaqueID(s.id), state: enumeration(s.state, ['uploading', 'ready']), size: integer(s.size, 0), max_file_bytes: integer(s.max_file_bytes), max_chunk_bytes: integer(s.max_chunk_bytes) }
}
export async function deleteMCPAttachment(id: string): Promise<void> { await apiRequest(`${attachmentBase}/${encodeURIComponent(opaqueID(id))}`, { method: 'DELETE' }) }
export async function uploadMCPAttachment(file: File, signal?: AbortSignal): Promise<MCPAttachment> {
  if (file.size < 1 || file.size > 50 * 1024 * 1024 || !file.name || file.name.length > 255) throw new ApiError('bad-request', 0)
  if (file.size <= 5 * 1024 * 1024) {
    const body = new FormData(); body.append('file', file)
    const result = parseAttachment(await apiRequest(attachmentBase, { method: 'POST', body, signal }))
    if (signal?.aborted) { await deleteMCPAttachment(result.id).catch(() => undefined); throw new ApiError('bad-request', 0) }
    return result
  }
  const started = parseAttachment(await apiRequest(`${attachmentBase}/chunked`, { method: 'POST', signal, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ filename: file.name, size: file.size }) }))
  try {
    const chunkSize = Math.min(started.max_chunk_bytes, 5 * 1024 * 1024)
    if (file.size > started.max_file_bytes) throw new ApiError('bad-request', 0)
    const total = Math.ceil(file.size / chunkSize)
    for (let index = 0; index < total; index++) {
      const body = new FormData(); body.append('chunk_index', String(index)); body.append('chunk', file.slice(index * chunkSize, (index + 1) * chunkSize))
      await apiRequest(`${attachmentBase}/${encodeURIComponent(started.id)}/chunks`, { method: 'POST', body, signal })
    }
    return parseAttachment(await apiRequest(`${attachmentBase}/${encodeURIComponent(started.id)}/merge`, { method: 'POST', signal, headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() }, body: JSON.stringify({ total_chunks: total, file_size: file.size }) }))
  } catch (error) { await deleteMCPAttachment(started.id).catch(() => undefined); throw error }
}
