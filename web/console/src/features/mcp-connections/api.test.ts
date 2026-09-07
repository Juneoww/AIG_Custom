/** 功能：验证 MCP 连接只读投影及写入边界；输入：安全/污染 DTO；输出：合同断言。 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createConnectionMutation, mcpErrorMessage, parseConnectionDetail, parseConnectionList, parseConnectionOptions } from './api'

const summary = { id: 'conn-1', name: '测试连接', description: '', scope: 'private', current_version: 1, resource_revision: '2', enabled: false, transport: 'auto', probe_status: 'not_tested', authentication_kind: 'bearer', created_at: '2026-09-02T00:00:00Z', updated_at: '2026-09-02T00:00:00Z' }
afterEach(() => vi.unstubAllGlobals())
describe('MCP connection boundary', () => {
  it('projects only safe summary and never copies secret extras', () => {
    expect(parseConnectionList({ items: [{ ...summary, secret: 'DO-NOT-CACHE', server_url: 'https://private.test' }] })).toEqual([summary])
    expect(() => parseConnectionList({ items: [{ ...summary, resource_revision: -1 }] })).toThrow()
  })
  it('projects configured flags without header values', () => {
    const detail = parseConnectionDetail({ ...summary, server_url: 'https://example.test/mcp', authentication_configured: true, headers: [{ name: 'X-Key', configured: true, value: 'SECRET' }], secret: 'SECRET' })
    expect(detail.headers).toEqual([{ name: 'X-Key', configured: true }])
    expect(JSON.stringify(detail)).not.toContain('SECRET')
    expect(parseConnectionOptions({ items: [{ connection_id: 'conn-1', connection_version: 1, name: '测试连接', scope: 'private', transport: 'http', authentication_kind: 'bearer', server_url: 'SECRET' }] })[0]).not.toHaveProperty('server_url')
  })
  it('uses one mutation key for an explicit network retry and applies If-Match and CSRF', async () => {
    document.cookie = 'aig_csrf=csrf-test; path=/'
    const fetcher = vi.fn().mockRejectedValueOnce(new TypeError('SECRET')).mockResolvedValueOnce(Response.json({ id: 'conn-1', current_version: 2, resource_revision: '3', status: 'updated', secret: 'SECRET' }))
    vi.stubGlobal('fetch', fetcher)
    const operation = createConnectionMutation('PATCH', 'conn-1', { authentication: { kind: 'bearer', secret: 'new-secret' } }, '2')
    await expect(operation.submit()).rejects.toThrow('无法连接')
    expect(fetcher).toHaveBeenCalledTimes(1)
    expect(await operation.submit()).toEqual({ id: 'conn-1', current_version: 2, resource_revision: '3', status: 'updated' })
    const first = fetcher.mock.calls[0][1] as RequestInit
    const second = fetcher.mock.calls[1][1] as RequestInit
    expect(first.body).toBe(second.body)
    expect(new Headers(first.headers).get('Idempotency-Key')).toBe(new Headers(second.headers).get('Idempotency-Key'))
    expect(new Headers(first.headers).get('If-Match')).toBe('"2"')
    expect(new Headers(first.headers).get('X-CSRF-Token')).toBe('csrf-test')
    operation.clear()
    await expect(operation.submit()).rejects.toThrow()
  })
  it.each(['body', 'json', 'dto'] as const)('marks a successful mutation with a broken %s as unknown without retaining raw response details', async (fault) => {
    const response = fault === 'body' ? new Response(new ReadableStream({ start(controller) { controller.error(new Error('SECRET')) } }), { status: 202, headers: { 'Content-Type': 'application/json' } }) : fault === 'json' ? new Response('{SECRET', { status: 201, headers: { 'Content-Type': 'application/json' } }) : Response.json({ ...summary, status: 'SECRET' })
    const fetcher = vi.fn().mockResolvedValueOnce(response).mockResolvedValueOnce(Response.json({ id: 'conn-1', current_version: 1, resource_revision: '2', status: 'created' }))
    vi.stubGlobal('fetch', fetcher)
    const operation = createConnectionMutation('POST', undefined, { name: '测试连接' })
    const error = await operation.submit().catch((caught: unknown) => caught)
    expect(mcpErrorMessage(error)).toContain('操作结果尚未确认')
    expect(String(error)).not.toContain('SECRET')
    expect(JSON.stringify(error)).not.toContain('SECRET')
    expect(fetcher).toHaveBeenCalledTimes(1)
    await expect(operation.submit()).resolves.toEqual({ id: 'conn-1', current_version: 1, resource_revision: '2', status: 'created' })
    const first = fetcher.mock.calls[0][1] as RequestInit
    const second = fetcher.mock.calls[1][1] as RequestInit
    expect(first.body).toBe(second.body)
    expect(new Headers(first.headers).get('Idempotency-Key')).toBe(new Headers(second.headers).get('Idempotency-Key'))
  })
  it.each([500, 502, 504])('retains a possibly committed operation after HTTP %s for explicit same-key retry', async (status) => {
    const fetcher = vi.fn().mockResolvedValueOnce(Response.json({ error: 'SECRET' }, { status })).mockResolvedValueOnce(Response.json({ id: 'conn-1', current_version: 1, resource_revision: '2', status: 'created' }))
    vi.stubGlobal('fetch', fetcher)
    const operation = createConnectionMutation('POST', undefined, { name: '测试连接' })
    const error = await operation.submit().catch((caught: unknown) => caught)
    expect(mcpErrorMessage(error)).toContain('操作结果尚未确认')
    expect(String(error)).not.toContain('SECRET')
    expect(fetcher).toHaveBeenCalledTimes(1)
    await operation.submit()
    const first = fetcher.mock.calls[0][1] as RequestInit
    const second = fetcher.mock.calls[1][1] as RequestInit
    expect(first.body).toBe(second.body)
    expect(new Headers(first.headers).get('Idempotency-Key')).toBe(new Headers(second.headers).get('Idempotency-Key'))
  })
  it('does not label local validation failures or explicit 4xx rejection as an unknown committed operation', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce(Response.json({ error: 'SECRET' }, { status: 409 }))
    vi.stubGlobal('fetch', fetcher)
    const invalid = createConnectionMutation('PATCH', 'conn-1', {}, 'invalid')
    const localError = await invalid.submit().catch((caught: unknown) => caught)
    expect(mcpErrorMessage(localError)).not.toContain('操作结果尚未确认')
    expect(fetcher).not.toHaveBeenCalled()
    const conflict = createConnectionMutation('PATCH', 'conn-1', {}, '2')
    const conflictError = await conflict.submit().catch((caught: unknown) => caught)
    expect(mcpErrorMessage(conflictError)).toContain('配置版本或提交状态已变化')
  })
})
