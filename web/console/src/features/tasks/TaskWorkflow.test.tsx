/**
 * 功能：锁定任务分页筛选、幂等提交、短轮询与不确定取消的真实浏览器合同。
 * 实现：以受控 fetch 响应验证请求路径、调用次数、固定安全 DTO 和终态边界。
 * 输入：任务列表、详情、创建及取消的后端响应夹具。
 * 输出：Task12 任务主流程的安全回归断言。
 * 依赖：Vitest、Testing Library 与任务 API 模块。
 */
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError, NetworkError } from '../../shared/api/errors'
import {
  cancelTaskGoverned,
  createTaskSubmission,
  fetchTaskList,
  parseTaskDetail,
  taskPollDelay,
  type TaskCreateRequest,
} from './api'

const runningTask = {
  id: 'task-opaque-1',
  owner: 'alice',
  task_type: 'mcp_scan',
  status: 'running',
  created_at: '2026-08-18T01:00:00Z',
  updated_at: '2026-08-18T01:01:00Z',
  input_summary: { language: 'zh', thread: 4 },
} as const

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('任务服务端列表合同', () => {
  it('只发送服务端支持的分页和精确筛选，并校验安全摘要', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        items: [{ ...runningTask, remark: '列表不得透出备注' }],
        total: 1,
        page: 2,
        page_size: 20,
        raw_result: '不得渲染',
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const result = await fetchTaskList({ page: 2, pageSize: 20, status: 'running', taskType: 'mcp_scan' })

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(
      'http://localhost:3000/api/v1/platform/tasks?page=2&page_size=20&status=running&task_type=mcp_scan',
    )
    expect(result).toEqual({ items: [expect.objectContaining({ id: 'task-opaque-1' })], total: 1, page: 2, page_size: 20 })
    expect(JSON.stringify(result)).not.toContain('raw_result')
    expect(JSON.stringify(result)).not.toContain('列表不得透出备注')
  })

  it('详情拒绝未知的端口扫描模式，不将其传给页面', () => {
    expect(() => parseTaskDetail({
      ...runningTask,
      input_summary: { port_scan_mode: 'not-approved' },
    })).toThrow(ApiError)
  })

  it('MCP 详情只保留来源类别，不接受端点或授权材料', () => {
    const detail = parseTaskDetail({
      ...runningTask,
      input_summary: {
        source_kind: 'service',
        endpoint: 'https://private.example/mcp',
        authorization_confirmed: true,
      },
    })

    expect(detail.input_summary).toEqual({ source_kind: 'service' })
    expect(JSON.stringify(detail)).not.toContain('private.example')
    expect(JSON.stringify(detail)).not.toContain('authorization_confirmed')
  })
})

describe('任务详情模型摘要白名单', () => {
  it('AI 基础设施任务只投影安全 model_id，不向页面传递敏感未知字段', () => {
    const result = parseTaskDetail({
      ...runningTask,
      task_type: 'ai_infra_scan',
      input_summary: {
        model_id: 'model-opaque-1',
        token: 'must-not-reach-page',
        base_url: 'https://internal.invalid',
      },
    })

    expect(result.input_summary).toEqual({ model_id: 'model-opaque-1' })
    expect(JSON.stringify(result)).not.toContain('token')
    expect(JSON.stringify(result)).not.toContain('base_url')
  })

  it.each([
    ['a', 'one character'],
    ['a'.repeat(128), '128 characters'],
    ['model.name', 'dot separator'],
    ['model_name', 'underscore separator'],
    ['model:name', 'colon separator'],
    ['model-name', 'hyphen separator'],
  ])('accepts a valid %s model ID', (modelID) => {
    const result = parseTaskDetail({
      ...runningTask,
      task_type: 'ai_infra_scan',
      input_summary: { model_id: modelID },
    })

    expect(result.input_summary).toEqual({ model_id: modelID })
  })

  it('projects model_id together with other approved AI infrastructure summary fields', () => {
    const result = parseTaskDetail({
      ...runningTask,
      task_type: 'ai_infra_scan',
      input_summary: {
        model_id: 'model-opaque-1',
        language: 'zh',
        timeout: 60,
        port_scan_mode: 'fixed_ai',
      },
    })

    expect(result.input_summary).toEqual({
      model_id: 'model-opaque-1',
      language: 'zh',
      timeout: 60,
      port_scan_mode: 'fixed_ai',
    })
  })

  it.each([
    ['', 'empty'],
    [' ', 'space'],
    ['a'.repeat(129), 'too long'],
    ['model/unsafe', 'forbidden character'],
    ['.', 'single dot'],
    ['..', 'double dot'],
  ])('rejects a %s model ID in task detail', (modelID) => {
    expect(() => parseTaskDetail({
      ...runningTask,
      task_type: 'ai_infra_scan',
      input_summary: { model_id: modelID },
    })).toThrow(ApiError)
  })

  it('rejects a non-string model ID in task detail', () => {
    expect(() => parseTaskDetail({
      ...runningTask,
      task_type: 'ai_infra_scan',
      input_summary: { model_id: 123 },
    })).toThrow(ApiError)
  })
})

