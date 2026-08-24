/**
 * 功能：验证角色侧栏的顺序、当前页语义、品牌和可访问折叠行为。
 * 实现：在真实路由与主题上下文中渲染侧栏并模拟折叠操作。
 * 输入：普通用户角色、报告页路径与配置品牌。
 * 输出：精确导航集合和折叠后的可访问链接。
 * 依赖：Testing Library、MemoryRouter、主题与公共品牌 Provider。
 */
import { fireEvent, render, screen } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { PublicBrandProvider } from '../../shared/brand/PublicBrandProvider'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { createAppQueryClient } from '../providers/AppProviders'
import { Sidebar } from './Sidebar'

function renderSidebar() {
  return render(
    <ThemeProvider initialMode="light">
      <QueryClientProvider client={createAppQueryClient()}>
        <MemoryRouter initialEntries={['/reports']}>
          <PublicBrandProvider
            initialConfig={{ product_name: '超长的企业人工智能安全治理平台名称', primary_color: '#005a9e', logo_data_url: '' }}
          >
            <Sidebar role="user" />
          </PublicBrandProvider>
        </MemoryRouter>
      </QueryClientProvider>
    </ThemeProvider>,
  )
}

describe('Sidebar', () => {
  it('renders the user navigation in order and marks the active link', () => {
    renderSidebar()

    expect(screen.getAllByRole('link').map((link) => link.textContent?.trim())).toEqual([
      '治理总览',
      '扫描任务',
      '安全报告',
      '模型与凭据',
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
})
