/**
 * 功能：为 AI 基础设施扫描提供本地目标表达式预览。
 * 实现：逐行展开 IPv4 CIDR、闭区间范围和末尾通配符，并稳定去重、限制总数。
 * 输入：任务创建页中的原始多行目标文本。
 * 输出：仅供界面提示的展开目标与数量，或中文可读错误；服务端仍负责最终校验。
 * 依赖：无运行时依赖。
 */

export const MAX_EXPANDED_TARGETS = 65_536

export type TargetExpressionPreview =
  | { ok: true; count: number; targets: string[] }
  | { ok: false; count: 0; targets: []; error: string }

type IPv4 = readonly [number, number, number, number]
type IPv4Interval = { start: number; count: number }
type ParsedExpression = string[] | IPv4Interval | string
type IPv4Coverage = { intervals: IPv4Interval[]; smallIntervals: Set<string> }

const TOO_MANY_TARGETS_ERROR = `目标数量超过 ${MAX_EXPANDED_TARGETS.toLocaleString('en-US')}，请缩小网段或拆分任务。`
const INVALID_WILDCARD_ERROR = '目标格式无效：IPv4 通配符必须从某一段开始连续出现在末尾，例如 22.2.10.*。'
const INVALID_RANGE_ERROR = '目标格式无效：IPv4 范围必须使用两个合法 IPv4 地址，且起始地址不能大于结束地址。'
const INVALID_IPV4_PORT_RANGE_ERROR = '目标格式无效：不支持 IPv4 端口范围；请使用两个不带端口的 IPv4 地址。'
const INVALID_IPV6_RANGE_ERROR = '目标格式无效：暂不支持 IPv6 范围。'
const TOO_MANY_EXPRESSIONS_ERROR = `目标表达式超过 ${MAX_EXPANDED_TARGETS.toLocaleString('en-US')} 条，请缩小输入范围或拆分任务。`
const IPV4_COVERAGE_THRESHOLD = 64

function failed(error: string): TargetExpressionPreview {
  return { ok: false, count: 0, targets: [], error }
}

function parseIPv4(value: string): IPv4 | undefined {
  const parts = value.split('.')
  if (parts.length !== 4) return undefined
  const octets: number[] = []
  for (const part of parts) {
    if (!/^(0|[1-9]\d{0,2})$/.test(part)) return undefined
    const octet = Number(part)
    if (octet > 255) return undefined
    octets.push(octet)
  }
  return [octets[0], octets[1], octets[2], octets[3]]
}

function ipv4Number(value: IPv4): number {
  return value[0] * 256 ** 3 + value[1] * 256 ** 2 + value[2] * 256 + value[3]
}

function numberIPv4(value: number): string {
  const first = Math.floor(value / 256 ** 3)
  const second = Math.floor(value / 256 ** 2) % 256
  const third = Math.floor(value / 256) % 256
  const fourth = value % 256
  return `${first}.${second}.${third}.${fourth}`
}

function isWebURL(target: string): boolean {
  const lowerTarget = target.toLowerCase()
  return lowerTarget.startsWith('http://') || lowerTarget.startsWith('https://')
}

function isIPv6Literal(value: string): boolean {
  const bracketed = /^\[([^\]]+)\](?::\d+)?$/.exec(value)
  const candidate = bracketed?.[1] ?? value
  if (!candidate.includes(':') || !/^[0-9a-fA-F:.]+$/.test(candidate)) return false
  if ((candidate.match(/::/g) ?? []).length > 1) return false

  const compressed = candidate.includes('::')
  const [left, right] = compressed ? candidate.split('::') : [candidate, '']
  const segments = [
    ...(left ? left.split(':') : []),
    ...(right ? right.split(':') : []),
  ]
  let units = 0
  for (let index = 0; index < segments.length; index += 1) {
    const segment = segments[index]
    if (!segment) return false
    if (segment.includes('.')) {
      if (index !== segments.length - 1 || !parseIPv4(segment)) return false
      units += 2
      continue
    }
    if (!/^[0-9a-fA-F]{1,4}$/.test(segment)) return false
    units += 1
  }
  return compressed ? units < 8 : units === 8
}

function isCIDRExpression(target: string): boolean {
  if (!target.includes('/') || isWebURL(target)) return false
  const [address] = target.split('/', 2)
  return parseIPv4(address) !== undefined || isIPv6Literal(address)
}

function looksLikeIPv4(value: string): boolean {
  return value.length > 0 && /^[0-9.]+$/.test(value)
}

