/**
 * 功能：验证扫描任务可选受治理模型的纯目录规则。
 * 实现：覆盖可用性、展示白名单和分页终止条件。
 * 输入：已由模型 API 严格解析的目录项和分页数据。
 * 输出：可供任务选择器安全使用的模型项、标签和下一页编号。
 * 依赖：Vitest、模型目录安全 DTO。
 */
import { describe, expect, it } from 'vitest'

import type { ModelCatalogItem } from '../models/api'
import { catalogPageFingerprint, hasRepeatedCatalogPage, modelOptionLabel, nextCatalogPage, selectableModels } from './governedModels'

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

  it('按服务端顺序对同 ID 模型 canonical 去重后再筛停用项', () => {
    const platformModel = model({ id: 'shared-id', name: '平台模型', source: 'platform', disabled: false })
    const yamlCollision = model({ id: 'shared-id', name: 'YAML 同 ID 模型', source: 'yaml', scope: 'global', owner_user_id: undefined, read_only: true })
    const disabledPlatform = model({ id: 'disabled-shared-id', name: '停用平台模型', source: 'platform', disabled: true })
    const enabledYamlCollision = model({ id: 'disabled-shared-id', name: '不应绕过的 YAML 模型', source: 'yaml', scope: 'global', owner_user_id: undefined, read_only: true })

    expect(selectableModels([platformModel, yamlCollision, disabledPlatform, enabledYamlCollision])).toEqual([platformModel])
  })

  it('只用名称、provider model 与中文作用域生成标签', () => {
    const privateModel = model()
    const globalModel = model({ scope: 'global', name: '全局模型', provider_model: 'claude-secure' })

    expect(modelOptionLabel(privateModel)).toBe('私有扫描模型（gpt-secure，私有）')
    expect(modelOptionLabel(globalModel)).toBe('全局模型（claude-secure，全局）')
  })

  it('以白名单字段比较目录页内容，不受地址或备注变化影响', () => {
    const accepted = model({ base_url: 'https://first.example.test/v1', note: 'first-note' })
    const sameSafeModel = model({ base_url: 'https://second.example.test/v1', note: 'second-note' })
    const changedSafeModel = model({ name: '不同模型名' })

    expect(catalogPageFingerprint([accepted])).toBe(catalogPageFingerprint([sameSafeModel]))
    expect(catalogPageFingerprint([accepted])).not.toBe(catalogPageFingerprint([changedSafeModel]))
    expect(hasRepeatedCatalogPage([sameSafeModel], [[accepted]])).toBe(true)
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

  it('拒绝畸形、非推进、重复或超出目录页数上限的候选页', () => {
    expect(nextCatalogPage({ page: 1, page_size: 100, total: 201 }, [1, 2])).toBeUndefined()
    expect(nextCatalogPage({ page: 0, page_size: 100, total: 201 })).toBeUndefined()
    expect(nextCatalogPage({ page: 1_001, page_size: 100, total: 200_000 })).toBeUndefined()
    expect(nextCatalogPage({ page: 1, page_size: 101, total: 201 })).toBeUndefined()
    expect(nextCatalogPage({ page: 1, page_size: 100, total: -1 })).toBeUndefined()
    expect(nextCatalogPage({ page: 1_000, page_size: 100, total: 100_001 })).toBeUndefined()
  })
})
