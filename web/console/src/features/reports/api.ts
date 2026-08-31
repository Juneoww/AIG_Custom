/**
 * 功能：读取不可变报告摘要、详情并导出同一快照的受审计 PDF。
 * 实现：把未知响应严格投影为有界白名单 DTO，并复用共享客户端的 CSRF 与受控流。
 * 输入：服务端分页参数、opaque 报告 ID、JSON 响应与 PDF 响应流。
 * 输出：安全报告视图或不含响应原文的固定 API 错误。
 * 依赖：共享同源 API 客户端、固定错误类型与浏览器 AbortSignal。
 */
import { apiBinaryRequest, apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'

export type ReportTaskType = 'mcp_scan' | 'ai_infra_scan' | 'model_redteam_report' | 'agent_scan'
export type RiskSeverity = 'high' | 'medium' | 'low'
export type InfrastructurePortScanMode = 'fixed_ai' | 'full_tcp'

export interface RiskSummaryView {
  mapping_version: string
  high: number
  medium: number
  low: number
  score: number
}

export interface ReportSummaryView {
  id: string
  task_id: string
  task_type: ReportTaskType
  completed_at: string
  created_at: string
  risk: RiskSummaryView
  brand_product_name: string
}

export interface ReportListView {
  items: ReportSummaryView[]
  total: number
  page: number
  page_size: number
}

export interface TrendPointView {
  date: string
  completed: number
  high: number
  medium: number
  low: number
}

export interface TopRiskView {
  severity: RiskSeverity
  count: number
  impact: string
  remediation: string
}

export interface TechnicalFindingView {
  title: string
  evidence: string
  impact: string
  remediation: string
}

export interface RenderModelView {
  render_version: 'report-render-v2'
  mapping_version: string
  generated_at: string
  completed_at: string
  task_id: string
  task_type: ReportTaskType
  product_name: string
  primary_color: string
  watermark: string
  risk: RiskSummaryView
  score_explanation: string
  risk_trend: TrendPointView[]
  risk_distribution: { high: number; medium: number; low: number }
  top_risks: TopRiskView[]
  technical_findings: TechnicalFindingView[]
  recommendations: string[]
  coverage: string
  conclusion: string
  port_scan_mode?: InfrastructurePortScanMode
  port_spec?: string
}

export interface ReportDetailView {
  id: string
  task_id: string
  task_type: ReportTaskType
  completed_at: string
  created_at: string
  risk: RiskSummaryView
  render: RenderModelView
}

const TASK_TYPES = new Set<ReportTaskType>(['mcp_scan', 'ai_infra_scan', 'model_redteam_report', 'agent_scan'])
const SEVERITIES = new Set<RiskSeverity>(['high', 'medium', 'low'])
const MAX_PDF_BYTES = 50 * 1024 * 1024
const FIXED_AI_PORT_SPEC = '11434,1337,7000-9000,18789'
const FULL_TCP_PORT_SPEC = '1-65535'

type InfrastructurePortScanSnapshot = Pick<RenderModelView, 'port_scan_mode' | 'port_spec'>

function recordOf(value: unknown): Record<string, unknown> | undefined {
  return typeof value === 'object' && value !== null ? value as Record<string, unknown> : undefined
}

function boundedString(value: unknown, maximum = 256, allowEmpty = false): string | undefined {
  return typeof value === 'string' && value.length <= maximum && (allowEmpty || value.length > 0) ? value : undefined
}

function boundedOpaqueID(value: unknown): string | undefined {
  const id = boundedString(value)
  return id === '.' || id === '..' ? undefined : id
}

const RFC3339_PATTERN = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|([+-])(\d{2}):(\d{2}))$/

function instantNanoseconds(value: unknown): bigint | undefined {
  const text = boundedString(value, 64)
  const match = text?.match(RFC3339_PATTERN)
  if (!match) return undefined
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const hour = Number(match[4])
  const minute = Number(match[5])
  const second = Number(match[6])
  const offsetHour = match[8] === 'Z' ? 0 : Number(match[10])
  const offsetMinute = match[8] === 'Z' ? 0 : Number(match[11])
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const days = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  if (month < 1 || month > 12 || day < 1 || day > days[month - 1]! || hour > 23 || minute > 59 ||
    second > 59 || offsetHour > 23 || offsetMinute > 59) return undefined
  const local = new Date(0)
  local.setUTCFullYear(year, month - 1, day)
  local.setUTCHours(hour, minute, second, 0)
  const offset = (offsetHour * 60 + offsetMinute) * (match[9] === '-' ? -1 : 1)
  const fraction = BigInt((match[7] ?? '').padEnd(9, '0') || '0')
  return BigInt(local.getTime() - offset * 60_000) * 1_000_000n + fraction
}

function dateString(value: unknown): string | undefined {
  const text = boundedString(value, 64)
  return text && instantNanoseconds(text) !== undefined ? text : undefined
}

