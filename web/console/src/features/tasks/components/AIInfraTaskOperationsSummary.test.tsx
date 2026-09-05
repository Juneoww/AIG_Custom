/**
 * 功能：验证 AI 基础设施任务态势卡片的精确计数与可访问语义。
 * 实现：用完整任务状态矩阵驱动纯派生函数，并渲染四张始终可见的指标卡。
 * 输入：当前查询总数和当前页安全任务摘要。
 * 输出：查询范围、当前页范围、零值及状态归类的回归断言。
 * 依赖：Vitest、Testing Library、任务 DTO 与专属任务态势组件。
 */
import { FluentProvider } from '@fluentui/react-components'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'

import type { TaskSummary } from '../../../shared/api/types'
import { AIInfraTaskTable } from './AIInfraTaskTable'
import { AIInfraWorkbenchHeader } from './AIInfraWorkbenchHeader'
import {
  AIInfraTaskOperationsSummary,
  deriveAIInfraTaskMetrics,
} from './AIInfraTaskOperationsSummary'
import { ledgerDarkTheme } from '../../../shared/theme/tokens'

const baseTask = {
  id: 'task-base',
  owner: 'alice',
  task_type: 'ai_infra_scan',
  status: 'pending',
  created_at: '2026-09-05T01:00:00Z',
  updated_at: '2026-09-05T01:01:00Z',
} as const satisfies TaskSummary

const statusMatrix: readonly TaskSummary[] = [
  { ...baseTask, id: 'task-pending', status: 'pending' },
  { ...baseTask, id: 'task-dispatching', status: 'dispatching' },
  { ...baseTask, id: 'task-running-a', status: 'running' },
  { ...baseTask, id: 'task-running-b', status: 'running' },
  { ...baseTask, id: 'task-failed', status: 'failed' },
  { ...baseTask, id: 'task-dispatch-failed', status: 'dispatch_failed' },
  { ...baseTask, id: 'task-dispatch-unknown', status: 'dispatch_unknown' },
  { ...baseTask, id: 'task-succeeded', status: 'succeeded' },
  { ...baseTask, id: 'task-cancelled', status: 'cancelled' },
]

afterEach(cleanup)

describe('AIInfraTaskOperationsSummary', () => {
  it('仅按规定状态派生当前页执行、等待和需关注计数', () => {
    expect(deriveAIInfraTaskMetrics(statusMatrix, 47)).toEqual({
      matching: 47,
      executing: 2,
      waiting: 2,
      attention: 3,
    })
  })

  it('以具名区域呈现查询总数和当前页范围的四张指标卡', () => {
    render(<AIInfraTaskOperationsSummary tasks={statusMatrix} total={47} />)

    const region = screen.getByRole('region', { name: 'AI 基础设施扫描运行态势' })
    expect(within(region).getByRole('heading', { name: '任务运行态势' })).toBeInTheDocument()

    const cards = within(region).getAllByRole('group')
    expect(cards).toHaveLength(4)
    expect(cards[0]).toHaveAccessibleName('当前查询匹配任务 47')
    expect(cards[0]).toHaveTextContent('匹配任务47当前查询')
    expect(cards[1]).toHaveAccessibleName('当前页正在执行 2')
    expect(cards[1]).toHaveTextContent('正在执行2当前页')
    expect(cards[2]).toHaveAccessibleName('当前页等待调度 2')
    expect(cards[2]).toHaveTextContent('等待调度2当前页')
    expect(cards[3]).toHaveAccessibleName('当前页需关注 3')
    expect(cards[3]).toHaveTextContent('需关注3当前页')

    for (const card of cards) {
      expect(card.querySelector('svg')).not.toBeNull()
    }
  })

  it('当前页为空时仍显示四张真实零值指标卡', () => {
    render(<AIInfraTaskOperationsSummary tasks={[]} total={0} />)

    const region = screen.getByRole('region', { name: 'AI 基础设施扫描运行态势' })
    expect(within(region).getAllByRole('group')).toHaveLength(4)
    expect(region).toHaveTextContent('匹配任务0当前查询')
    expect(region).toHaveTextContent('正在执行0当前页')
    expect(region).toHaveTextContent('等待调度0当前页')
    expect(region).toHaveTextContent('需关注0当前页')
  })

  it('在深色主题中仍保留全部可访问指标与主题化表面', () => {
    render(
      <FluentProvider theme={ledgerDarkTheme}>
        <AIInfraTaskOperationsSummary tasks={statusMatrix} total={47} />
      </FluentProvider>,
    )

    const region = screen.getByRole('region', { name: 'AI 基础设施扫描运行态势' })
    expect(within(region).getAllByRole('group')).toHaveLength(4)
    expect(region.className).not.toBe('')
    expect(region).toHaveTextContent('当前查询')
    expect(region).toHaveTextContent('当前页')
  })
})

