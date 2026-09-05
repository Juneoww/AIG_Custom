/**
 * 功能：封装任务分页、详情、创建、取消及受控短轮询合同。
 * 实现：对未知响应执行安全白名单投影，写请求不自动重放并复用逻辑提交幂等键。
 * 输入：服务端支持的精确筛选、opaque 任务 ID 和创建表单安全字段。
 * 输出：分页摘要、任务详情、取消确认或不确定状态。
 * 依赖：共享同源 API 客户端、固定错误类型与任务 DTO。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError, NetworkError } from '../../shared/api/errors'
import type {
  TaskCreateRequest,
  TaskDetail,
  TaskInputSummary,
  TaskListResponse,
  TaskStatus,
  TaskSummary,
  TaskType,
} from '../../shared/api/types'

export type { TaskCreateRequest } from '../../shared/api/types'

export interface TaskListFilters {
  page: number
  pageSize: number
  status?: TaskStatus
  taskType?: Exclude<TaskType, 'unknown'>
}

const TASK_TYPES = new Set<TaskType>(['mcp_scan', 'ai_infra_scan', 'model_redteam_report', 'agent_scan', 'unknown'])
const TASK_STATUSES = new Set<TaskStatus>([
  'pending',
  'dispatching',
  'running',
  'succeeded',
  'failed',
  'dispatch_failed',
  'dispatch_unknown',
  'cancelled',
])
const TERMINAL_STATUSES = new Set<TaskStatus>(['succeeded', 'failed', 'cancelled'])
const MAX_POLL_COUNT = 8
const MAX_TASK_REMARK_CODE_POINTS = 2_000

function recordOf(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : undefined
}

function boundedString(value: unknown, maximum = 256): string | undefined {
  return typeof value === 'string' && value.length > 0 && value.length <= maximum ? value : undefined
}

function safeModelID(value: unknown): string | undefined {
  return typeof value === 'string'
    && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value)
    && value !== '.'
    && value !== '..'
    ? value
    : undefined
}

function safeDate(value: unknown): string | undefined {
  const text = boundedString(value, 64)
  return text && Number.isFinite(Date.parse(text)) ? text : undefined
}

function safeInteger(value: unknown, maximum = Number.MAX_SAFE_INTEGER): number | undefined {
  return Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= maximum
    ? (value as number)
    : undefined
}

function isWellFormedUnicode(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const unit = value.charCodeAt(index)
    if (unit >= 0xD800 && unit <= 0xDBFF) {
      if (index + 1 >= value.length) return false
      const next = value.charCodeAt(index + 1)
      if (!(next >= 0xDC00 && next <= 0xDFFF)) return false
      index += 1
      continue
    }
    if (unit >= 0xDC00 && unit <= 0xDFFF) return false
  }
  return true
}

function safeTaskRemark(value: unknown): string | undefined {
  if (typeof value !== 'string' || !isWellFormedUnicode(value)) return undefined
  const normalized = value.trim()
  if (!normalized || !isWellFormedUnicode(normalized) || [...normalized].length > MAX_TASK_REMARK_CODE_POINTS) return undefined
  return normalized
}

function parseTaskSummary(value: unknown): TaskSummary | undefined {
  const source = recordOf(value)
  const id = boundedString(source?.id)
  const owner = boundedString(source?.owner, 128)
  const taskType = boundedString(source?.task_type, 32) as TaskType | undefined
  const status = boundedString(source?.status, 32) as TaskStatus | undefined
  const createdAt = safeDate(source?.created_at)
  const updatedAt = safeDate(source?.updated_at)
  if (!id || !owner || !taskType || !TASK_TYPES.has(taskType) || !status || !TASK_STATUSES.has(status) || !createdAt || !updatedAt) {
    return undefined
  }
  return { id, owner, task_type: taskType, status, created_at: createdAt, updated_at: updatedAt }
}

function parseInputSummary(value: unknown): TaskInputSummary | undefined {
  const source = recordOf(value)
  if (!source) return undefined
  const result: TaskInputSummary = {}
  if (source.model_id !== undefined) {
    const modelID = safeModelID(source.model_id)
    if (!modelID) return undefined
    result.model_id = modelID
  }
  if (source.language !== undefined) {
    if (source.language !== 'zh' && source.language !== 'en') return undefined
    result.language = source.language
  }
  if (source.port_scan_mode !== undefined) {
    if (source.port_scan_mode !== 'fixed_ai' && source.port_scan_mode !== 'full_tcp') return undefined
    result.port_scan_mode = source.port_scan_mode
  }
  for (const [wire, key, maximum] of [
    ['thread', 'thread', 1_024],
    ['timeout', 'timeout', 86_400],
    ['target_count', 'target_count', 1_000_000],
    ['num_prompts', 'num_prompts', 1_000_000],
  ] as const) {
    if (source[wire] === undefined) continue
    const parsed = safeInteger(source[wire], maximum)
    if (parsed === undefined) return undefined
    result[key] = parsed
  }
  return result
}

export function parseTaskDetail(value: unknown): TaskDetail {
  const source = recordOf(value)
  const summary = parseTaskSummary(value)
  const inputSummary = parseInputSummary(source?.input_summary)
  if (!summary || !inputSummary) throw new ApiError('unexpected-response', 200)
  const detail: TaskDetail = { ...summary, input_summary: inputSummary }
  if (source?.remark !== undefined) {
    const remark = safeTaskRemark(source.remark)
    if (!remark) throw new ApiError('unexpected-response', 200)
    detail.remark = remark
  }
  return detail
}

function parseTaskList(value: unknown): TaskListResponse {
  const source = recordOf(value)
  const total = safeInteger(source?.total)
  const page = safeInteger(source?.page, 1_000)
  const pageSize = safeInteger(source?.page_size, 100)
  if (!Array.isArray(source?.items) || source.items.length > 100 || total === undefined || !page || !pageSize) {
    throw new ApiError('unexpected-response', 200)
  }
  const items = source.items.map(parseTaskSummary)
  if (!items.every((item): item is TaskSummary => item !== undefined)) {
    throw new ApiError('unexpected-response', 200)
  }
  return { items, total, page, page_size: pageSize }
}

function taskPath(id: string): string {
  const opaqueID = boundedString(id)
  if (!opaqueID) throw new ApiError('bad-request', 0)
  return `/api/v1/platform/tasks/${encodeURIComponent(opaqueID)}`
}

export async function fetchTaskList(filters: TaskListFilters, signal?: AbortSignal): Promise<TaskListResponse> {
  const query = new URLSearchParams({ page: String(filters.page), page_size: String(filters.pageSize) })
  if (filters.status) query.set('status', filters.status)
  if (filters.taskType) query.set('task_type', filters.taskType)
  const response = await apiRequest<unknown>(`/api/v1/platform/tasks?${query}`, { signal })
  return parseTaskList(response)
}

export async function fetchTaskDetail(id: string, signal?: AbortSignal): Promise<TaskDetail> {
  return parseTaskDetail(await apiRequest<unknown>(taskPath(id), { signal }))
}

export interface TaskSubmission {
  readonly idempotencyKey: string
  submit: (signal?: AbortSignal) => Promise<TaskDetail>
}

export function createTaskSubmission(input: TaskCreateRequest, idempotencyKey: string = crypto.randomUUID()): TaskSubmission {
  if (!boundedString(idempotencyKey, 128)) throw new ApiError('bad-request', 0)
  return {
    idempotencyKey,
    submit: async (signal) => {
      const response = await apiRequest<unknown>('/api/v1/platform/tasks', {
        method: 'POST',
        signal,
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey },
        body: JSON.stringify(input),
      })
      return parseTaskDetail(response)
    },
  }
}

export function taskPollDelay(task: Pick<TaskDetail, 'status'> | undefined, completedPolls: number): number | false {
  if (!task || TERMINAL_STATUSES.has(task.status) || completedPolls >= MAX_POLL_COUNT) return false
  return Math.min(8_000, 2_000 * 2 ** Math.floor(completedPolls / 2))
}

export type GovernedCancelResult =
  | { status: 'confirmed' }
  | { status: 'uncertain'; task: TaskDetail }

export async function cancelTaskGoverned(id: string, signal?: AbortSignal): Promise<GovernedCancelResult> {
  try {
    await apiRequest<void>(`${taskPath(id)}/cancel`, { method: 'POST', signal }, { expectedStatus: 204 })
    return { status: 'confirmed' }
  } catch (error) {
    const needsConfirmation = error instanceof NetworkError
      || (error instanceof ApiError && error.kind === 'server')
      || (error instanceof ApiError && error.kind === 'unexpected-response' && error.status >= 200 && error.status < 300)
    if (!needsConfirmation) throw error
    const task = await fetchTaskDetail(id, signal)
    return { status: 'uncertain', task }
  }
}