function samePersistedInstant(left: string, right: string): boolean {
  const leftNanos = instantNanoseconds(left)
  const rightNanos = instantNanoseconds(right)
  const persistedMicroseconds = (value: bigint) => value >= 0 ? value / 1_000n : (value - 999n) / 1_000n
  return leftNanos !== undefined && rightNanos !== undefined &&
    persistedMicroseconds(leftNanos) === persistedMicroseconds(rightNanos)
}

function count(value: unknown, maximum = 2_147_483_647): number | undefined {
  return Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= maximum ? value as number : undefined
}

function taskType(value: unknown): ReportTaskType | undefined {
  const text = boundedString(value, 32) as ReportTaskType | undefined
  return text && TASK_TYPES.has(text) ? text : undefined
}

function parseRisk(value: unknown): RiskSummaryView | undefined {
  const source = recordOf(value)
  const mappingVersion = boundedString(source?.mapping_version, 128, true)
  const high = count(source?.high)
  const medium = count(source?.medium)
  const low = count(source?.low)
  const score = count(source?.score, 100)
  return mappingVersion !== undefined && high !== undefined && medium !== undefined && low !== undefined && score !== undefined
    ? { mapping_version: mappingVersion, high, medium, low, score }
    : undefined
}

function parseSummary(value: unknown): ReportSummaryView | undefined {
  const source = recordOf(value)
  const id = boundedOpaqueID(source?.id)
  const taskID = boundedString(source?.task_id)
  const type = taskType(source?.task_type)
  const completedAt = dateString(source?.completed_at)
  const createdAt = dateString(source?.created_at)
  const risk = parseRisk(source?.risk)
  const productName = boundedString(source?.brand_product_name, 128)
  return id && taskID && type && completedAt && createdAt && risk && productName
    ? { id, task_id: taskID, task_type: type, completed_at: completedAt, created_at: createdAt, risk, brand_product_name: productName }
    : undefined
}

function parseArray<T>(value: unknown, maximum: number, parser: (item: unknown) => T | undefined): T[] | undefined {
  if (!Array.isArray(value) || value.length > maximum) return undefined
  const parsed = value.map(parser)
  return parsed.every((item): item is T => item !== undefined) ? parsed : undefined
}

export function parseReportList(value: unknown): ReportListView {
  const source = recordOf(value)
  const items = parseArray(source?.items, 100, parseSummary)
  const total = count(source?.total)
  const page = count(source?.page, 1_000)
  const pageSize = count(source?.page_size, 100)
  if (!items || total === undefined || !page || !pageSize) throw new ApiError('unexpected-response', 200)
  return { items, total, page, page_size: pageSize }
}

function parseTrendPoint(value: unknown): TrendPointView | undefined {
  const source = recordOf(value)
  const date = dateString(source?.date)
  const completed = count(source?.completed)
  const high = count(source?.high)
  const medium = count(source?.medium)
  const low = count(source?.low)
  return date && completed !== undefined && high !== undefined && medium !== undefined && low !== undefined
    ? { date, completed, high, medium, low }
    : undefined
}

function parseTopRisk(value: unknown): TopRiskView | undefined {
  const source = recordOf(value)
  const severity = boundedString(source?.severity, 16) as RiskSeverity | undefined
  const riskCount = count(source?.count)
  const impact = boundedString(source?.impact, 16_384, true)
  const remediation = boundedString(source?.remediation, 16_384, true)
  return severity && SEVERITIES.has(severity) && riskCount !== undefined && impact !== undefined && remediation !== undefined
    ? { severity, count: riskCount, impact, remediation }
    : undefined
}

function parseFinding(value: unknown): TechnicalFindingView | undefined {
  const source = recordOf(value)
  const title = boundedString(source?.title, 2_048)
  const evidence = boundedString(source?.evidence, 16_384, true)
  const impact = boundedString(source?.impact, 16_384, true)
  const remediation = boundedString(source?.remediation, 16_384, true)
  return title && evidence !== undefined && impact !== undefined && remediation !== undefined
    ? { title, evidence, impact, remediation }
    : undefined
}

function parseInfrastructurePortScan(source: Record<string, unknown>, taskType: ReportTaskType | undefined): InfrastructurePortScanSnapshot | null | undefined {
  if (source.port_scan_mode === undefined && source.port_spec === undefined) return undefined
  if (taskType !== 'ai_infra_scan') return null
  if (source.port_scan_mode === 'fixed_ai' && source.port_spec === FIXED_AI_PORT_SPEC) {
    return { port_scan_mode: 'fixed_ai', port_spec: FIXED_AI_PORT_SPEC }
  }
  if (source.port_scan_mode === 'full_tcp' && source.port_spec === FULL_TCP_PORT_SPEC) {
    return { port_scan_mode: 'full_tcp', port_spec: FULL_TCP_PORT_SPEC }
  }
  return null
}

