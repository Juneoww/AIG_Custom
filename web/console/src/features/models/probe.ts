/**
 * 功能：提交模型连通性测试并投影安全结果。
 * 实现：只发送连接字段，按固定错误代码生成文案，忽略服务端自由文本。
 * 输入：当前表单、可选已保存模型 ID、取消信号；输出：无凭据的测试结果。
 * 依赖：共享 Cookie/CSRF API 客户端；不写入查询缓存或浏览器存储。
 */
import { apiRequest } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'

const messages = {
  ok: '连接成功，模型可正常响应。',
  invalid_config: '模型配置无效，请检查模型ID、基础 URL 和访问 Token。',
  authentication_failed: '认证失败，请检查访问 Token 和模型权限。',
  model_not_found: '模型或接口不存在，请检查模型ID和基础 URL。',
  rate_limited: '模型服务请求受限，请稍后重试。',
  timeout: '连接超时，请检查模型服务状态和网络。',
  network_error: '无法连接模型服务，请检查网络、地址和 HTTPS 证书。',
  invalid_response: '模型服务未返回有效的文本响应，请检查接口兼容性。',
  upstream_error: '模型服务暂时异常，请稍后重试。',
  redirect_blocked: '模型接口发生重定向，请直接填写最终的基础 URL。',
  busy: '测试过于频繁或正在进行，请稍后重试。',
  unavailable: '模型测试暂不可用，请稍后重试。',
} as const

export interface ModelProbeInput { provider_model: string; base_url: string; token?: string }
export interface ModelProbeResult { status: 'success' | 'error'; code: keyof typeof messages; message: string; elapsed_ms: number }

// 比较完整 API 基础地址，保留路径，只合并默认端口和尾斜线等语义等价差异。
export function normalizeModelBaseURL(value: string): string | undefined {
  const raw = value.trim()
  if (!/^https?:\/\//i.test(raw) || /[?#\\\s]/.test(raw)) return undefined
  try {
    const url = new URL(raw)
    if (url.username || url.password) return undefined
    return `${url.origin}${url.pathname.replace(/\/+$/, '')}`
  } catch { return undefined }
}

export function parseModelProbe(value: unknown): ModelProbeResult {
  const item = value as Partial<ModelProbeResult> | null
  if (!item || typeof item !== 'object' || typeof item.code !== 'string' || !Object.hasOwn(messages, item.code) ||
      (item.status !== 'success' && item.status !== 'error') || (item.status === 'success') !== (item.code === 'ok') ||
      !Number.isSafeInteger(item.elapsed_ms) || (item.elapsed_ms as number) < 0) throw new ApiError('unexpected-response', 200)
  return { status: item.status, code: item.code, message: messages[item.code], elapsed_ms: item.elapsed_ms as number }
}

export async function testModelConnection(input: ModelProbeInput, id?: string, signal?: AbortSignal): Promise<ModelProbeResult> {
  if (!input.provider_model.trim() || input.provider_model.length > 512 || input.base_url.length > 2048 || !normalizeModelBaseURL(input.base_url) ||
      (!id && !input.token?.trim()) || input.token === '********' || (input.token?.length ?? 0) > 8192 ||
      (id !== undefined && !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(id))) throw new ApiError('bad-request', 0)
  const path = id ? `/api/v1/platform/models/${encodeURIComponent(id)}/test` : '/api/v1/platform/models/test'
  const body = { provider_model: input.provider_model.trim(), base_url: input.base_url.trim(), ...(input.token ? { token: input.token } : {}) }
  return parseModelProbe(await apiRequest<unknown>(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body), signal }, { expectedStatus: 200 }))
}

export function modelProbeError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.kind === 'bad-request') return messages.invalid_config
    if (error.status === 401) return '登录状态已失效，请重新登录。'
    if (error.status === 403) return '无权测试此模型，或请求未通过安全校验。'
    if (error.status === 404) return '模型配置已不存在，请刷新后重试。'
    if (error.status === 429) return messages.busy
    if (error.status === 426) return '请通过 HTTPS 访问平台后再测试模型。'
  }
  return '模型测试未完成，请检查网络后重试。'
}
