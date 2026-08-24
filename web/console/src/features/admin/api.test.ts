/**
 * 功能：锁定管理页使用的用户、审计、品牌和系统 API 安全 DTO 合同。
 * 实现：以未知 JSON 输入验证白名单投影、分页约束、CSRF 写请求和敏感 metadata 丢弃。
 * 输入：模拟的同源 HTTP 响应、当前 CSRF Cookie 与管理操作参数。
 * 输出：安全领域 DTO 或不包含响应原文的固定错误。
 * 依赖：Vitest、共享 API 客户端与待实现的管理 API 适配层。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/errors'
import {
  createUser,
  fetchAuditEvents,
  fetchUsers,
  fetchSafeVersion,
  parseBrandConfig,
  parseSystemStatus,
  requestPasswordReset,
  triggerSystemSync,
} from './api'

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'aig_csrf=; Max-Age=0; Path=/'
})

describe('管理 API 安全 DTO', () => {
  it('分页读取用户时仅接受安全用户字段', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({
      items: [{
        id: 'user-1', username: 'alice', role: 'user', active: true, must_change_password: false,
        created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-02T00:00:00Z', password_hash: 'must-not-reach-ui',
      }], total: 1, page: 1, page_size: 20,
    })))

    await expect(fetchUsers({ page: 1, pageSize: 20 })).resolves.toEqual({
      items: [{
        id: 'user-1', username: 'alice', role: 'user', active: true, must_change_password: false,
        created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-02T00:00:00Z',
      }], total: 1, page: 1, page_size: 20,
    })
  })

  it('审计列表丢弃任意 metadata，避免把密钥或路径送入页面状态', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({
      items: [{
        id: 'event-1', action: 'model.updated', resource_type: 'model', resource_id: 'model-1', outcome: 'success',
        actor_user_id: 'admin', occurred_at: '2026-01-01T00:00:00Z', metadata: { token: 'must-not-reach-ui', path: 'C:/private' },
      }], total: 1, page: 1, page_size: 20,
    })))

    await expect(fetchAuditEvents({ page: 1, pageSize: 20 })).resolves.toEqual({
      items: [{
        id: 'event-1', action: 'model.updated', resource_type: 'model', resource_id: 'model-1', outcome: 'success',
        actor_user_id: 'admin', occurred_at: '2026-01-01T00:00:00Z',
      }], total: 1, page: 1, page_size: 20,
    })
  })

  it('创建用户在调用时读取 CSRF，且不接受意外的成功状态', async () => {
    document.cookie = 'aig_csrf=fresh-token; Path=/'
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({
      id: 'user-2', username: 'bob', role: 'auditor', active: true, must_change_password: true,
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    }, 201))
    vi.stubGlobal('fetch', fetchMock)

    await expect(createUser({ username: 'bob', password: 'temporary-password', role: 'auditor' })).resolves.toMatchObject({ id: 'user-2' })
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: 'POST' })
    expect(new Headers(fetchMock.mock.calls[0]?.[1]?.headers).get('X-CSRF-Token')).toBe('fresh-token')
  })

  it('站外密码重置与数据同步只执行单次写请求，且重置端点不接收 Token 响应', async () => {
    document.cookie = 'aig_csrf=fresh-token; Path=/'
    const resetFetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', resetFetch)
    await expect(requestPasswordReset('user-2')).resolves.toBeUndefined()
    expect(resetFetch).toHaveBeenCalledTimes(1)
    expect(String(resetFetch.mock.calls[0]?.[0])).toContain('/password-reset')
    expect(resetFetch.mock.calls[0]?.[1]).toMatchObject({ method: 'POST' })

    const syncFetch = vi.fn().mockResolvedValue(jsonResponse({ status: 0, data: { running: true, message: 'sync started', files_updated: 0, ref: 'main' } }))
    vi.stubGlobal('fetch', syncFetch)
    await expect(triggerSystemSync()).resolves.toMatchObject({ running: true, message: 'sync started' })
    expect(syncFetch).toHaveBeenCalledTimes(1)
    expect(syncFetch.mock.calls[0]?.[1]).toMatchObject({ method: 'POST' })
  })

  it('品牌与系统响应必须通过安全投影，格式错误时固定失败', () => {
    expect(parseBrandConfig({
      product_name: 'AI 安全治理平台', primary_color: '#1677ff', logo: 'aGVsbG8=', logo_mime: 'image/png',
      watermark: '内部', updated_by: 'admin', updated_at: '2026-01-01T00:00:00Z', ignored: '<script>',
    })).toEqual({
      product_name: 'AI 安全治理平台', primary_color: '#1677ff', logo: 'aGVsbG8=', logo_mime: 'image/png', watermark: '内部',
    })
    expect(parseSystemStatus({ status: 0, message: 'idle', data: { running: false, message: 'idle', files_updated: 0, raw_error: 'hidden' } })).toEqual({
      running: false, message: 'idle', files_updated: 0,
    })
    expect(parseSystemStatus({ status: 1, message: 'fatal: remote https://token@example.invalid', data: { running: false, success: false, message: 'fatal: remote https://token@example.invalid', files_updated: 0 } })).toEqual({
      running: false, success: false, message: '数据同步未完成，请查看受控服务日志。', files_updated: 0,
    })
    expect(() => parseBrandConfig({ product_name: 42 })).toThrow(ApiError)
  })

  it('关于页只接受本地安全版本 DTO，绝不调用远程更新检查端点', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ version: 'v1.2.3', commit: 'abc123', build_time: '2026-01-01T00:00:00Z', remote_url: 'must-not-reach-ui' }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchSafeVersion()).resolves.toEqual({ version: 'v1.2.3', commit: 'abc123', build_time: '2026-01-01T00:00:00Z' })
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('/api/v1/version')
    expect(String(fetchMock.mock.calls[0]?.[0])).not.toContain('/system/version')
  })
})
