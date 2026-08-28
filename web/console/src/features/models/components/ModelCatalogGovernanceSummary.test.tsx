/**
 * 功能：验证模型目录治理态势的当前页计数、查询语义和敏感信息边界。
 * 实现：以已校验的安全模型目录 DTO 直接渲染纯展示组件，不依赖路由或网络。
 * 输入：当前页 ModelCatalogPage 与调用方提供的可配置判定谓词。
 * 输出：模型治理态势区域、空态和本页治理信号的回归断言。
 * 依赖：Vitest、Testing Library、模型安全 DTO 与 ModelCatalogGovernanceSummary。
 */
import { render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ModelCatalogItem, ModelCatalogPage } from '../api'
import {
  deriveModelCatalogGovernanceSignals,
  ModelCatalogGovernanceSummary,
} from './ModelCatalogGovernanceSummary'

const baseURLSentinel = 'https://base-url-sentinel.invalid/v1'

function modelItem(overrides: Partial<ModelCatalogItem> = {}): ModelCatalogItem {
  return {
    id: 'model-default',
    owner_user_id: 'user-1',
    scope: 'private',
    name: '模型目录项',
    provider_model: 'gpt-governed',
    base_url: baseURLSentinel,
    note: '仅用于治理摘要测试',
    limit: 4,
    disabled: false,
    source: 'platform',
    read_only: false,
    created_at: '2026-08-28T00:00:00Z',
    updated_at: '2026-08-28T01:00:00Z',
    ...overrides,
  }
}

const currentPageItems: ModelCatalogItem[] = [
  modelItem({ id: 'manageable-active', name: '可配置启用模型' }),
  modelItem({ id: 'manageable-overlap', name: '可配置停用只读模型', disabled: true, read_only: true }),
  modelItem({
    id: 'yaml-read-only',
    name: 'YAML 只读模型',
    owner_user_id: undefined,
    scope: 'global',
    source: 'yaml',
    disabled: true,
    read_only: true,
  }),
  modelItem({ id: 'role-limited-read-only', name: '角色受限只读模型', read_only: true }),
]

const populatedCatalog: ModelCatalogPage = {
  items: currentPageItems,
  total: 45,
  page: 2,
  page_size: 20,
}

const isManageable = (item: ModelCatalogItem) =>
  item.id === 'manageable-active' || item.id === 'manageable-overlap'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('ModelCatalogGovernanceSummary', () => {
  it('只以当前页项目和调用方可配置谓词派生可配置、停用与只读信号', () => {
    expect(deriveModelCatalogGovernanceSignals(populatedCatalog.items, isManageable)).toEqual({
      manageableModels: 2,
      disabledModels: 2,
      readOnlyModels: 3,
    })
  })

  it('展示具名当前查询与当前页治理信号，并且不读取或展示敏感连接信息', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)

    render(<ModelCatalogGovernanceSummary catalog={populatedCatalog} isManageable={isManageable} />)

    const summary = screen.getByRole('region', { name: '模型治理态势' })
    const query = within(summary).getByRole('group', { name: '当前查询' })
    const signals = within(summary).getByRole('group', { name: '本页治理信号' })

    expect(within(summary).getByRole('heading', { name: '模型治理态势' })).toBeInTheDocument()
    expect(query).toHaveTextContent('当前查询')
    expect(query).toHaveTextContent('匹配模型 45')
    expect(query).toHaveTextContent('服务器第 2 页')
    expect(signals).toHaveTextContent('可配置模型 2')
    expect(signals).toHaveTextContent('已停用 2')
    expect(signals).toHaveTextContent('只读项 3')
    expect(summary).not.toHaveTextContent(baseURLSentinel)
    expect(summary).not.toHaveTextContent('********')
    expect(summary).not.toHaveTextContent(/Token|token|连接健康|可用率/)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('没有可见模型时保留当前查询并只显示空目录文案', () => {
    const emptyCatalog: ModelCatalogPage = { items: [], total: 0, page: 1, page_size: 20 }

    render(<ModelCatalogGovernanceSummary catalog={emptyCatalog} isManageable={isManageable} />)

    const summary = screen.getByRole('region', { name: '模型治理态势' })
    const query = within(summary).getByRole('group', { name: '当前查询' })

    expect(query).toHaveTextContent('匹配模型 0')
    expect(query).toHaveTextContent('服务器第 1 页')
    expect(within(summary).getByText('暂无可见模型')).toBeInTheDocument()
    expect(within(summary).queryByRole('group', { name: '本页治理信号' })).not.toBeInTheDocument()
    expect(summary).not.toHaveTextContent(/可配置模型 0|已停用 0|只读项 0/)
  })

  it('匹配模型存在但当前页为空时不把服务器总数伪装成本页信号', () => {
    const emptyCurrentPage: ModelCatalogPage = { items: [], total: 45, page: 3, page_size: 20 }

    render(<ModelCatalogGovernanceSummary catalog={emptyCurrentPage} isManageable={isManageable} />)

    const summary = screen.getByRole('region', { name: '模型治理态势' })
    const query = within(summary).getByRole('group', { name: '当前查询' })

    expect(query).toHaveTextContent('匹配模型 45')
    expect(query).toHaveTextContent('服务器第 3 页')
    expect(within(summary).getByText('当前页没有模型')).toBeInTheDocument()
    expect(within(summary).queryByRole('group', { name: '本页治理信号' })).not.toBeInTheDocument()
    expect(summary).not.toHaveTextContent(/可配置模型 45|已停用 45|只读项 45/)
  })
})
