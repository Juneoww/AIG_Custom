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

const TOO_MANY_TARGETS_ERROR = `目标数量超过 ${MAX_EXPANDED_TARGETS.toLocaleString('en-US')}，请缩小网段或拆分任务。`
const INVALID_WILDCARD_ERROR = '目标格式无效：IPv4 通配符必须从某一段开始连续出现在末尾，例如 22.2.10.*。'
const INVALID_RANGE_ERROR = '目标格式无效：IPv4 范围必须使用两个合法地址，且起始地址不能大于结束地址。'

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
  return target.startsWith('http://') || target.startsWith('https://')
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

function isRangeExpression(target: string): boolean {
  if (isWebURL(target) || !target.includes('-') || !target.includes('.')) return false
  const parts = target.split('-')
  if (parts.length !== 2) return parts.some((part) => parseIPv4(part) !== undefined)
  return parseIPv4(parts[0]) !== undefined
    || parseIPv4(parts[1]) !== undefined
    || (looksLikeIPv4(parts[0]) && looksLikeIPv4(parts[1]))
}

function expandNumbers(start: number, count: number): string[] {
  const expanded = new Array<string>(count)
  for (let index = 0; index < count; index += 1) {
    expanded[index] = numberIPv4(start + index)
  }
  return expanded
}

function expandCIDR(target: string): string[] | string {
  const match = /^([^/]+)\/(\d{1,2})$/.exec(target)
  if (!match) return '目标格式无效：IPv4 CIDR 不正确。'
  const address = parseIPv4(match[1])
  if (!address) return '目标格式无效：IPv4 CIDR 不正确。'
  const bits = Number(match[2])
  if (bits < 0 || bits > 32) return '目标格式无效：IPv4 CIDR 不正确。'
  const count = 2 ** (32 - bits)
  if (count > MAX_EXPANDED_TARGETS) return TOO_MANY_TARGETS_ERROR
  const start = Math.floor(ipv4Number(address) / count) * count
  return expandNumbers(start, count)
}

function expandRange(target: string): string[] | string {
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
  return expandNumbers(first, count)
}

function expandWildcard(target: string): string[] | string {
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
  return expandNumbers(start, count)
}

function expandExpression(target: string): string[] | string {
  if (target.includes('\\') || target.includes('~')) {
    return '目标格式无效：范围只能使用标准连字符 -，不能包含反斜杠或波浪号。'
  }
  if (isCIDRExpression(target)) {
    if (isIPv6Literal(target.split('/', 2)[0])) return '目标格式无效：暂不支持 IPv6 CIDR。'
    return expandCIDR(target)
  }
  if (isRangeExpression(target)) return expandRange(target)
  if (target.includes('*')) return expandWildcard(target)
  if (/\s/.test(target)) return '目标格式无效：每行只能填写一个目标。'
  if (isIPv6Literal(target)) return '目标格式无效：暂不支持 IPv6 目标。'
  return [target]
}

/**
 * 仅用于浏览器即时提示。服务端会将正文与附件合并后重新按同类规则校验。
 */
export function previewTargetExpressions(content: string): TargetExpressionPreview {
  const targets: string[] = []
  const seen = new Set<string>()
  for (const rawLine of content.split(/\r?\n/)) {
    const expression = rawLine.trim()
    if (!expression) continue
    const expanded = expandExpression(expression)
    if (typeof expanded === 'string') return failed(expanded)
    for (const target of expanded) {
      if (seen.has(target)) continue
      if (targets.length >= MAX_EXPANDED_TARGETS) return failed(TOO_MANY_TARGETS_ERROR)
      seen.add(target)
      targets.push(target)
    }
  }
  return { ok: true, count: targets.length, targets }
}
