/**
 * 功能：验证 Agent 配置目录概览的完整目录范围与角色语义。
 * 实现：向纯展示组件传入安全总量和权限标记，断言可访问语义与敏感数据边界。
 * 输入：目录总量与可维护标记；测试中的敏感哨兵值不传入组件。
 * 输出：目录数量、管理员/只读提示及敏感文本缺失的回归断言。
 * 依赖：Vitest、Testing Library 与 AgentConfigurationBrief。
 */
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { AgentConfigurationBrief } from './AgentConfigurationBrief'

const agentTokenSentinel = 'agent-token-sentinel'
const promptSentinel = 'prompt-sentinel'
const testOutputSentinel = 'test-output-sentinel'

function expectNoSensitiveText(region: HTMLElement) {
  expect(region).not.toHaveTextContent(agentTokenSentinel)
  expect(region).not.toHaveTextContent(promptSentinel)
  expect(region).not.toHaveTextContent(testOutputSentinel)
}

describe('AgentConfigurationBrief', () => {
  it('展示完整 Agent 目录的安全总量', () => {
    render(<AgentConfigurationBrief total={3} canManage />)

    const brief = screen.getByRole('region', { name: 'Agent 配置目录概览' })
    const scope = within(brief).getByRole('group', { name: '当前目录范围' })

    expect(scope).toHaveTextContent('当前目录 3 项')
    expect(brief).toHaveTextContent('选择一项后在本地工作区查看或维护原文')
    expectNoSensitiveText(brief)
  })

  it('向管理员说明可维护与验证的目录边界', () => {
    render(<AgentConfigurationBrief total={1} canManage />)

    const brief = screen.getByRole('region', { name: 'Agent 配置目录概览' })

    expect(within(brief).getByText('可维护与验证')).toBeInTheDocument()
    expect(brief).toHaveTextContent('选择一项后在本地工作区查看或维护原文')
    expectNoSensitiveText(brief)
  })

  it('向只读角色说明可查看原文，不可修改', () => {
    render(<AgentConfigurationBrief total={1} canManage={false} />)

    const brief = screen.getByRole('region', { name: 'Agent 配置目录概览' })

    expect(within(brief).getByText('可查看原文，不可修改')).toBeInTheDocument()
    expect(brief).toHaveTextContent('选择一项后在本地工作区查看或维护原文')
    expectNoSensitiveText(brief)
  })
})