describe('AIInfraWorkbenchHeader', () => {
  it('呈现专属标题、说明和调用方操作入口', () => {
    render(<AIInfraWorkbenchHeader action={<button type="button">新建任务</button>} />)

    expect(screen.getByRole('heading', { level: 1, name: 'AI 基础设施扫描' })).toBeInTheDocument()
    expect(screen.getByText('集中跟踪已授权目标的扫描任务')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '新建任务' })).toBeInTheDocument()
  })

  it('可选返回入口保留图标和受控路由', () => {
    render(
      <MemoryRouter>
        <AIInfraWorkbenchHeader backLink={{ to: '/tasks/ai-infra', label: '返回 AI 基础设施扫描任务台' }} />
      </MemoryRouter>,
    )

    const backLink = screen.getByRole('link', { name: '返回 AI 基础设施扫描任务台' })
    expect(backLink).toHaveAttribute('href', '/tasks/ai-infra')
    expect(backLink.querySelector('svg')).not.toBeNull()
  })
})

describe('AIInfraTaskTable', () => {
  it('以六列语义表格展示安全任务摘要和专属详情链接', () => {
    render(
      <MemoryRouter>
        <AIInfraTaskTable tasks={[statusMatrix[0]]} />
      </MemoryRouter>,
    )

    const table = screen.getByRole('table', { name: 'AI 基础设施扫描任务台账' })
    for (const header of ['任务 ID', '负责人', '状态', '创建时间', '更新时间', '操作']) {
      expect(within(table).getByRole('columnheader', { name: header })).toBeInTheDocument()
    }
    expect(within(table).getByText('等待调度')).toBeInTheDocument()
    expect(within(table).getByRole('link', { name: '查看任务 task-pending' })).toHaveAttribute(
      'href',
      '/tasks/ai-infra/task-pending',
    )
    const viewport = screen.getByRole('region', { name: '可横向滚动的 AI 基础设施扫描任务表格' })
    expect(viewport).toHaveAttribute('tabindex', '0')
  })

  it('只在收到真实分页响应时呈现分页元数据和操作', () => {
    const onPreviousPage = vi.fn()
    const onNextPage = vi.fn()
    render(
      <MemoryRouter>
        <AIInfraTaskTable
          tasks={[statusMatrix[0]]}
          pagination={{ total: 47, page: 2, pageSize: 20, onPreviousPage, onNextPage }}
        />
      </MemoryRouter>,
    )

    const pagination = screen.getByRole('navigation', { name: 'AI 基础设施扫描任务分页' })
    expect(pagination).toHaveTextContent('共 47 条')
    expect(pagination).toHaveTextContent('第 2 页')
    expect(pagination).toHaveTextContent('20 条/页')
    fireEvent.click(within(pagination).getByRole('button', { name: '上一页' }))
    fireEvent.click(within(pagination).getByRole('button', { name: '下一页' }))
    expect(onPreviousPage).toHaveBeenCalledTimes(1)
    expect(onNextPage).toHaveBeenCalledTimes(1)
  })
})
