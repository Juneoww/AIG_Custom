/**
 * 功能：验证角色侧栏的分组导航、当前页语义、品牌和可访问折叠行为。
 * 实现：在真实路由与主题上下文中渲染侧栏，模拟分组展开和路由切换。
 * 输入：普通用户角色、分组路由路径与配置品牌。
 * 输出：精确的分组导航集合、活动子项和折叠后的可访问链接。
 * 依赖：Testing Library、MemoryRouter、主题与公共品牌 Provider。
 */
import { fireEvent, render, screen, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter, useNavigate } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { PublicBrandProvider } from '../../shared/brand/PublicBrandProvider'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { createAppQueryClient } from '../providers/AppProviders'
import { Sidebar } from './Sidebar'

function RouteDriver() {
  const navigate = useNavigate()
  return (
    <div>
      <button onClick={() => navigate('/tasks')}>前往任务列表</button>
      <button onClick={() => navigate('/tasks/new')}>前往新建任务</button>
      <button onClick={() => navigate('/tasks/mcp')}>前往 MCP 扫描</button>
      <button onClick={() => navigate('/tasks/agent-workflow')}>前往 Agent 扫描</button>
      <button onClick={() => navigate('/tasks/ai-infra?status=running')}>前往 AI 基础设施运行中</button>
      <button onClick={() => navigate('/tasks/ai-infra?status=failed&page=2')}>前往 AI 基础设施失败</button>
      <button onClick={() => navigate('/tasks/ai-infra/new')}>前往新建 AI 基础设施任务</button>
      <button onClick={() => navigate('/models')}>前往模型配置</button>
    </div>
  )
}

function renderSidebar(initialEntry = '/reports', withRouteDriver = false) {
  return render(
    <ThemeProvider initialMode="light">
      <QueryClientProvider client={createAppQueryClient()}>
        <MemoryRouter initialEntries={[initialEntry]}>
          <PublicBrandProvider
            initialConfig={{ product_name: '超长的企业人工智能安全治理平台名称', primary_color: '#005a9e', logo_data_url: '' }}
          >
            <Sidebar role="user" />
            {withRouteDriver ? <RouteDriver /> : null}
          </PublicBrandProvider>
        </MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  )
}

