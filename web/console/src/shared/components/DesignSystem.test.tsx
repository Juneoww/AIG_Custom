/**
 * 功能：验证监管台账基础组件的语义、状态与键盘可达性。
 * 实现：渲染公开组件 API，并通过可访问角色检查标题、表格、状态和指标。
 * 输入：页面标题、状态说明、列定义、行数据和指标状态。
 * 输出：DOM 语义与可聚焦操作断言。
 * 依赖：Vitest、Testing Library、React 与 Fluent UI v9。
 */
import { FluentProvider, tokens } from '@fluentui/react-components'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { DataTable } from './DataTable'
import { MetricCard } from './MetricCard'
import { PageHeader } from './PageHeader'
import { StatePanel } from './StatePanel'
import { ledgerColors, ledgerDarkTheme } from '../theme/tokens'

describe('regulatory ledger components', () => {
  it('renders one semantic page heading and accessible actions', () => {
    render(
      <PageHeader title="扫描任务" description="查看任务执行与风险处置进度。">
        <button type="button">新建任务</button>
      </PageHeader>,
    )

    expect(screen.getByRole('heading', { level: 1, name: '扫描任务' })).toBeInTheDocument()
    screen.getByRole('button', { name: '新建任务' }).focus()
    expect(screen.getByRole('button', { name: '新建任务' })).toHaveFocus()
  })

  it('uses native table semantics with a caption and scoped column headers', () => {
    render(
      <DataTable
        caption="扫描任务台账"
        columns={[
          { id: 'name', header: '任务名称', render: (row) => row.name },
          { id: 'status', header: '状态', render: (row) => row.status },
          { id: 'summary', header: '摘要', render: (row) => `${row.name}：${row.status}` },
        ]}
        rows={[{ id: 'task-1', name: '模型网关巡检', status: '待处置' }]}
        getRowKey={(row) => row.id}
      />,
    )

    expect(screen.getByRole('table', { name: '扫描任务台账' }).tagName).toBe('TABLE')
    expect(screen.getAllByRole('columnheader')).toHaveLength(3)
    expect(screen.getAllByRole('columnheader').every((header) => header.tagName === 'TH')).toBe(true)
    expect(screen.getByRole('cell', { name: '模型网关巡检' })).toBeInTheDocument()
    expect(screen.getByRole('cell', { name: '模型网关巡检：待处置' })).toBeInTheDocument()
    expect(getComputedStyle(screen.getByText('扫描任务台账')).paddingBottom).toBe(tokens.spacingVerticalS)
  })

  it('rejects duplicate semantic column identifiers', () => {
    expect(() =>
      render(
        <DataTable
          caption="重复列台账"
          columns={[
            { id: 'duplicate', header: '列一', render: () => '一' },
            { id: 'duplicate', header: '列二', render: () => '二' },
          ]}
          rows={[{ id: 'task-1' }]}
          getRowKey={(row) => row.id}
        />,
      ),
    ).toThrow('表格列 id 必须唯一')
  })

  it('announces loading and error states and exposes a retry action', () => {
    const onRetry = vi.fn()
    const { rerender } = render(<StatePanel state="loading" title="正在加载台账" />)

    expect(screen.getByRole('status')).toHaveTextContent('正在加载台账')
    expect(getComputedStyle(screen.getByRole('status').firstElementChild!).gap).toBe(
      tokens.spacingVerticalS,
    )

    rerender(
      <StatePanel
        state="error"
        title="暂时无法加载"
        description="请检查网络后重试。"
        actionLabel="重试"
        onAction={onRetry}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: '重试' }))

    expect(screen.getByRole('alert')).toHaveTextContent('暂时无法加载')
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('labels a metric and reserves risk color for semantic status', () => {
    render(<MetricCard label="高危漏洞" value="12" status="high" supportingText="较昨日新增 2 项" />)

    expect(screen.getByRole('group', { name: '高危漏洞' })).toHaveAttribute('data-status', 'high')
    expect(screen.getByText('12')).toHaveClass('ledger-metric')
  })

  it('uses the reviewed dark risk token inside a dark Fluent theme', () => {
    render(
      <FluentProvider data-testid="dark-theme" theme={ledgerDarkTheme}>
        <MetricCard label="高危漏洞" value="12" status="high" />
      </FluentProvider>,
    )

    expect(screen.getByText('12')).toHaveStyle({ color: 'var(--colorPaletteRedForeground1)' })
    expect(screen.getByTestId('dark-theme')).toBeInTheDocument()
    expect(ledgerDarkTheme.colorPaletteRedForeground1).toBe(ledgerColors.dark.statusHigh)
  })
})
