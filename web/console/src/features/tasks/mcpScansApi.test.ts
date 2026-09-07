/** 功能：验证 MCP 专属任务与附件 API；输入：安全字段/冲突响应；输出：专属端点与白名单断言。 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMCPScanSubmission, parseMCPScanDetail, uploadMCPAttachment, validateMCPScanInput } from './mcpScansApi'
afterEach(() => vi.unstubAllGlobals())
describe('MCP scan boundary', () => {
  it('requires exclusive repository sources and explicit service authorization and version', () => {
    expect(() => validateMCPScanInput({ source_kind: 'repository', repository_url: 'https://example.test/repo', attachment_ids: ['a'] })).toThrow()
    expect(() => validateMCPScanInput({ source_kind: 'repository', repository_url: 'http://example.test/repo' })).toThrow()
    expect(() => validateMCPScanInput({ source_kind: 'service', connection_config_id: 'c', connection_config_version: 1, authorization_confirmed: false })).toThrow()
    expect(() => validateMCPScanInput({ source_kind: 'repository', repository_url: 'https://example.test/repo', thread: 33 })).toThrow()
    expect(() => validateMCPScanInput({ source_kind: 'repository', repository_url: 'https://example.test/repo', task_type: 'mcp_scan' })).toThrow()
  })
  it('uses only the dedicated endpoint and reuses the same body and key after unknown outcome', async () => {
    const fetcher = vi.fn().mockRejectedValueOnce(new TypeError()).mockResolvedValueOnce(Response.json({ task_id: 'mcp-1', status: 'pending', secret: 'hidden' }))
    vi.stubGlobal('fetch', fetcher)
    const operation = createMCPScanSubmission({ source_kind: 'service', connection_config_id: 'conn-1', connection_config_version: 2, authorization_confirmed: true, thread: 4 })
    await expect(operation.submit()).rejects.toThrow()
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(await operation.submit()).toEqual({ task_id: 'mcp-1', status: 'pending' })
    expect(String(fetcher.mock.calls[0][0])).toContain('/platform/mcp-scans')
    expect(fetcher.mock.calls[0][1].body).toBe(fetcher.mock.calls[1][1].body)
    expect(new Headers(fetcher.mock.calls[0][1].headers).get('Idempotency-Key')).toBe(new Headers(fetcher.mock.calls[1][1].headers).get('Idempotency-Key'))
  })
  it('drops raw task parameters and rejects malformed input summaries', () => {
    const safe = { id: 'mcp-1', owner: 'u-1', source_kind: 'service', status: 'running', created_at: '2026-09-02T00:00:00Z', updated_at: '2026-09-02T00:00:00Z', input_summary: { language: 'zh_CN', source_kind: 'service', thread: 4 } }
    expect(parseMCPScanDetail({ ...safe, content: 'SECRET', params: { token: 'SECRET' }, input_summary: { ...safe.input_summary, endpoint: 'SECRET' } })).toEqual(safe)
    expect(() => parseMCPScanDetail({ ...safe, input_summary: { language: 'en', source_kind: 'service' } })).toThrow()
  })
  it('uploads only to the dedicated attachment endpoint and discards returned filenames', async () => {
    const fetcher = vi.fn().mockResolvedValue(Response.json({ id: 'a-1', state: 'ready', size: 4, max_file_bytes: 52428800, max_chunk_bytes: 5242880, filename: 'SECRET' }))
    vi.stubGlobal('fetch', fetcher)
    expect(await uploadMCPAttachment(new File(['code'], 'repo.zip'))).not.toHaveProperty('filename')
    expect(String(fetcher.mock.calls[0][0])).toContain('/platform/mcp-scan-attachments')
  })
  it('sends an idempotency key when merging a dedicated chunk upload', async () => {
    const bytes = 6 * 1024 * 1024
    const fetcher = vi.fn(async (url: string, init: RequestInit) => {
      const safe = { id: 'chunked-1', state: 'uploading', size: bytes, max_file_bytes: 52428800, max_chunk_bytes: 5242880 }
      if (url.endsWith('/chunked')) return Response.json(safe)
      if (url.endsWith('/merge')) return new Headers(init.headers).get('Idempotency-Key') ? Response.json({ ...safe, state: 'ready' }) : new Response(null, { status: 400 })
      return new Response(null, { status: 204 })
    })
    vi.stubGlobal('fetch', fetcher)
    expect((await uploadMCPAttachment(new File([new Uint8Array(bytes)], 'repo.zip'))).state).toBe('ready')
    expect(fetcher.mock.calls.filter(([url]) => url.endsWith('/chunks'))).toHaveLength(2)
    expect(fetcher.mock.calls.every(([url]) => url.includes('/platform/mcp-scan-attachments'))).toBe(true)
  })
  it('accepts a display owner name without interpreting it as an opaque ID', () => {
    expect(parseMCPScanDetail({ id: 'scan-1', owner: '安全操作员', source_kind: 'repository', status: 'pending', created_at: '2026-09-02T00:00:00Z', updated_at: '2026-09-02T00:00:00Z', input_summary: { language: 'zh_CN', source_kind: 'repository' } }).owner).toBe('安全操作员')
  })
})
