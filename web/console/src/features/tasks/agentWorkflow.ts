/**
 * 功能：定义 Agent 工作流表单与摘要共用的安全引用边界。
 * 实现：按后端的长度、字符和凭据前缀规则过滤；不读取任何目标配置。
 * 输入：未知目录或响应字段；输出：安全 ID 或 undefined；依赖：无。
 */
const CREDENTIAL_PREFIX = /^(sk[-_]|ghp_|github_pat_|akia|asia|eyj|bearer |token |password)/i

export function safeAgentReference(value: unknown): string | undefined {
  return typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9._ -]{0,127}$/.test(value)
    && !value.includes('..') && !CREDENTIAL_PREFIX.test(value) ? value : undefined
}

export function safeEvaluationModelReference(value: unknown): string | undefined {
  return typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value)
    && !CREDENTIAL_PREFIX.test(value) ? value : undefined
}

export function safeReportReference(value: unknown): string | undefined {
  return typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$/.test(value)
    && !CREDENTIAL_PREFIX.test(value) ? value : undefined
}