function parseRender(value: unknown): RenderModelView | undefined {
  const source = recordOf(value)
  if (source?.render_version !== 'report-render-v2') return undefined
  const mappingVersion = boundedString(source.mapping_version, 128)
  const generatedAt = dateString(source.generated_at)
  const completedAt = dateString(source.completed_at)
  const taskID = boundedString(source.task_id)
  const type = taskType(source.task_type)
  const productName = boundedString(source.product_name, 128)
  const primaryColor = boundedString(source.primary_color, 7)
  const watermark = boundedString(source.watermark, 256, true)
  const risk = parseRisk(source.risk)
  const explanation = boundedString(source.score_explanation, 16_384, true)
  const trend = parseArray(source.risk_trend, 30, parseTrendPoint)
  const distributionSource = recordOf(source.risk_distribution)
  const high = count(distributionSource?.high)
  const medium = count(distributionSource?.medium)
  const low = count(distributionSource?.low)
  const topRisks = parseArray(source.top_risks, 3, parseTopRisk)
  const findings = parseArray(source.technical_findings, 50, parseFinding)
  const recommendations = parseArray(source.recommendations, 50, (item) => boundedString(item, 16_384))
  const coverage = boundedString(source.coverage, 16_384, true)
  const conclusion = boundedString(source.conclusion, 16_384, true)
  const infrastructurePortScan = parseInfrastructurePortScan(source, type)
  if (!mappingVersion || !generatedAt || !completedAt || !taskID || !type || !productName || !primaryColor ||
    !/^#[0-9a-f]{6}$/i.test(primaryColor) || watermark === undefined || !risk || explanation === undefined ||
    !trend || trend.length !== 30 || high === undefined || medium === undefined || low === undefined || !topRisks ||
    !findings || !recommendations || coverage === undefined || conclusion === undefined || infrastructurePortScan === null) return undefined
  const daysAreStable = trend.every((point, index) => {
    const current = new Date(point.date)
    if (current.getUTCHours() !== 0 || current.getUTCMinutes() !== 0 || current.getUTCSeconds() !== 0 || current.getUTCMilliseconds() !== 0) return false
    return index === 0 || current.getTime() - new Date(trend[index - 1]!.date).getTime() === 86_400_000
  })
  if (!daysAreStable || risk.mapping_version !== mappingVersion || high !== risk.high || medium !== risk.medium || low !== risk.low) return undefined
  return {
    render_version: 'report-render-v2', mapping_version: mappingVersion, generated_at: generatedAt,
    completed_at: completedAt, task_id: taskID, task_type: type, product_name: productName,
    primary_color: primaryColor, watermark, risk, score_explanation: explanation, risk_trend: trend,
    risk_distribution: { high, medium, low }, top_risks: topRisks, technical_findings: findings,
    recommendations, coverage, conclusion, ...infrastructurePortScan,
  }
}

function sameRisk(left: RiskSummaryView, right: RiskSummaryView): boolean {
  return left.mapping_version === right.mapping_version && left.high === right.high && left.medium === right.medium &&
    left.low === right.low && left.score === right.score
}

export function parseReportDetail(value: unknown): ReportDetailView {
  const source = recordOf(value)
  const id = boundedOpaqueID(source?.id)
  const taskID = boundedString(source?.task_id)
  const type = taskType(source?.task_type)
  const completedAt = dateString(source?.completed_at)
  const createdAt = dateString(source?.created_at)
  const risk = parseRisk(source?.risk)
  const render = parseRender(source?.render)
  if (!id || !taskID || !type || !completedAt || !createdAt || !risk || !render || render.task_id !== taskID ||
    render.task_type !== type || !samePersistedInstant(render.completed_at, completedAt) ||
    !samePersistedInstant(render.generated_at, createdAt) || !sameRisk(risk, render.risk)) {
    throw new ApiError('unexpected-response', 200)
  }
  return { id, task_id: taskID, task_type: type, completed_at: completedAt, created_at: createdAt, risk, render }
}

function reportPath(id: string): string {
  const opaqueID = boundedOpaqueID(id)
  if (!opaqueID) throw new ApiError('bad-request', 0)
  return `/api/v1/platform/reports/${encodeURIComponent(opaqueID)}`
}

export async function fetchReportList(page: number, pageSize = 20, signal?: AbortSignal): Promise<ReportListView> {
  if (!Number.isSafeInteger(page) || page < 1 || page > 1_000 || !Number.isSafeInteger(pageSize) || pageSize < 1 || pageSize > 100) {
    throw new ApiError('bad-request', 0)
  }
  return parseReportList(await apiRequest<unknown>(`/api/v1/platform/reports?page=${page}&page_size=${pageSize}`, { signal }))
}

export async function fetchReportDetail(id: string, signal?: AbortSignal): Promise<ReportDetailView> {
  return parseReportDetail(await apiRequest<unknown>(reportPath(id), { signal }))
}

export async function exportReportPDF(id: string, signal?: AbortSignal): Promise<Blob> {
  const response = await apiBinaryRequest(
    `${reportPath(id)}/exports/pdf`,
    { method: 'POST', signal },
    { expectedStatus: 200 },
    { expectedContentType: 'application/pdf', maximumBytes: MAX_PDF_BYTES },
  )
  return response.blob
}