describe('Sidebar', () => {
  it.each(['/tasks/skills', '/tasks/skills/new', '/tasks/skills/task-opaque-1'])('在%s保持 Skills 子导航活动状态', (path) => {
    renderSidebar(path)
    expect(screen.getByRole('link', { name: 'Skills 扫描' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'AI 基础设施扫描' })).not.toHaveAttribute('aria-current')
  })

  it('renders the user navigation in order and marks the active link', () => {
    renderSidebar()

    expect(screen.getAllByRole('link').map((link) => link.textContent?.trim())).toEqual([
      '治理总览',
      '扫描任务',
      '安全报告',
      '凭证配置',
      '规则与知识库',
    ])
    expect(screen.getByRole('link', { name: '安全报告' })).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByRole('link', { name: '用户管理' })).not.toBeInTheDocument()
  })

  it('keeps full accessible names and product title after collapsing', () => {
    renderSidebar()

    const product = screen.getByLabelText('超长的企业人工智能安全治理平台名称')
    expect(product).toHaveAttribute('title', '超长的企业人工智能安全治理平台名称')
    fireEvent.click(screen.getByRole('button', { name: '收起导航' }))

    expect(screen.getByRole('button', { name: '展开导航' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '治理总览' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '安全报告' })).toHaveAttribute('aria-current', 'page')
  })

  it('expands scan tasks independently with the four classified entries', () => {
    renderSidebar()

    fireEvent.click(screen.getByRole('button', { name: '展开扫描任务子菜单' }))

    expect(screen.getByRole('link', { name: 'MCP 安全扫描' })).toHaveAttribute('href', '/tasks/mcp')
    expect(screen.getByRole('link', { name: 'Skills 扫描' })).toHaveAttribute('href', '/tasks/skills')
    expect(screen.getByRole('link', { name: 'AI 基础设施扫描' })).toHaveAttribute(
      'href',
      '/tasks/ai-infra',
    )
    expect(screen.getByRole('link', { name: 'Agent 工作流扫描' })).toHaveAttribute(
      'href',
      '/tasks/agent-workflow',
    )

    fireEvent.click(screen.getByRole('button', { name: '展开凭证配置子菜单' }))

    const credentialToggle = screen.getByRole('button', { name: '收起凭证配置子菜单' })
    const credentialSubmenu = document.getElementById(credentialToggle.getAttribute('aria-controls') ?? '')
    expect(credentialToggle).toHaveAttribute('aria-expanded', 'true')
    expect(credentialSubmenu).toBeInTheDocument()
    expect(within(credentialSubmenu!).getAllByRole('link').map((link) => link.textContent?.trim())).toEqual([
      '模型配置',
      '智能体配置',
      'MCP 连接配置',
    ])
  })

  it('exposes accessible expansion controls with independent aria state', () => {
    renderSidebar()

    const scanToggle = screen.getByRole('button', { name: '展开扫描任务子菜单' })
    const credentialsToggle = screen.getByRole('button', { name: '展开凭证配置子菜单' })
    expect(scanToggle).toHaveAttribute('aria-expanded', 'false')
    expect(credentialsToggle).toHaveAttribute('aria-expanded', 'false')

    fireEvent.click(scanToggle)

    const expandedScanToggle = screen.getByRole('button', { name: '收起扫描任务子菜单' })
    expect(expandedScanToggle).toHaveAttribute('aria-expanded', 'true')
    expect(expandedScanToggle).toHaveAttribute('aria-controls')
    expect(document.getElementById(expandedScanToggle.getAttribute('aria-controls')!)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '展开凭证配置子菜单' })).toHaveAttribute('aria-expanded', 'false')
  })

  it('expands from the initial route and activates only the exact scan child', () => {
    renderSidebar('/tasks/agent-workflow')

    const scanToggle = screen.getByRole('button', { name: '收起扫描任务子菜单' })
    const scanSubmenu = document.getElementById(scanToggle.getAttribute('aria-controls')!)
    expect(within(scanSubmenu!).getAllByRole('link').map((link) => link.textContent?.trim())).toEqual([
      'MCP 安全扫描',
      'Skills 扫描',
      'AI 基础设施扫描',
      'Agent 工作流扫描',
    ])
    expect(within(scanSubmenu!).getByRole('link', { name: 'Agent 工作流扫描' })).toHaveAttribute('aria-current', 'page')
    expect(within(scanSubmenu!).getByRole('link', { name: 'MCP 安全扫描' })).not.toHaveAttribute('aria-current')
    expect(within(scanSubmenu!).getByRole('link', { name: 'AI 基础设施扫描' })).not.toHaveAttribute('aria-current')
    expect(within(scanSubmenu!).getByRole('link', { name: 'Skills 扫描' })).not.toHaveAttribute('aria-current')
  })

  it('updates group expansion after mounted route changes', () => {
    renderSidebar('/reports', true)

    fireEvent.click(screen.getByRole('button', { name: '前往任务列表' }))
    expect(screen.getByRole('button', { name: '收起扫描任务子菜单' })).toHaveAttribute('aria-expanded', 'true')

    fireEvent.click(screen.getByRole('button', { name: '收起扫描任务子菜单' }))
    fireEvent.click(screen.getByRole('button', { name: '前往新建任务' }))
    expect(screen.getByRole('button', { name: '收起扫描任务子菜单' })).toHaveAttribute('aria-expanded', 'true')

    fireEvent.click(screen.getByRole('button', { name: '收起扫描任务子菜单' }))
    fireEvent.click(screen.getByRole('button', { name: '前往 MCP 扫描' }))
    expect(screen.getByRole('link', { name: 'MCP 安全扫描' })).toHaveAttribute('aria-current', 'page')

    fireEvent.click(screen.getByRole('button', { name: '前往 Agent 扫描' }))
    expect(screen.getByRole('link', { name: 'Agent 工作流扫描' })).toHaveAttribute('aria-current', 'page')

    fireEvent.click(screen.getByRole('button', { name: '前往模型配置' }))
    expect(screen.getByRole('button', { name: '收起凭证配置子菜单' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('link', { name: '模型配置' })).toHaveAttribute('aria-current', 'page')
  })

  it.each(['/tasks/ai-infra', '/tasks/ai-infra/new', '/tasks/ai-infra/scan-42'])('activates AI infrastructure scans across its task routes', (path) => {
    renderSidebar(path)

    expect(screen.getByRole('link', { name: '扫描任务' })).toHaveAttribute('href', '/tasks')
    expect(screen.getByRole('link', { name: '扫描任务' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: '收起扫描任务子菜单' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('link', { name: 'AI 基础设施扫描' })).toHaveAttribute('aria-current', 'page')
  })

  it.each(['/tasks/agent-workflow', '/tasks/agent-workflow/new', '/tasks/agent-workflow/scan-42'])('activates Agent scans across its task routes', (path) => {
    renderSidebar(path)
    expect(screen.getByRole('link', { name: 'Agent 工作流扫描' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'AI 基础设施扫描' })).not.toHaveAttribute('aria-current', 'page')
  })
  it('preserves a manual task collapse across query-only navigation', () => {
    renderSidebar('/tasks/ai-infra?status=running', true)

    fireEvent.click(screen.getByRole('button', { name: '收起扫描任务子菜单' }))
    expect(screen.getByRole('button', { name: '展开扫描任务子菜单' })).toHaveAttribute('aria-expanded', 'false')

    fireEvent.click(screen.getByRole('button', { name: '前往 AI 基础设施失败' }))
    expect(screen.getByRole('button', { name: '展开扫描任务子菜单' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('link', { name: 'AI 基础设施扫描' })).not.toBeInTheDocument()
  })

  it('auto-expands task navigation after moving to a new task pathname', () => {
    renderSidebar('/tasks/ai-infra?status=running', true)

    fireEvent.click(screen.getByRole('button', { name: '收起扫描任务子菜单' }))
    fireEvent.click(screen.getByRole('button', { name: '前往新建 AI 基础设施任务' }))

    expect(screen.getByRole('button', { name: '收起扫描任务子菜单' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('link', { name: 'AI 基础设施扫描' })).toHaveAttribute('aria-current', 'page')
  })

  it('activates credentials without activating the rules group', () => {
    renderSidebar('/knowledge/agents')

    expect(screen.getByRole('link', { name: '凭证配置' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('button', { name: '收起凭证配置子菜单' })).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('link', { name: '智能体配置' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: '规则与知识库' })).not.toHaveAttribute('aria-current', 'page')
  })

  it('keeps the rules group active on ordinary knowledge routes', () => {
    renderSidebar('/knowledge/rules')

    expect(screen.getByRole('link', { name: '规则与知识库' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: '凭证配置' })).not.toHaveAttribute('aria-current', 'page')
  })

  it('removes secondary links when the full sidebar is collapsed', () => {
    renderSidebar()
    fireEvent.click(screen.getByRole('button', { name: '展开扫描任务子菜单' }))
    fireEvent.click(screen.getByRole('button', { name: '展开凭证配置子菜单' }))

    fireEvent.click(screen.getByRole('button', { name: '收起导航' }))

    for (const label of ['MCP 安全扫描', 'Skills 扫描', 'AI 基础设施扫描', 'Agent 工作流扫描', '模型配置', '智能体配置']) {
      expect(screen.queryByRole('link', { name: label })).not.toBeInTheDocument()
    }
    expect(screen.getByRole('link', { name: '扫描任务' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '凭证配置' })).toBeInTheDocument()
  })

  it('marks the MCP workbench only on its exact route', () => {
    renderSidebar('/tasks/mcp')

    expect(screen.getByRole('link', { name: 'MCP 安全扫描' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'AI 基础设施扫描' })).not.toHaveAttribute('aria-current')
  })

  it('keeps the dedicated task classification active with additional query parameters', () => {
    renderSidebar('/tasks/ai-infra?status=running&page=2')

    expect(screen.getByRole('link', { name: 'AI 基础设施扫描' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Skills 扫描' })).not.toHaveAttribute('aria-current')
    expect(screen.getByRole('link', { name: 'Agent 工作流扫描' })).not.toHaveAttribute('aria-current')
  })

  it('keeps the MCP parent active for its dedicated subroutes', () => {
    renderSidebar('/tasks/mcp/history')

    expect(screen.getByRole('link', { name: 'MCP 安全扫描' })).toHaveAttribute('aria-current', 'page')
  })
})
