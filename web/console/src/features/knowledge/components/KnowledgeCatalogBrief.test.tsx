/**
 * 功能：验证知识库目录概览的安全范围、空态和角色治理边界。
 * 实现：以原始数字范围直接渲染纯展示组件，不依赖路由、查询缓存或网络请求。
 * 输入：分页或完整目录的安全显示范围、资源名称与可治理权限标记。
 * 输出：目录范围、空态、只读/可治理文案及 MCP 非分页语义的回归断言。
 * 依赖：Vitest、Testing Library 与 KnowledgeCatalogBrief。
 */
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import {
  KnowledgeCatalogBrief,
  type KnowledgeCatalogScope,
} from './KnowledgeCatalogBrief'

const rawContentSentinel = 'raw-content-must-never-reach-the-brief'
const tokenSentinel = 'token-must-never-reach-the-brief'

const paginatedScope: KnowledgeCatalogScope = {
  kind: 'paginated',
  total: 45,
  page: 2,
  visibleItems: 3,
}

describe('KnowledgeCatalogBrief', () => {
  it('展示分页目录的服务端范围、本页资产和可治理边界', () => {
    render(<KnowledgeCatalogBrief resourceLabel="指纹规则" scope={paginatedScope} canManage />)

    const brief = screen.getByRole('region', { name: '资产目录概览' })
    const scope = within(brief).getByRole('group', { name: '当前资源范围' })
    const boundary = within(brief).getByRole('group', { name: '治理边界' })

    expect(within(brief).getByRole('heading', { name: '资产目录概览' })).toBeInTheDocument()
    expect(scope).toHaveTextContent('匹配资源 45')
    expect(scope).toHaveTextContent('服务器第 2 页')
    expect(within(brief).getByText('本页资产 3')).toBeInTheDocument()
    expect(boundary).toHaveTextContent('可治理')
    expect(boundary).toHaveTextContent('管理员可创建、编辑和删除当前类别资产。')
    expect(brief).not.toHaveTextContent(/风险|健康|覆盖率|同步成功/)
    expect(brief).not.toHaveTextContent(rawContentSentinel)
    expect(brief).not.toHaveTextContent(tokenSentinel)
  })

  it('完整 MCP 目录只表达目录与当前显示范围，不泄露分页语义', () => {
    const completeScope: KnowledgeCatalogScope = {
      kind: 'complete',
      total: 3,
      visibleItems: 3,
    }

    render(<KnowledgeCatalogBrief resourceLabel="MCP 插件" scope={completeScope} canManage={false} />)

    const brief = screen.getByRole('region', { name: '资产目录概览' })
    const scope = within(brief).getByRole('group', { name: '当前资源范围' })
    const boundary = within(brief).getByRole('group', { name: '治理边界' })

    expect(scope).toHaveTextContent('当前目录 3 项')
    expect(within(brief).getByText('当前显示 3 项')).toBeInTheDocument()
    expect(boundary).toHaveTextContent('只读查看')
    expect(boundary).toHaveTextContent('当前角色可查看目录与原文，不可修改资产。')
    expect(brief).not.toHaveTextContent(/本页|服务器第|第\s*\d+\s*页|上一页|下一页|每页/)
  })

  it('总量为零时保留资源上下文并不渲染零值信号', () => {
    const emptyScope: KnowledgeCatalogScope = {
      kind: 'paginated',
      total: 0,
      page: 1,
      visibleItems: 0,
    }

    render(<KnowledgeCatalogBrief resourceLabel="指纹规则" scope={emptyScope} canManage />)

    const brief = screen.getByRole('region', { name: '资产目录概览' })
    expect(within(brief).getByRole('group', { name: '当前资源范围' })).toHaveTextContent('匹配资源 0')
    expect(within(brief).getByRole('status')).toHaveTextContent('暂无指纹规则')
    expect(within(brief).queryByText('本页资产 0')).not.toBeInTheDocument()
    expect(within(brief).queryByText('当前显示 0 项')).not.toBeInTheDocument()
  })

  it('分页目录总量非零但服务器页为空时不伪造零值本页资产', () => {
    const emptyServerPage: KnowledgeCatalogScope = {
      kind: 'paginated',
      total: 45,
      page: 3,
      visibleItems: 0,
    }

    render(<KnowledgeCatalogBrief resourceLabel="指纹规则" scope={emptyServerPage} canManage={false} />)

    const brief = screen.getByRole('region', { name: '资产目录概览' })
    const scope = within(brief).getByRole('group', { name: '当前资源范围' })

    expect(scope).toHaveTextContent('匹配资源 45')
    expect(scope).toHaveTextContent('服务器第 3 页')
    expect(within(brief).getByRole('status')).toHaveTextContent('当前页没有指纹规则')
    expect(within(brief).queryByText('本页资产 0')).not.toBeInTheDocument()
  })
})
