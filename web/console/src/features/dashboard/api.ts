/**
 * 功能：读取并校验按当前主体限定的治理总览安全 DTO。
 * 实现：请求单一 dashboard 端点，对未知 JSON 做白名单、边界和枚举校验。
 * 输入：TanStack Query 取消信号及后端未知 JSON。
 * 输出：只含指标、趋势、待关注与任务摘要的 DashboardView。
 * 依赖：共享 API 客户端、固定错误类型和任务摘要 DTO。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import type { TaskStatus, TaskSummary, TaskType } from '../../shared/api/types'

export interface RiskSummary {
  mapping_version: string
  score: number
  high: number
  medium: number
  low: number
}

export interface TrendPoint {
  date: string
  completed: number
  security_score: number | null
  high: number
  medium: number
  low: number
}

export interface AttentionItem {
  report_id: string
  task_id: string
  task_type: Exclude<TaskType, 'unknown'>
  completed_at: string
  score: number
  high: number
  medium: number
  low: number
}

export interface DashboardView {
  has_data: boolean
  security_score: number | null
  mapping_versions: string[]
  risk: RiskSummary
  trend: TrendPoint[]
  recent_tasks: TaskSummary[]
  attention: AttentionItem[]
}

const TASK_TYPES = new Set<TaskType>([
  'mcp_scan',
  'ai_infra_scan',
  'model_redteam_report',
  'agent_scan',
  'unknown',
])
const ATTENTION_TASK_TYPES = new Set<AttentionItem['task_type']>([
  'mcp_scan',
  'ai_infra_scan',
  'model_redteam_report',
  'agent_scan',
])
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

function recordOf(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : undefined
}

function boundedString(value: unknown, maximum = 256): string | undefined {
  return typeof value === 'string' && value.length <= maximum ? value : undefined
}

function dateString(value: unknown): string | undefined {
  const text = boundedString(value, 64)
  return text && Number.isFinite(Date.parse(text)) ? text : undefined
}

function count(value: unknown): number | undefined {
  return Number.isSafeInteger(value) && (value as number) >= 0 ? (value as number) : undefined
}

function score(value: unknown): number | undefined {
  const parsed = count(value)
  return parsed !== undefined && parsed <= 100 ? parsed : undefined
}

function optionalScore(value: unknown): number | null | undefined {
  return value === null ? null : score(value)
}

function parseRisk(value: unknown): RiskSummary | undefined {
  const source = recordOf(value)
  if (!source) return undefined
  const mappingVersion = boundedString(source.mapping_version, 128)
  const parsedScore = score(source.score)
  const high = count(source.high)
  const medium = count(source.medium)
  const low = count(source.low)
  if (
    mappingVersion === undefined ||
    parsedScore === undefined ||
    high === undefined ||
    medium === undefined ||
    low === undefined
  ) {
    return undefined
  }
  return { mapping_version: mappingVersion, score: parsedScore, high, medium, low }
}

function parseTrendPoint(value: unknown): TrendPoint | undefined {
  const source = recordOf(value)
  if (!source) return undefined
  const date = dateString(source.date)
  const completed = count(source.completed)
  const securityScore = optionalScore(source.security_score)
  const high = count(source.high)
  const medium = count(source.medium)
  const low = count(source.low)
  if (
    date === undefined ||
    completed === undefined ||
    securityScore === undefined ||
    high === undefined ||
    medium === undefined ||
    low === undefined
  ) {
    return undefined
  }
  return { date, completed, security_score: securityScore, high, medium, low }
}

function parseTaskSummary(value: unknown): TaskSummary | undefined {
  const source = recordOf(value)
  if (!source) return undefined
  const id = boundedString(source.id)
  const owner = boundedString(source.owner, 128)
  const taskType = boundedString(source.task_type, 32) as TaskType | undefined
  const status = boundedString(source.status, 32) as TaskStatus | undefined
  const createdAt = dateString(source.created_at)
  const updatedAt = dateString(source.updated_at)
  if (
    !id ||
    owner === undefined ||
    !taskType ||
    !TASK_TYPES.has(taskType) ||
    !status ||
    !TASK_STATUSES.has(status) ||
    !createdAt ||
    !updatedAt
  ) {
    return undefined
  }
  return {
    id,
    owner,
    task_type: taskType,
    status,
    created_at: createdAt,
    updated_at: updatedAt,
  }
}

function parseAttention(value: unknown): AttentionItem | undefined {
  const source = recordOf(value)
  if (!source) return undefined
  const reportID = boundedString(source.report_id)
  const taskID = boundedString(source.task_id)
  const taskType = boundedString(source.task_type, 32) as AttentionItem['task_type'] | undefined
  const completedAt = dateString(source.completed_at)
  const parsedScore = score(source.score)
  const high = count(source.high)
  const medium = count(source.medium)
  const low = count(source.low)
  if (
    !reportID ||
    !taskID ||
    !taskType ||
    !ATTENTION_TASK_TYPES.has(taskType) ||
    !completedAt ||
    parsedScore === undefined ||
    high === undefined ||
    medium === undefined ||
    low === undefined
  ) {
    return undefined
  }
  return {
    report_id: reportID,
    task_id: taskID,
    task_type: taskType,
    completed_at: completedAt,
    score: parsedScore,
    high,
    medium,
    low,
  }
}

function parseArray<T>(
  value: unknown,
  parser: (item: unknown) => T | undefined,
  exactOrMaximum: number,
  exact = false,
): T[] | undefined {
  if (!Array.isArray(value) || (exact ? value.length !== exactOrMaximum : value.length > exactOrMaximum)) {
    return undefined
  }
  const parsed = value.map(parser)
  return parsed.every((item): item is T => item !== undefined) ? parsed : undefined
}

export function parseDashboardView(value: unknown): DashboardView {
  const source = recordOf(value)
  const hasData = source?.has_data
  const securityScore = optionalScore(source?.security_score)
  const mappingVersions = parseArray(
    source?.mapping_versions,
    (item) => {
      const version = boundedString(item, 128)
      return version && version.length > 0 ? version : undefined
    },
    32,
  )
  const risk = parseRisk(source?.risk)
  const trend = parseArray(source?.trend, parseTrendPoint, 30, true)
  const recentTasks = parseArray(source?.recent_tasks, parseTaskSummary, 5)
  const attention = parseArray(source?.attention, parseAttention, 5)
  if (
    typeof hasData !== 'boolean' ||
    securityScore === undefined ||
    !mappingVersions ||
    !risk ||
    !trend ||
    !recentTasks ||
    !attention
  ) {
    throw new ApiError('unexpected-response', 200)
  }
  const emptyStateConsistent =
    securityScore === null &&
    mappingVersions.length === 0 &&
    risk.mapping_version === '' &&
    risk.score === 0 &&
    risk.high === 0 &&
    risk.medium === 0 &&
    risk.low === 0 &&
    attention.length === 0 &&
    trend.every((point) =>
      point.completed === 0 &&
      point.security_score === null &&
      point.high === 0 &&
      point.medium === 0 &&
      point.low === 0,
    )
  if ((!hasData && !emptyStateConsistent) || (hasData && securityScore === null)) {
    throw new ApiError('unexpected-response', 200)
  }
  return {
    has_data: hasData,
    security_score: securityScore,
    mapping_versions: mappingVersions,
    risk,
    trend,
    recent_tasks: recentTasks,
    attention,
  }
}

export async function fetchDashboard(signal?: AbortSignal): Promise<DashboardView> {
  const response = await apiRequest<unknown>('/api/v1/platform/dashboard', { signal })
  return parseDashboardView(response)
}