function isIPv4PortRangeExpression(target: string): boolean {
  if (isWebURL(target) || !target.includes('-')) return false
  return target.split('-').some((part) => {
    const separator = part.indexOf(':')
    return separator > 0 && parseIPv4(part.slice(0, separator)) !== undefined
  })
}

function isIPv6RangeExpression(target: string): boolean {
  if (isWebURL(target) || !target.includes('-')) return false
  return target.split('-').some((part) => {
    if (isIPv6Literal(part)) return true
    const closing = part.indexOf(']')
    return part.startsWith('[') && closing > 1 && isIPv6Literal(part.slice(1, closing))
  })
}

function isRangeExpression(target: string): boolean {
  if (isWebURL(target) || !target.includes('-') || !target.includes('.')) return false
  const parts = target.split('-')
  if (parts.length !== 2) return parts.some((part) => parseIPv4(part) !== undefined)
  return parseIPv4(parts[0]) !== undefined
    || (parts[0].includes('.') && parseIPv4(parts[1]) !== undefined)
    || (looksLikeIPv4(parts[0]) && looksLikeIPv4(parts[1]))
}

function parseCIDRInterval(target: string): IPv4Interval | string {
  const match = /^([^/]+)\/(\d{1,2})$/.exec(target)
  if (!match) return '目标格式无效：IPv4 CIDR 不正确。'
  const address = parseIPv4(match[1])
  if (!address) return '目标格式无效：IPv4 CIDR 不正确。'
  const bits = Number(match[2])
  if (bits < 0 || bits > 32) return '目标格式无效：IPv4 CIDR 不正确。'
  const count = 2 ** (32 - bits)
  if (count > MAX_EXPANDED_TARGETS) return TOO_MANY_TARGETS_ERROR
  const start = Math.floor(ipv4Number(address) / count) * count
  return { start, count }
}

function parseRangeInterval(target: string): IPv4Interval | string {
  const parts = target.split('-')
  if (parts.length !== 2) return INVALID_RANGE_ERROR
  const start = parseIPv4(parts[0])
  const end = parseIPv4(parts[1])
  if (!start || !end) return INVALID_RANGE_ERROR
  const first = ipv4Number(start)
  const last = ipv4Number(end)
  if (first > last) return INVALID_RANGE_ERROR
  const count = last - first + 1
  if (count > MAX_EXPANDED_TARGETS) return TOO_MANY_TARGETS_ERROR
  return { start: first, count }
}

function parseWildcardInterval(target: string): IPv4Interval | string {
  const parts = target.split('.')
  if (parts.length !== 4) return INVALID_WILDCARD_ERROR
  let firstStar = -1
  const prefix: number[] = []
  for (let index = 0; index < parts.length; index += 1) {
    const part = parts[index]
    if (part === '*') {
      if (firstStar === -1) firstStar = index
      continue
    }
    if (part.includes('*') || firstStar !== -1) return INVALID_WILDCARD_ERROR
    if (!/^(0|[1-9]\d{0,2})$/.test(part)) return INVALID_WILDCARD_ERROR
    const octet = Number(part)
    if (octet > 255) return INVALID_WILDCARD_ERROR
    prefix.push(octet)
  }
  if (firstStar === -1) return INVALID_WILDCARD_ERROR
  const count = 256 ** (4 - firstStar)
  if (count > MAX_EXPANDED_TARGETS) return TOO_MANY_TARGETS_ERROR
  const start = prefix.reduce((total, octet) => total * 256 + octet, 0) * 256 ** (4 - firstStar)
  return { start, count }
}

function isIPv4Interval(value: ParsedExpression): value is IPv4Interval {
  return typeof value !== 'string' && !Array.isArray(value)
}

function parseExpression(target: string): ParsedExpression {
  // URL 是单一目标；其中的路径和查询参数可合法包含 ~、反斜杠或 *。
  if (/\s/.test(target)) return '目标格式无效：每行只能填写一个目标。'
  if (isWebURL(target)) return [target]
  if (target.includes('\\') || target.includes('~')) {
    return '目标格式无效：范围只能使用标准连字符 -，不能包含反斜杠或波浪号。'
  }
  if (isCIDRExpression(target)) {
    if (isIPv6Literal(target.split('/', 2)[0])) return '目标格式无效：暂不支持 IPv6 CIDR。'
    return parseCIDRInterval(target)
  }
  if (isIPv4PortRangeExpression(target)) return INVALID_IPV4_PORT_RANGE_ERROR
  if (isIPv6RangeExpression(target)) return INVALID_IPV6_RANGE_ERROR
  if (isRangeExpression(target)) return parseRangeInterval(target)
  if (target.includes('*')) return parseWildcardInterval(target)
  if (isIPv6Literal(target)) return '目标格式无效：暂不支持 IPv6 目标。'
  const ipv4 = parseIPv4(target)
  if (ipv4) return { start: ipv4Number(ipv4), count: 1 }
  return [target]
}