describe('任务详情备注白名单', () => {
  it('保留去除外围空白后的中文与 emoji，并按 Unicode 码点接受 2000 个字符', () => {
    const boundaryRemark = `${'安'.repeat(1_999)}😀`

    const result = parseTaskDetail({
      ...runningTask,
      task_type: 'ai_infra_scan',
      remark: ` \n${boundaryRemark}\t `,
      input_summary: {},
      internal_note: '不得传给页面',
    })

    expect(result.remark).toBe(boundaryRemark)
    expect(Object.keys(result)).toEqual([
      'id', 'owner', 'task_type', 'status', 'created_at', 'updated_at', 'input_summary', 'remark',
    ])
    expect(JSON.stringify(result)).not.toContain('internal_note')
  })

  it.each([
    ['数字', 123],
    ['全空白文本', ' \n\t '],
    ['超过 2000 个 Unicode 码点', `注${'😀'.repeat(2_000)}`],
    ['未配对代理项', '\uD800'],
  ])('拒绝%s备注', (_caseName, remark) => {
    expect(() => parseTaskDetail({
      ...runningTask,
      task_type: 'ai_infra_scan',
      remark,
      input_summary: {},
    })).toThrow(ApiError)
  })

  it('备注缺省时不增加详情字段', () => {
    const result = parseTaskDetail({ ...runningTask, input_summary: {} })

    expect(result).not.toHaveProperty('remark')
  })
})

describe('任务写入和短轮询边界', () => {
  it('创建请求原样发送可选备注，且不混入扫描内容或参数', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(runningTask, 202))
    vi.stubGlobal('fetch', fetchMock)
    const input: TaskCreateRequest = {
      task_type: 'ai_infra_scan',
      content: '192.0.2.10',
      params: { model_id: 'model-opaque-1' },
      remark: '  本次为变更窗口前扫描  ',
    }

    await createTaskSubmission(input, 'task-with-remark').submit()

    const sent = JSON.parse(String((fetchMock.mock.calls[0]?.[1] as RequestInit).body)) as Record<string, unknown>
    expect(sent.remark).toBe('  本次为变更窗口前扫描  ')
    expect(sent.content).toBe('192.0.2.10')
    expect(sent.params).toEqual({ model_id: 'model-opaque-1' })
  })

  it('同一次逻辑提交显式重试复用幂等键且不会自动重放', async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('network'))
      .mockResolvedValueOnce(jsonResponse(runningTask, 202))
    vi.stubGlobal('fetch', fetchMock)
    const input: TaskCreateRequest = { task_type: 'mcp_scan', content: 'https://target.invalid', params: {} }
    const submission = createTaskSubmission(input, 'task-submit-fixed-key')

    await expect(submission.submit()).rejects.toBeInstanceOf(NetworkError)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await expect(submission.submit()).resolves.toEqual(expect.objectContaining({ id: 'task-opaque-1' }))

    const keys = fetchMock.mock.calls.map(([, init]) => new Headers((init as RequestInit).headers).get('Idempotency-Key'))
    expect(keys).toEqual(['task-submit-fixed-key', 'task-submit-fixed-key'])
  })

  it('短轮询对终态停止，并以有界退避在最大次数后停止', () => {
    expect(taskPollDelay(runningTask, 0)).toBe(2_000)
    expect(taskPollDelay(runningTask, 2)).toBe(4_000)
    expect(taskPollDelay(runningTask, 7)).toBe(8_000)
    expect(taskPollDelay(runningTask, 8)).toBe(false)
    expect(taskPollDelay({ ...runningTask, status: 'succeeded' }, 0)).toBe(false)
  })

  it('取消遇到网络不确定只读取一次详情，不自动再次写入', async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('network'))
      .mockResolvedValueOnce(jsonResponse(runningTask))
    vi.stubGlobal('fetch', fetchMock)

    const result = await cancelTaskGoverned('task-opaque-1')

    expect(result).toEqual({ status: 'uncertain', task: runningTask })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).method).toBe('POST')
    expect((fetchMock.mock.calls[1]?.[1] as RequestInit).method).toBe('GET')
  })

  it.each([
    ['服务端错误', new Response(null, { status: 503 })],
    ['成功响应解析异常', new Response('{', { status: 200, headers: { 'Content-Type': 'application/json' } })],
    ['非契约成功状态', jsonResponse({ status: 'accepted-but-not-cancelled' })],
  ])('取消遇到%s时只读取一次详情确认', async (_name, failedResponse) => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(failedResponse)
      .mockResolvedValueOnce(jsonResponse(runningTask))
    vi.stubGlobal('fetch', fetchMock)

    await expect(cancelTaskGoverned('task-opaque-1')).resolves.toEqual({ status: 'uncertain', task: runningTask })

    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock.mock.calls.map(([, init]) => (init as RequestInit).method)).toEqual(['POST', 'GET'])
  })

  it('取消遇到明确4xx时直接抛出且绝不确认或重写', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 403 }))
    vi.stubGlobal('fetch', fetchMock)

    const error = await cancelTaskGoverned('task-opaque-1').catch((caught: unknown) => caught)
    expect(error).toBeInstanceOf(ApiError)
    expect((error as ApiError).kind).toBe('forbidden')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect((fetchMock.mock.calls[0]?.[1] as RequestInit).method).toBe('POST')
  })
})
