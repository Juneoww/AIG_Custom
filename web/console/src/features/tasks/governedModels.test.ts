/**
 * 功能：验证扫描任务可选受治理模型的纯目录规则。
 * 实现：覆盖可用性、展示白名单和分页终止条件。
 * 输入：已由模型 API 严格解析的目录项和分页数据。
 * 输出：可供任务选择器安全使用的模型项、标签和下一页编号。
 * 依赖：Vitest、模型目录安全 DTO。
 */
import { describe, expect, it } from 'vitest'

import type { ModelCatalogItem } from '../models/api'
import { fallbackModelLabel, modelOptionLabel, nextCatalogPage, selectableModels } from './governedModels'

function model(overrides: Partial<ModelCatalogItem> = {}): ModelCatalogItem {
  return {
    id: 'platform-model-1',
    owner_user_id: 'user-1',
    scope: 'private',
    name: '私有扫描模型',
    provider_model: 'gpt-secure',
    base_url: 'https://models.example.test/v1',
    note: '内部模型说明',
    limit: 4,
    disabled: false,
    source: 'platform',
    read_only: false,
    ...overrides,
  }
}

describe('governed model catalog helpers', () => {
  it('保留已启用的私有平台模型与只读 YAML 模型，但排除已停用模型', () => {
    const privateModel = model()
    const yamlModel = model({ id: 'yaml-model-1', name: 'YAML 全局模型', scope: 'global', source: 'yaml', read_only: true, owner_user_id: undefined })
    const disabledModel = model({ id: 'disabled-model-1', disabled: true })

    expect(selectableModels([privateModel, yamlModel, disabledModel])).toEqual([privateModel, yamlModel])
  })

  it('只用名称、provider model 与中文作用域生成标签', () => {
    const privateModel = model()
    const globalModel = model({ scope: 'global', name: '全局模型', provider_model: 'claude-secure' })

    expect(modelOptionLabel(privateModel)).toBe('私有扫描模型（gpt-secure，私有）')
    expect(modelOptionLabel(globalModel)).toBe('全局模型（claude-secure，全局）')
    expect(fallbackModelLabel('model-deleted-1')).toBe('已选择的模型（ID: model-deleted-1）')
  })

  it('标签不会泄露 base URL、token 或备注', () => {
    const sensitiveModel = model({
      name: '安全名称',
      provider_model: 'provider-id',
      base_url: 'https://base-url-secret.example.test/v1',
      note: 'note-secret',
    })
    const label = modelOptionLabel(sensitiveModel)

    expect(label).not.toContain('base-url-secret')
    expect(label).not.toContain('note-secret')
    expect(label).not.toContain('token-secret')
  })

  it.each([
    [{ page: 1, page_size: 100, total: 101 }, 2],
    [{ page: 2, page_size: 100, total: 200 }, undefined],
    [{ page: 1, page_size: 100, total: 100 }, undefined],
    [{ page: 0, page_size: 100, total: 200 }, undefined],
    [{ page: 1, page_size: 0, total: 200 }, undefined],
    [{ page: 1, page_size: 100, total: 0 }, undefined],
  ] as const)('仅在可继续推进的分页上返回下一页：%o', (page, expected) => {
    expect(nextCatalogPage(page)).toBe(expected)
  })
})
