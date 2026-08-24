/**
 * 功能：验证知识库兼容响应、安全 DTO、原文读取和治理写请求合同。
 * 实现：以受控 fetch 响应覆盖六类资源、opaque 标识、CSRF 与失败泛化。
 * 输入：未知 legacy JSON、恶意额外字段、原文字节和写操作参数。
 * 输出：白名单领域 DTO 或固定 ApiError，且写请求最多一次。
 * 依赖：Vitest、共享 API 客户端与知识库领域适配器。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/errors'
import {
  createPromptCollection,
  deletePromptCollection,
  fetchAgentConfig,
  fetchAgentNames,
  fetchAgentTemplates,
  fetchEvaluationPage,
  fetchFingerprintPage,
  fetchMCPPlugins,
  fetchPromptCollections,
  fetchRawKnowledge,
  fetchVulnerabilityPage,
  saveAgentConfig,
  testAgentPrompt,
  updateMCPPlugin,
} from './api'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'aig_csrf=; Max-Age=0; Path=/'
})

describe('knowledge legacy adapter', () => {
  it('只投影六类读取响应的安全白名单字段', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({
        status: 0,
        message: 'success',
        data: { total: 1, page: 1, size: 20, items: [{ info: { name: 'dify', author: 'lab', desc: 'desc', severity: 'info', recommendation: 9 }, http: [], version: [], raw_result: 'RAW_SENTINEL', token: 'TOKEN_SENTINEL' }] },
      }))
      .mockResolvedValueOnce(jsonResponse({
        status: 0,
        message: 'success',
        data: { total: 1, page: 1, size: 20, items: [{ info: { name: 'dify', cve: 'CVE-2026-1', summary: 'summary', details: 'details', cvss: '9.8', severity: 'high', security_advise: 'fix', references: ['https://example.invalid/advisory'], author: 'lab' }, rule: 'version < 2', references: [], path: 'PATH_SENTINEL' }] },
      }))
      .mockResolvedValueOnce(jsonResponse({
        status: 0,
        message: 'success',
        data: { total: 1, page: 1, size: 20, items: [{ name: 'safe', description: 'desc', description_zh: '中文', author: 'lab', source: ['source'], count: 2, default: false, tags: ['tag'], recommendation: 8, language: 'zh', data: null, token: 'TOKEN_SENTINEL' }] },
      }))
      .mockResolvedValueOnce(jsonResponse({
        status: 0,
        message: 'success',
        data: { total: 1, items: [{ info: { id: 'cors', name: 'CORS', description: 'desc', author: 'lab', category: ['code'] }, rules: [], prompt_template: 'prompt', RawData: 'info:\n  id: cors\n', secret: 'SECRET_SENTINEL' }] },
      }))
      .mockResolvedValueOnce(jsonResponse({
        status: 0,
        message: 'success',
        data: { total: 1, items: [{ id: 'prompt-1', product: 'product', affiliation: 'lab', model_version: 'v1', prompt: 'prompt', code_exec: false, upload_file: false, multi_modal: false, web_search: false, sec_policies: true, token: 'TOKEN_SENTINEL' }] },
      }))
      .mockResolvedValueOnce(jsonResponse({ status: 0, message: 'success', data: ['openai', 'private-agent'] }))
      .mockResolvedValueOnce(jsonResponse({ status: 0, message: 'success', data: 'provider: openai\napi_key: ${ENV}\n' }))
    vi.stubGlobal('fetch', fetchMock)

    const fingerprint = await fetchFingerprintPage({ page: 1, size: 20, query: '' })
    const vulnerability = await fetchVulnerabilityPage({ page: 1, size: 20, query: '' })
    const evaluation = await fetchEvaluationPage({ page: 1, size: 20, query: '' })
    const mcp = await fetchMCPPlugins()
    const prompts = await fetchPromptCollections()
    const names = await fetchAgentNames()
    const agent = await fetchAgentConfig('openai')

    expect(fingerprint.items[0]).toEqual({ name: 'dify', author: 'lab', description: 'desc', severity: 'info', recommendation: 9 })
    expect(vulnerability.items[0]).toEqual({ cve: 'CVE-2026-1', fingerprint: 'dify', summary: 'summary', details: 'details', cvss: '9.8', severity: 'high', advice: 'fix', references: ['https://example.invalid/advisory'], author: 'lab', rule: 'version < 2' })
    expect(evaluation.items[0]).not.toHaveProperty('token')
    expect(mcp[0]).toEqual({ id: 'cors', name: 'CORS', description: 'desc', author: 'lab', categories: ['code'] })
    expect(mcp[0]).not.toHaveProperty('rawContent')
    expect(prompts[0]).not.toHaveProperty('token')
    expect(names).toEqual(['openai', 'private-agent'])
    expect(agent).toEqual({ name: 'openai', content: 'provider: openai\napi_key: ${ENV}\n' })
    expect(JSON.stringify({ fingerprint, vulnerability, evaluation, mcp, prompts, names, agent })).not.toContain('SENTINEL')
  })

  it('status非零时忽略服务端原始message并抛固定错误', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ status: 1, message: 'C:\\secret\\rules.yaml TOKEN_SENTINEL', data: null })))

    await expect(fetchMCPPlugins()).rejects.toMatchObject({ name: 'ApiError', kind: 'unexpected-response' })
    await expect(fetchMCPPlugins()).rejects.not.toThrow(/secret|TOKEN_SENTINEL/)
  })

  it('拒绝畸形分页、原型污染键和过深Prompt结构', async () => {
    vi.stubGlobal('fetch', vi.fn()
      .mockResolvedValueOnce(jsonResponse({ status: 0, message: 'success', data: { total: 1, page: 1, size: 20, items: [{ info: { name: '__proto__', author: '', desc: '', severity: '' } }] } }))
      .mockResolvedValueOnce(jsonResponse({ status: 0, message: 'success', data: { total: 1, items: [{ id: 'prompt-1', product: {}, affiliation: '', model_version: '', prompt: '', code_exec: false, upload_file: false, multi_modal: false, web_search: false, sec_policies: false }] } })))

    await expect(fetchFingerprintPage({ page: 1, size: 20, query: '' })).rejects.toBeInstanceOf(ApiError)
    await expect(fetchPromptCollections()).rejects.toBeInstanceOf(ApiError)
  })

  it('严格投影Agent动态表单模板并拒绝危险字段路径', async () => {
    vi.stubGlobal('fetch', vi.fn()
      .mockResolvedValueOnce(jsonResponse({ http: { name: 'HTTP接口', description: 'desc', fields: [{ field: 'url', label: 'URL', type: 'text', required: true, placeholder: 'https://example.invalid' }] }, common: { name: '通用配置', description: '', fields: [{ field: 'timeout_ms', label: '超时', type: 'number', required: false, min: 1000, max: 300000, defaultValue: 30000 }] } }))
      .mockResolvedValueOnce(jsonResponse({ bad: { name: 'bad', description: '', fields: [{ field: '__proto__.token', label: '危险', type: 'password', required: true }] } })))

    await expect(fetchAgentTemplates()).resolves.toEqual([
      { id: 'http', name: 'HTTP接口', description: 'desc', fields: [{ field: 'url', label: 'URL', type: 'text', required: true, placeholder: 'https://example.invalid' }, { field: 'timeout_ms', label: '超时', type: 'number', required: false, minimum: 1000, maximum: 300000, defaultValue: 30000 }] },
    ])
    await expect(fetchAgentTemplates()).rejects.toBeInstanceOf(ApiError)
  })
})

describe('knowledge raw and mutation contracts', () => {
  it('读取独立raw路由并保持内容字节，不接受点段ID', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ status: 0, message: 'success', data: { content: '# comment\nkey: &anchor value\ncopy: *anchor\n' } }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(fetchRawKnowledge('fingerprint', 'demo')).resolves.toBe('# comment\nkey: &anchor value\ncopy: *anchor\n')
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/knowledge/fingerprints/demo/raw')
    await expect(fetchRawKnowledge('fingerprint', '.')).rejects.toMatchObject({ kind: 'bad-request' })
    await expect(fetchRawKnowledge('evaluation', '..')).rejects.toMatchObject({ kind: 'bad-request' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('Prompt创建删除和MCP更新使用真实路由、当前CSRF且每次仅写一次', async () => {
    document.cookie = 'aig_csrf=fresh-token; Path=/'
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(jsonResponse({ status: 0, message: 'created' })))
    vi.stubGlobal('fetch', fetchMock)

    const prompt = { id: 'prompt-1', product: 'product', affiliation: 'lab', modelVersion: 'v1', prompt: 'text', codeExec: false, uploadFile: false, multiModal: false, webSearch: false, securityPolicies: true }
    await createPromptCollection(prompt)
    await deletePromptCollection('prompt-1')
    await updateMCPPlugin('cors', 'info:\n  id: cors\n')

    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(fetchMock.mock.calls.map((call) => [new URL(String(call[0])).pathname, (call[1] as RequestInit).method])).toEqual([
      ['/api/v1/knowledge/prompt_collections', 'POST'],
      ['/api/v1/knowledge/prompt_collections/prompt-1', 'DELETE'],
      ['/api/v1/knowledge/mcp/cors', 'PUT'],
    ])
    for (const call of fetchMock.mock.calls) {
      expect(new Headers((call[1] as RequestInit).headers).get('X-CSRF-Token')).toBe('fresh-token')
    }
    expect(JSON.parse(String((fetchMock.mock.calls[0]?.[1] as RequestInit).body))).toEqual({ content: JSON.stringify({ id: 'prompt-1', product: 'product', affiliation: 'lab', model_version: 'v1', prompt: 'text', code_exec: false, upload_file: false, multi_modal: false, web_search: false, sec_policies: true }, null, 2) })
  })

  it('Agent写请求透传AbortSignal且失败不自动重放或暴露原始message', async () => {
    const controller = new AbortController()
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ status: 0, message: 'saved' }))
      .mockResolvedValueOnce(jsonResponse({ status: 1, message: 'provider TOKEN_SENTINEL /tmp/private' }))
    vi.stubGlobal('fetch', fetchMock)

    await saveAgentConfig('openai', 'provider: openai\n', controller.signal)
    await expect(testAgentPrompt('provider: openai\n', 'hello', controller.signal)).rejects.not.toThrow(/TOKEN_SENTINEL|private/)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).signal).toBe(controller.signal)
  })

  it('Prompt成功输出按UTF-8字节拒绝超过256KiB的多字节文本', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ status: 0, message: '中'.repeat(90_000) })))

    await expect(testAgentPrompt('provider: safe\n', 'hello')).rejects.toMatchObject({
      name: 'ApiError',
      kind: 'unexpected-response',
    })
  })
})