function uncoveredIPv4Intervals(covered: IPv4Interval[], interval: IPv4Interval): IPv4Interval[] {
  let start = interval.start
  const end = start + interval.count - 1
  const result: IPv4Interval[] = []
  for (const existing of covered) {
    const existingEnd = existing.start + existing.count - 1
    if (existingEnd < start) continue
    if (existing.start > end) break
    if (existing.start > start) result.push({ start, count: existing.start - start })
    if (existingEnd >= end) return result
    start = existingEnd + 1
  }
  if (start <= end) result.push({ start, count: end - start + 1 })
  return result
}

function addCoveredIPv4Interval(covered: IPv4Interval[], interval: IPv4Interval): IPv4Interval[] {
  let start = interval.start
  let end = start + interval.count - 1
  const result: IPv4Interval[] = []
  let inserted = false
  for (const existing of covered) {
    const existingEnd = existing.start + existing.count - 1
    if (existingEnd + 1 < start) {
      result.push(existing)
      continue
    }
    if (end + 1 < existing.start) {
      if (!inserted) {
        result.push({ start, count: end - start + 1 })
        inserted = true
      }
      result.push(existing)
      continue
    }
    start = Math.min(start, existing.start)
    end = Math.max(end, existingEnd)
  }
  if (!inserted) result.push({ start, count: end - start + 1 })
  return result
}

function intervalKey(interval: IPv4Interval): string {
  return `${interval.start}:${interval.count}`
}

function appendUncoveredIPv4Targets(
  targets: string[],
  seen: Set<string>,
  coverage: IPv4Coverage,
  interval: IPv4Interval,
): string | IPv4Coverage {
  if (interval.count < IPV4_COVERAGE_THRESHOLD) {
    const key = intervalKey(interval)
    if (coverage.smallIntervals.has(key)) return coverage
    for (let offset = 0; offset < interval.count; offset += 1) {
      const target = numberIPv4(interval.start + offset)
      if (seen.has(target)) continue
      if (targets.length >= MAX_EXPANDED_TARGETS) return TOO_MANY_TARGETS_ERROR
      seen.add(target)
      targets.push(target)
    }
    coverage.smallIntervals.add(key)
    return coverage
  }

  const uncovered = uncoveredIPv4Intervals(coverage.intervals, interval)
  if (uncovered.length === 0) return coverage
  for (const part of uncovered) {
    for (let offset = 0; offset < part.count; offset += 1) {
      const target = numberIPv4(part.start + offset)
      if (seen.has(target)) continue
      if (targets.length >= MAX_EXPANDED_TARGETS) return TOO_MANY_TARGETS_ERROR
      seen.add(target)
      targets.push(target)
    }
  }
  return { ...coverage, intervals: addCoveredIPv4Interval(coverage.intervals, interval) }
}

/**
 * 仅用于浏览器即时提示。服务端会将正文与附件合并后重新按同类规则校验。
 */
export function previewTargetExpressions(content: string): TargetExpressionPreview {
  const targets: string[] = []
  const seen = new Set<string>()
  let coverage: IPv4Coverage = { intervals: [], smallIntervals: new Set<string>() }
  let expressionCount = 0
  for (const rawLine of content.split(/\r?\n/)) {
    const expression = rawLine.trim()
    if (!expression) continue
    expressionCount += 1
    if (expressionCount > MAX_EXPANDED_TARGETS) return failed(TOO_MANY_EXPRESSIONS_ERROR)
    const parsed = parseExpression(expression)
    if (typeof parsed === 'string') return failed(parsed)
    if (isIPv4Interval(parsed)) {
      const updatedCoverage = appendUncoveredIPv4Targets(targets, seen, coverage, parsed)
      if (typeof updatedCoverage === 'string') return failed(updatedCoverage)
      coverage = updatedCoverage
      continue
    }
    for (const target of parsed) {
      if (seen.has(target)) continue
      if (targets.length >= MAX_EXPANDED_TARGETS) return failed(TOO_MANY_TARGETS_ERROR)
      seen.add(target)
      targets.push(target)
    }
  }
  return { ok: true, count: targets.length, targets }
}
