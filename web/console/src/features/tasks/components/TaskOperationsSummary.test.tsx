/**
 * 功能：验证任务运行态势摘要的状态计数、语义结构和筛选清除入口。
 * 实现：直接渲染纯展示组件，使用 TaskSummary 安全摘要构造确定性状态矩阵。
 * 输入：当前页任务、查询匹配总数、筛选标签与可选清除回调。
 * 输出：运行态势可访问性、状态计数和空结果边界的回归断言。
 * 依赖：Vitest、Testing Library、任务共享 DTO 与 TaskOperationsSummary。
 */
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { TaskSummary } from '../../../shared/api/types'
import { deriveTaskPageActivity, TaskOperationsSummary } from './TaskOperationsSummary'

const baseTask = {
  id: 'task-base',
  owner: 'alice',
  task_type: 'mcp_scan',
  status: 'pending',
  created_at: '2026-08-18T01:00:00Z',
  updated_at: '2026-08-18T01:01:00Z',
} as const satisfies TaskSummary

const statusMatrix: readonly TaskSummary[] = [
  { ...baseTask, id: 'task-pending', status: 'pending' },
  { ...baseTask, id: 'task-dispatching', status: 'dispatching' },
  { ...baseTask, id: 'task-running', status: 'running' },
  { ...baseTask, id: 'task-failed', status: 'failed' },
  { ...baseTask, id: 'task-dispatch-failed', status: 'dispatch_failed' },
  { ...baseTask, id: 'task-dispatch-unknown', status: 'dispatch_unknown' },
  { ...baseTask, id: 'task-succeeded', status: 'succeeded' },
  { ...baseTask, id: 'task-cancelled', status: 'cancelled' },
]

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('TaskOperationsSummary', () => {
  it('仅按当前页 TaskSummary 状态矩阵派生活跃、等待和需关注计数', () => {
    expect(deriveTaskPageActivity(statusMatrix)).toEqual({
      active: 2,
      pending: 1,
      attention: 3,
    })
  })

  it('展示具名查询和当前页运行信号组，同时保留匹配总数与筛选标签', () => {
    render(
      <TaskOperationsSummary
        tasks={statusMatrix}
        total={47}
        filterLabels={['全部状态', '全部类型']}
      />,
    )

    const summary = screen.getByRole('region', { name: '任务运行态势' })
    const query = within(summary).getByRole('group', { name: '当前查询' })
    const signals = within(summary).getByRole('group', { name: '本页运行信号' })

    expect(query).toHaveTextContent('全部状态')
    expect(query).toHaveTextContent('全部类型')
    expect(query).toHaveTextContent('当前查询匹配任务 47')
    expect(signals).toHaveTextContent('本页正在执行 2')
    expect(signals).toHaveTextContent('本页等待调度 1')
    expect(signals).toHaveTextContent('本页需关注 3')
  })

  it('空任务时保留查询和清除入口，不渲染零值信号且只在点击后回调', () => {
    const onClearFilters = vi.fn()
    render(
      <TaskOperationsSummary
        tasks={[]}
        total={0}
        filterLabels={['状态：执行中', '类型：Agent 扫描']}
        onClearFilters={onClearFilters}
      />,
    )

    const summary = screen.getByRole('region', { name: '任务运行态势' })
    const query = within(summary).getByRole('group', { name: '当前查询' })
    expect(query).toHaveTextContent('状态：执行中')
    expect(query).toHaveTextContent('类型：Agent 扫描')
    expect(query).toHaveTextContent('当前查询匹配任务 0')
    expect(summary).not.toHaveTextContent('本页正在执行 0')
    expect(summary).not.toHaveTextContent('本页等待调度 0')
    expect(summary).not.toHaveTextContent('本页需关注 0')

    const clearButton = screen.getByRole('button', { name: '清除筛选' })
    expect(onClearFilters).not.toHaveBeenCalled()
    fireEvent.click(clearButton)
    expect(onClearFilters).toHaveBeenCalledTimes(1)
  })
})
