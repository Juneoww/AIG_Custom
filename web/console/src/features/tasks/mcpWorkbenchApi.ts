/**
 * 功能：读取并白名单投影 MCP 安全扫描工作台的只读响应。
 * 实现：对未知 JSON 严格校验固定字段、枚举、时间、数值和列表上限，再丢弃额外字段。
 * 输入：同源 MCP 工作台端点的 JSON 响应与可选取消信号。
 * 输出：浏览器可安全渲染的 MCPWorkbenchView，或固定 ApiError。
 * 依赖：共享 API 客户端、错误类型及 MCP 安全 DTO。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import type {
  MCPRiskCategory,
  MCPRiskSeverity,
  MCPSourceKind,
  MCPWorkbenchActiveTask,
  MCPWorkbenchMetrics,
  MCPWorkbenchRecentRisk,
  MCPWorkbenchView,
  TaskStatus,
} from '../../shared/api/types'

const MAX_ACTIVE_TASKS = 10
const MAX_RECENT_RISKS = 5
const MAX_SUMMARY_LENGTH = 160
const RFC3339 = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/

const MCP_SOURCE_KINDS = new Set<MCPSourceKind>(['repository', 'service', 'legacy_unknown'])
const MCP_RISK_SEVERITIES = new Set<MCPRiskSeverity>(['high', 'medium', 'low'])
const MCP_RISK_CATEGORIES = new Set<MCPRiskCategory>([
  'dangerous_tool',
  'command_file',
  'authorization',
  'data_leakage',
  'tool_poisoning',
  'skill_mismatch',
  'other',
])
const ACTIVE_TASK_STATUSES = new Set<TaskStatus>(['pending', 'dispatching', 'running', 'dispatch_failed', 'dispatch_unknown'])

function recordOf(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

function boundedString(value: unknown, maximum: number): string | undefined {
  return typeof value === 'string' && value.length > 0 && Array.from(value).length <= maximum ? value : undefined
}

function rfc3339(value: unknown): string | undefined {
  const text = boundedString(value, 64)
  const parts = text?.match(RFC3339)
  if (!parts) return undefined
  const [, yearText, monthText, dayText, hourText, minuteText, secondText, offset] = parts
  const year = Number(yearText)
  const month = Number(monthText)
  const day = Number(dayText)
  const hour = Number(hourText)
  const minute = Number(minuteText)
  const second = Number(secondText)
  if (
    year < 1 ||
    month < 1 || month > 12 ||
    day < 1 || day > daysInMonth(year, month) ||
    hour > 23 || minute > 59 || second > 59 ||
    !validOffset(offset)
  ) return undefined
  return text
}

function daysInMonth(year: number, month: number): number {
  if (month === 2) return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0) ? 29 : 28
  return month === 4 || month === 6 || month === 9 || month === 11 ? 30 : 31
}

function validOffset(value: string): boolean {
  if (value === 'Z') return true
  const offsetHour = Number(value.slice(1, 3))
  const offsetMinute = Number(value.slice(4, 6))
  return offsetHour <= 23 && offsetMinute <= 59
}

function count(value: unknown): number | undefined {
  return Number.isSafeInteger(value) && (value as number) >= 0 ? value as number : undefined
}

function parseMetrics(value: unknown): MCPWorkbenchMetrics | undefined {
  const source = recordOf(value)
  const running = count(source?.running)
  const pending = count(source?.pending)
  const highRisk = count(source?.high_risk)
  const completed30d = count(source?.completed_30d)
  if (running === undefined || pending === undefined || highRisk === undefined || completed30d === undefined) return undefined
  return { running, pending, high_risk: highRisk, completed_30d: completed30d }
}

function parseActiveTask(value: unknown): MCPWorkbenchActiveTask | undefined {
  const source = recordOf(value)
  const taskID = boundedString(source?.task_id, 256)
  const label = boundedString(source?.label, 160)
  const sourceKind = boundedString(source?.source_kind, 32) as MCPSourceKind | undefined
  const phase = source?.phase === null ? null : boundedString(source?.phase, 128)
  const status = boundedString(source?.status, 32) as TaskStatus | undefined
  const updatedAt = rfc3339(source?.updated_at)
  if (!taskID || !label || !sourceKind || !MCP_SOURCE_KINDS.has(sourceKind) || phase === undefined || !status || !ACTIVE_TASK_STATUSES.has(status) || !updatedAt) {
    return undefined
  }
  return { task_id: taskID, label, source_kind: sourceKind, phase, status, updated_at: updatedAt }
}

function parseRecentRisk(value: unknown): MCPWorkbenchRecentRisk | undefined {
  const source = recordOf(value)
  const reportID = boundedString(source?.report_id, 256)
  const taskID = boundedString(source?.task_id, 256)
  const severity = boundedString(source?.severity, 16) as MCPRiskSeverity | undefined
  const category = boundedString(source?.category, 32) as MCPRiskCategory | undefined
  const summary = boundedString(source?.summary, MAX_SUMMARY_LENGTH)
  const completedAt = rfc3339(source?.completed_at)
  if (!reportID || !taskID || !severity || !MCP_RISK_SEVERITIES.has(severity) || !category || !MCP_RISK_CATEGORIES.has(category) || !summary || !completedAt) {
    return undefined
  }
  return { report_id: reportID, task_id: taskID, severity, category, summary, completed_at: completedAt }
}

function parseArray<T>(value: unknown, maximum: number, parse: (item: unknown) => T | undefined): T[] | undefined {
  if (!Array.isArray(value) || value.length > maximum) return undefined
  const parsed = value.map(parse)
  return parsed.every((item): item is T => item !== undefined) ? parsed : undefined
}

export function parseMCPWorkbenchView(value: unknown): MCPWorkbenchView {
  const source = recordOf(value)
  const metrics = parseMetrics(source?.metrics)
  const activeTasks = parseArray(source?.active_tasks, MAX_ACTIVE_TASKS, parseActiveTask)
  const recentRisks = parseArray(source?.recent_risks, MAX_RECENT_RISKS, parseRecentRisk)
  if (!metrics || !activeTasks || !recentRisks) throw new ApiError('unexpected-response', 200)
  return { metrics, active_tasks: activeTasks, recent_risks: recentRisks }
}

export async function fetchMCPWorkbench(signal?: AbortSignal): Promise<MCPWorkbenchView> {
  return parseMCPWorkbenchView(await apiRequest<unknown>('/api/v1/platform/mcp-workbench', { signal }))
}
