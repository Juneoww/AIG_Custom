/** 功能：验证模型探测 API 的安全结果和连接范围；实现：受控响应与URL用例；输入：虚构DTO；输出：安全投影和固定错误；依赖：Vitest。 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { normalizeModelBaseURL, parseModelProbe, testModelConnection } from './probe'

afterEach(() => vi.unstubAllGlobals())
describe('模型探测 API', () => {
  it('只保留固定代码和耗时，不展示服务端说明或密钥', () => {
    expect(parseModelProbe({ status: 'success', code: 'ok', elapsed_ms: 12, message: 'secret-text', token: 'secret' })).toEqual({ status: 'success', code: 'ok', elapsed_ms: 12, message: '连接成功，模型可正常响应。' })
  })
  it.each([{ status: 'error', code: 'ok', elapsed_ms: 1 }, { status: 'success', code: 'timeout', elapsed_ms: 1 }, { status: 'error', code: 'invented', elapsed_ms: 1 }, { status: 'success', code: 'ok', elapsed_ms: -1 }, { status: 'success', code: 'ok', elapsed_ms: 0.1 }, null])('拒绝不一致的未知结果 %j', value => {
    expect(() => parseModelProbe(value)).toThrow()
  })
  it('规范化完整URL但不合并不同API路径', () => {
    expect(normalizeModelBaseURL('HTTP://MODELS.INTERNAL:80/v1/')).toBe('http://models.internal/v1')
    expect(normalizeModelBaseURL('https://models.internal:443/v2')).toBe('https://models.internal/v2')
    for (const value of ['ftp://models.internal', 'http://user:pass@models.internal', 'http://models.internal/v1?key=value', 'http://models.internal/#', 'http:\\models.internal']) expect(normalizeModelBaseURL(value)).toBeUndefined()
  })
  it('无效模型ID引用和掩码Token不能发送请求', async () => {
    const fetcher = vi.fn()
    vi.stubGlobal('fetch', fetcher)
    await expect(testModelConnection({ provider_model: 'internal', base_url: 'http://models.internal/v1', token: '********' })).rejects.toThrow()
    await expect(testModelConnection({ provider_model: 'internal', base_url: 'http://models.internal/v1' }, '../escape')).rejects.toThrow()
    expect(fetcher).not.toHaveBeenCalled()
  })
})
