/**
 * 功能：提供受限附件预检、普通/分片上传及角色化下载。
 * 实现：仅传 FormData 和 opaque ID，大文件按固定 5 MiB 分片串行上传并由服务端合并校验。
 * 输入：浏览器 File、AbortSignal、当前主体角色和后端附件 DTO。
 * 输出：安全附件视图或下载 Blob；不暴露存储路径。
 * 依赖：共享 JSON/二进制 API 客户端及身份 DTO。
 */
import { apiBinaryRequest, apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import type { AttachmentView, SubjectRole } from '../../shared/api/types'

export const MAX_ATTACHMENT_BYTES = 50 * 1024 * 1024
export const ATTACHMENT_CHUNK_BYTES = 5 * 1024 * 1024
export const MAX_ATTACHMENTS_PER_TASK = 10

function recordOf(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : undefined
}

function boundedString(value: unknown, maximum = 256): string | undefined {
  return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined
}

function parseAttachment(value: unknown): AttachmentView {
  const source = recordOf(value)
  const id = boundedString(source?.id)
  const filename = boundedString(source?.filename, 255)
  const size = Number.isSafeInteger(source?.size) && (source?.size as number) >= 0 ? (source?.size as number) : undefined
  const state = source?.state
  const createdAt = boundedString(source?.created_at, 64)
  if (!id || !filename || size === undefined || (state !== 'uploading' && state !== 'ready') || !createdAt || !Number.isFinite(Date.parse(createdAt))) {
    throw new ApiError('unexpected-response', 200)
  }
  return { id, filename, size, state, created_at: createdAt }
}

function attachmentPath(id: string): string {
  const opaqueID = boundedString(id)
  if (!opaqueID) throw new ApiError('bad-request', 0)
  return `/api/v1/platform/tasks/attachments/${encodeURIComponent(opaqueID)}`
}

export function preflightAttachments(files: readonly File[]): void {
  if (files.length > MAX_ATTACHMENTS_PER_TASK) throw new Error('单个任务最多选择 10 个附件')
  const names = new Set<string>()
  for (const file of files) {
    if (file.size <= 0) throw new Error('附件不能为空')
    if (file.size > MAX_ATTACHMENT_BYTES) throw new Error('附件大小不能超过 50 MiB')
    if (!file.name || file.name.length > 255 || file.name.includes('..') || /[/\\]/.test(file.name)) {
      throw new Error('附件名称无效')
    }
    if (names.has(file.name)) throw new Error('附件名称不能重复')
    names.add(file.name)
  }
}

async function uploadWhole(file: File, signal?: AbortSignal): Promise<AttachmentView> {
  const body = new FormData()
  body.append('file', file, file.name)
  return parseAttachment(
    await apiRequest<unknown>('/api/v1/platform/tasks/attachments', { method: 'POST', body, signal }),
  )
}

async function uploadChunked(file: File, signal?: AbortSignal): Promise<AttachmentView> {
  const started = parseAttachment(
    await apiRequest<unknown>('/api/v1/platform/tasks/attachments/chunked', {
      method: 'POST',
      signal,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ filename: file.name, size: file.size }),
    }),
  )
  const totalChunks = Math.ceil(file.size / ATTACHMENT_CHUNK_BYTES)
  for (let index = 0; index < totalChunks; index += 1) {
    const chunk = file.slice(index * ATTACHMENT_CHUNK_BYTES, Math.min(file.size, (index + 1) * ATTACHMENT_CHUNK_BYTES))
    const body = new FormData()
    body.append('chunk_index', String(index))
    body.append('chunk', chunk, `chunk-${index}`)
    await apiRequest<void>(`${attachmentPath(started.id)}/chunks`, { method: 'POST', body, signal })
  }
  return parseAttachment(
    await apiRequest<unknown>(`${attachmentPath(started.id)}/merge`, {
      method: 'POST',
      signal,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ total_chunks: totalChunks, file_size: file.size }),
    }),
  )
}

export async function uploadAttachment(file: File, signal?: AbortSignal): Promise<AttachmentView> {
  preflightAttachments([file])
  return file.size > ATTACHMENT_CHUNK_BYTES ? uploadChunked(file, signal) : uploadWhole(file, signal)
}

function safeDownloadFilename(header: string | null): string {
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(header ?? '')?.[1]
  let candidate = 'attachment.bin'
  if (encoded) {
    try {
      candidate = decodeURIComponent(encoded.replace(/\+/g, '%20'))
    } catch {
      candidate = 'attachment.bin'
    }
  }
  candidate = Array.from(candidate, (character) => {
    const code = character.codePointAt(0) ?? 0
    return code < 32 || code === 127 || character === '/' || character === '\\' ? '_' : character
  }).join('').replace(/^\.+/, '').slice(0, 255)
  return candidate || 'attachment.bin'
}

export async function downloadAttachment(id: string, role: SubjectRole, signal?: AbortSignal): Promise<void> {
  if (role === 'auditor') throw new Error('当前角色不能下载附件')
  const response = await apiBinaryRequest(`${attachmentPath(id)}/download`, { signal })
  const url = URL.createObjectURL(response.blob)
  try {
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = safeDownloadFilename(response.contentDisposition)
    anchor.rel = 'noopener'
    anchor.click()
  } finally {
    URL.revokeObjectURL(url)
  }
}
