/** 功能：验证 Skills 安全摘要的类型识别、固定静态模式与字段白名单。 */
import { describe, expect, it } from 'vitest'

import { ApiError } from '../../shared/api/errors'
import { parseTaskDetail } from './api'

const skillsTask = {
  id: 'skills-task-1', owner: 'alice', task_type: 'skills_scan', status: 'running',
  created_at: '2026-09-05T01:00:00Z', updated_at: '2026-09-05T01:01:00Z',
}

describe('Skills 任务安全摘要', () => {
  it('仅投影语言、模型和静态扫描模式', () => {
    const parsed = parseTaskDetail({
      ...skillsTask,
      input_summary: {
        language: 'zh', model_id: 'model-1', scan_mode: 'static',
        thread: 4, timeout: 60, target_count: 9, num_prompts: 7, port_scan_mode: 'full_tcp',
        skill_count: 2, filename: 'secret.zip', attachment_ids: ['attachment-secret'], token: 'token-secret',
      },
    })
    expect(parsed.task_type).toBe('skills_scan')
    expect(parsed.input_summary).toEqual({ language: 'zh', model_id: 'model-1', scan_mode: 'static' })
  })

  it('拒绝非静态扫描模式', () => {
    expect(() => parseTaskDetail({ ...skillsTask, input_summary: { scan_mode: 'dynamic' } })).toThrow(ApiError)
  })
})
