/**
 * 功能：验证报告复核态势的计数、语义分组和空结果边界。
 * 实现：直接渲染纯展示组件，并以 ReportSummaryView 安全摘要构造固定风险矩阵。
 * 输入：当前页报告与服务端匹配总数。
 * 输出：复核态势区域、当前查询与本页信号的回归断言。
 * 依赖：Vitest、Testing Library、报告安全 DTO 与 ReportPageReviewSummary。
 */
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import type { ReportSummaryView } from '../api'
import { deriveReportPageReviewActivity, ReportPageReviewSummary } from './ReportPageReviewSummary'

const reports: readonly ReportSummaryView[] = [
  {
    id: 'report-1',
    task_id: 'task-1',
    task_type: 'mcp_scan',
    completed_at: '2026-08-28T01:00:00Z',
    created_at: '2026-08-28T00:00:00Z',
    risk: { mapping_version: 'risk-v2', high: 2, medium: 1, low: 3, score: 72 },
    brand_product_name: '报告一',
  },
  {
    id: 'report-2',
    task_id: 'task-2',
    task_type: 'ai_infra_scan',
    completed_at: '2026-08-28T02:00:00Z',
    created_at: '2026-08-28T01:00:00Z',
    risk: { mapping_version: 'risk-v2', high: 0, medium: 2, low: 4, score: 48 },
    brand_product_name: '报告二',
  },
  {
    id: 'report-3',
    task_id: 'task-3',
    task_type: 'agent_scan',
    completed_at: '2026-08-28T03:00:00Z',
    created_at: '2026-08-28T02:00:00Z',
    risk: { mapping_version: 'risk-v2', high: 1, medium: 0, low: 1, score: 65 },
    brand_product_name: '报告三',
  },
]

describe('ReportPageReviewSummary', () => {
  it('仅按当前页 ReportSummaryView 风险摘要派生优先复核和高风险发现计数', () => {
    expect(deriveReportPageReviewActivity(reports)).toEqual({
      priorityReports: 2,
      highFindings: 3,
    })
  })

  it('展示具名当前查询和本页复核信号组', () => {
    render(<ReportPageReviewSummary reports={reports} total={45} />)

    const summary = screen.getByRole('region', { name: '报告复核态势' })
    const query = within(summary).getByRole('group', { name: '当前查询' })
    const signals = within(summary).getByRole('group', { name: '本页复核信号' })

    expect(query).toHaveTextContent('当前查询')
    expect(query).toHaveTextContent('全部报告')
    expect(query).toHaveTextContent('匹配报告 45')
    expect(signals).toHaveTextContent('本页需优先复核 2')
    expect(signals).toHaveTextContent('本页高风险发现 3')
  })

  it('空报告时保留当前查询且不渲染零值复核信号', () => {
    render(<ReportPageReviewSummary reports={[]} total={0} />)

    const summary = screen.getByRole('region', { name: '报告复核态势' })
    const query = within(summary).getByRole('group', { name: '当前查询' })

    expect(query).toHaveTextContent('当前查询')
    expect(query).toHaveTextContent('全部报告')
    expect(query).toHaveTextContent('匹配报告 0')
    expect(within(summary).queryByRole('group', { name: '本页复核信号' })).not.toBeInTheDocument()
    expect(summary).not.toHaveTextContent('本页需优先复核 0')
    expect(summary).not.toHaveTextContent('本页高风险发现 0')
  })
})
