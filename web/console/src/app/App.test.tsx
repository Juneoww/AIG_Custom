/**
 * 功能：验证企业控制台产品壳采用新的中文品牌。
 * 实现：通过 Testing Library 渲染真实 App，并断言新旧品牌文本。
 * 输入：App 根组件与 Vitest 的 jsdom 测试环境。
 * 输出：产品壳品牌回归测试结果。
 * 依赖：Vitest、React 与 Testing Library。
 */
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { App } from './App'

describe('App', () => {
  it('renders the configured Chinese product shell without old AIG branding', () => {
    render(<App />)

    expect(screen.getByText('AI 安全治理平台')).toBeInTheDocument()
    expect(screen.queryByText(/^A\.I\.G$/)).not.toBeInTheDocument()
  })

  it('starts each product shell render with a single brand name', () => {
    render(<App />)

    expect(screen.getByText('AI 安全治理平台')).toBeInTheDocument()
  })

  it('exposes build status terms and definitions', () => {
    render(<App />)

    expect(screen.getAllByRole('term')).toHaveLength(2)
    expect(screen.getAllByRole('definition')).toHaveLength(2)
  })
})
