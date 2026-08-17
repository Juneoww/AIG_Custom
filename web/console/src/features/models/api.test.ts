/**
 * 功能：验证受治理模型 API 的安全投影、凭据边界与精确状态。
 * 实现：使用真实 Response 和 fetch 替身观测同源请求，不记录请求体或原始响应。
 * 输入：未知服务端 JSON、分页参数与模型写入。
 * 输出：严格白名单 DTO、单次写请求与固定安全错误。
 * 依赖：Vitest、共享 API 客户端与模型领域适配器。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../shared/api/errors'
import {
  createModel,
  deleteModel,
  fetchModelCatalog,
  parseModelCatalog,
  rotateModelEncryption,
  updateModel,
} from './api'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function catalogItem(overrides: Record<string, unknown> = {}) {
  return {
    id: 'model-shared',
    owner_user_id: 'user-1',
    scope: 'private',
    name: '生产模型',
    provider_model: 'gpt-secure',
    base_url: 'https://models.example.invalid/v1',
    note: '仅用于治理任务',
    limit: 4,
    disabled: false,
    token: '********',
    source: 'platform',
    read_only: false,
    created_at: '2026-08-18T01:00:00Z',
    updated_at: '2026-08-18T02:00:00Z',
    ...overrides,
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('parseModelCatalog', () => {
  it('只返回白名单字段，不让Token或未知敏感字段进入前端DTO', () => {
    const secret = 'sentinel-plaintext-secret'
    const page = parseModelCatalog({
      items: [catalogItem({ encrypted_token: secret, raw_config: { token: secret } })],
      total: 1,
      page: 1,
      page_size: 20,
      internal_path: secret,
    })

    expect(page.items[0]).toEqual({
      id: 'model-shared',
      owner_user_id: 'user-1',
      scope: 'private',
      name: '生产模型',
      provider_model: 'gpt-secure',
      base_url: 'https://models.example.invalid/v1',
      note: '仅用于治理任务',
      limit: 4,
      disabled: false,
      source: 'platform',
      read_only: false,
      created_at: '2026-08-18T01:00:00Z',
      updated_at: '2026-08-18T02:00:00Z',
    })
    expect(JSON.stringify(page)).not.toContain(secret)
    expect(page.items[0]).not.toHaveProperty('token')
  })

  it('保留同ID的platform与YAML两行，不让只读来源遮蔽数据库来源', () => {
    const page = parseModelCatalog({
      items: [
        catalogItem(),
        catalogItem({ owner_user_id: '', scope: 'global', source: 'yaml', read_only: true }),
      ],
      total: 2,
      page: 1,
      page_size: 20,
    })

    expect(page.items.map(({ id, source, read_only }) => ({ id, source, read_only }))).toEqual([
      { id: 'model-shared', source: 'platform', read_only: false },
      { id: 'model-shared', source: 'yaml', read_only: true },
    ])
  })

  it.each([
    catalogItem({ token: 'plaintext' }),
    catalogItem({ source: 'file' }),
    catalogItem({ read_only: 'false' }),
    catalogItem({ source: 'yaml', read_only: false }),
    catalogItem({ source: 'yaml', read_only: true, scope: 'private', owner_user_id: '' }),
    catalogItem({ source: 'yaml', read_only: true, scope: 'global', owner_user_id: 'user-1' }),
    catalogItem({ id: '..' }),
  ])('对非合同响应失败关闭', (item) => {
    expect(() => parseModelCatalog({ items: [item], total: 1, page: 1, page_size: 20 }))
      .toThrowError(new ApiError('unexpected-response', 200))
  })
})

describe('模型API请求', () => {
  it('使用服务端分页并传递AbortSignal', async () => {
    const signal = new AbortController().signal
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ items: [], total: 0, page: 2, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(fetchModelCatalog({ page: 2, pageSize: 20 }, signal)).resolves.toMatchObject({ page: 2 })
    expect(fetchMock).toHaveBeenCalledWith(
      'http://localhost:3000/api/v1/platform/models?page=2&page_size=20',
      expect.objectContaining({ signal }),
    )
  })

  it('创建精确接受201，失败时不自动重放', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 500 }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(createModel({
      name: '私有模型', provider_model: 'gpt-secure', base_url: 'https://models.invalid/v1',
      token: 'create-only-secret', scope: 'private', note: '', limit: 2,
    })).rejects.toMatchObject({ kind: 'server' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('不接受掩码作为创建Token，且不发网络请求', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    await expect(createModel({
      name: '私有模型', provider_model: 'gpt-secure', base_url: 'https://models.invalid/v1',
      token: '********', scope: 'private', note: '', limit: 2,
    })).rejects.toMatchObject({ kind: 'bad-request' })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('更新未修改Token时省略token，即使调用方传入掩码也绝不写回', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(catalogItem({ source: undefined, read_only: undefined }), 200))
    vi.stubGlobal('fetch', fetchMock)

    await updateModel('model-shared', { name: '更新名称', token: '********' })
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit
    expect(JSON.parse(String(init.body))).toEqual({ name: '更新名称' })
    expect(String(init.body)).not.toContain('********')
  })

  it.each([
    ['delete', () => deleteModel('model-shared'), 'DELETE', '/api/v1/platform/models/model-shared'],
    ['rotate', () => rotateModelEncryption('model-shared'), 'POST', '/api/v1/platform/models/model-shared/rotate-encryption'],
  ] as const)('%s只接受精确204且无请求体', async (_name, operation, method, path) => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await expect(operation()).resolves.toBeUndefined()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`http://localhost:3000${path}`)
    expect(fetchMock.mock.calls[0]?.[1]).toEqual(expect.objectContaining({ method }))
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).body).toBeUndefined()
  })

  it('删除和轮换遇到200均按非合同响应拒绝', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({}, 200))
      .mockResolvedValueOnce(jsonResponse({}, 200))
    vi.stubGlobal('fetch', fetchMock)

    await expect(deleteModel('model-shared')).rejects.toMatchObject({ kind: 'unexpected-response', status: 200 })
    await expect(rotateModelEncryption('model-shared')).rejects.toMatchObject({ kind: 'unexpected-response', status: 200 })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
