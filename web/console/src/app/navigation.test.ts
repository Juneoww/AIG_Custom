/**
 * 功能：验证角色导航矩阵的顺序、可见范围与唯一授权来源。
 * 实现：直接检查声明式导航元数据在三类角色下的投影。
 * 输入：管理员、审计员和普通用户角色。
 * 输出：精确且稳定的中文导航集合。
 * 依赖：Vitest 与导航元数据模块。
 */
import { describe, expect, it } from 'vitest'

import { navigationItems, secondaryNavigationFor, visibleNavigationFor } from './navigation'

const common = ['治理总览', '扫描任务', '安全报告', '凭证配置', '规则与知识库']

describe('role navigation', () => {
  it('keeps the approved route order and unique paths', () => {
    expect(navigationItems.map(({ path }) => path)).toEqual([
      '/',
      '/tasks',
      '/reports',
      '/models',
      '/knowledge',
      '/admin/users',
      '/admin/audit',
      '/admin/brand',
      '/system',
    ])
    expect(new Set(navigationItems.map(({ path }) => path)).size).toBe(navigationItems.length)
  })

  it('shows the exact approved items for each role', () => {
    expect(visibleNavigationFor('user').map(({ label }) => label)).toEqual(common)
    expect(visibleNavigationFor('auditor').map(({ label }) => label)).toEqual([
      ...common,
      '审计日志',
      '系统信息',
    ])
    expect(visibleNavigationFor('admin').map(({ label }) => label)).toEqual([
      ...common,
      '用户管理',
      '审计日志',
      '品牌设置',
      '系统信息',
    ])
  })

  it('exposes grouped scan navigation for regular users', () => {
    const scanChildren = [
      expect.objectContaining({ label: 'MCP 扫描', path: '/tasks/new?scan=mcp' }),
      expect.objectContaining({ label: 'Skills 扫描', path: '/tasks/skills' }),
      expect.objectContaining({ label: 'AI 基础设施扫描', path: '/tasks/ai-infra' }),
      expect.objectContaining({ label: 'Agent 工作流扫描', path: '/tasks/new?scan=agent-workflow' }),
    ]
    const credentialChildren = [
      expect.objectContaining({ label: '模型配置', path: '/models' }),
      expect.objectContaining({ label: '智能体配置', path: '/knowledge/agents' }),
    ]

    for (const role of ['user', 'auditor', 'admin'] as const) {
      expect(visibleNavigationFor(role).map((item) => item.label)).toContain('凭证配置')
      expect(secondaryNavigationFor('tasks', role)).toEqual(scanChildren)
      expect(secondaryNavigationFor('credentials', role)).toEqual(credentialChildren)
    }
  })
})
