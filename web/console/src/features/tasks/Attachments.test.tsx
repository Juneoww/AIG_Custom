/**
 * 功能：锁定附件普通/分片上传、客户端预检、opaque ID 和角色化下载边界。
 * 实现：用真实 FormData、204 分片响应和受控二进制响应验证调用序列与零请求拒绝。
 * 输入：浏览器 File、当前角色和附件安全 DTO。
 * 输出：附件工作流及权限的回归断言。
 * 依赖：Vitest、共享 API 客户端与附件模块。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import { apiBinaryRequest, subscribeToUnauthorized } from '../../shared/api/client'
import {
  MAX_ATTACHMENT_BYTES,
  MAX_ATTACHMENTS_PER_TASK,
  downloadAttachment,
  preflightAttachments,
  uploadAttachment,
} from './attachments'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('附件上传', () => {
  it('大小或数量预检失败时不发网络请求', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const oversized = new File([new Uint8Array(1)], 'large.txt')
    Object.defineProperty(oversized, 'size', { value: MAX_ATTACHMENT_BYTES + 1 })

    expect(() => preflightAttachments([oversized])).toThrow('附件大小不能超过 50 MiB')
    expect(() =>
      preflightAttachments(Array.from({ length: MAX_ATTACHMENTS_PER_TASK + 1 }, (_, index) => new File(['x'], `${index}.txt`))),
    ).toThrow('单个任务最多选择 10 个附件')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('小文件使用普通 multipart 上传并只返回白名单字段', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(
        {
          id: 'attachment-opaque',
          filename: 'evidence.txt',
          size: 8,
          state: 'ready',
          created_at: '2026-08-18T01:00:00Z',
          storage_path: 'D:/secret/path',
        },
        201,
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    const result = await uploadAttachment(new File(['evidence'], 'evidence.txt'))

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe('http://localhost:3000/api/v1/platform/tasks/attachments')
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).body).toBeInstanceOf(FormData)
    expect(new Headers((fetchMock.mock.calls[0]?.[1] as RequestInit).headers).has('Content-Type')).toBe(false)
    expect(result).not.toHaveProperty('storage_path')
  })

  it('大文件依序初始化、上传分片并合并，opaque ID 不作为路径展开', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        jsonResponse(
          { id: 'opaque/id', filename: 'large.bin', size: 5_242_881, state: 'uploading', created_at: '2026-08-18T01:00:00Z' },
          201,
        ),
      )
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(
        jsonResponse({ id: 'opaque/id', filename: 'large.bin', size: 5_242_881, state: 'ready', created_at: '2026-08-18T01:00:00Z' }),
      )
    vi.stubGlobal('fetch', fetchMock)
    const file = new File([new Uint8Array(5_242_881)], 'large.bin')

    await uploadAttachment(file)

    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(fetchMock.mock.calls[1]?.[0]).toContain('/attachments/opaque%2Fid/chunks')
    expect(fetchMock.mock.calls[2]?.[0]).toContain('/attachments/opaque%2Fid/chunks')
    expect(fetchMock.mock.calls[3]?.[0]).toContain('/attachments/opaque%2Fid/merge')
  })
})

describe('附件下载授权投影', () => {
  it('审计员不显示调用能力且零请求', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    await expect(downloadAttachment('opaque-id', 'auditor')).rejects.toThrow('当前角色不能下载附件')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('只接受有界octet-stream并清理服务端文件名', async () => {
    let downloadedFilename = ''
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      downloadedFilename = this.download
    })
    const createObjectURL = vi.fn().mockReturnValue('blob:safe-download')
    const revokeObjectURL = vi.fn()
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revokeObjectURL })
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(new Uint8Array([1, 2, 3]), {
        headers: {
          'Content-Type': 'application/octet-stream',
          'Content-Length': '3',
          'Content-Disposition': "attachment; filename*=UTF-8''..%2Fsecret.txt",
        },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await downloadAttachment('opaque/id', 'user')

    expect(fetchMock.mock.calls[0]?.[0]).toContain('/attachments/opaque%2Fid/download')
    expect(click).toHaveBeenCalledTimes(1)
    expect(downloadedFilename).toBe('_secret.txt')
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:safe-download')
  })

  it('二进制401沿用会话失效通知，错误MIME会取消响应体', async () => {
    const listener = vi.fn()
    const unsubscribe = subscribeToUnauthorized(listener)
    const cancel = vi.fn()
    const unauthorized = new Response(new ReadableStream({ cancel }), { status: 401 })
    const wrongMime = new Response(new ReadableStream({ cancel }), { headers: { 'Content-Type': 'text/html' } })
    const fetchMock = vi.fn().mockResolvedValueOnce(unauthorized).mockResolvedValueOnce(wrongMime)
    vi.stubGlobal('fetch', fetchMock)

    await expect(apiBinaryRequest('/api/v1/platform/tasks/attachments/id/download')).rejects.toMatchObject({ kind: 'unauthenticated' })
    expect(listener).toHaveBeenCalledTimes(1)
    await expect(apiBinaryRequest('/api/v1/platform/tasks/attachments/id/download')).rejects.toMatchObject({ kind: 'unexpected-response' })
    expect(cancel).toHaveBeenCalledTimes(2)
    unsubscribe()
  })
})
