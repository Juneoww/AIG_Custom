/**
 * 功能：展示当前报告分页的复核态势与固定查询上下文。
 * 实现：仅用 ReportSummaryView 的高风险摘要在本地派生本页复核信号，不请求数据或修改路由。
 * 输入：当前页报告与服务端匹配总数。
 * 输出：具名报告复核态势区域、当前查询和非空页面的信号卡片。
 * 依赖：React、Fluent UI v9 与报告安全 DTO。
 */
import { Badge, Card, Text, makeStyles, tokens } from '@fluentui/react-components'

import type { ReportSummaryView } from '../api'

export interface ReportPageReviewActivity {
  priorityReports: number
  highFindings: number
}

export function deriveReportPageReviewActivity(reports: readonly ReportSummaryView[]): ReportPageReviewActivity {
  const activity: ReportPageReviewActivity = { priorityReports: 0, highFindings: 0 }

  for (const report of reports) {
    if (report.risk.high > 0) {
      activity.priorityReports += 1
    }
    activity.highFindings += report.risk.high
  }

  return activity
}

const useStyles = makeStyles({
  root: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalL,
    padding: tokens.spacingVerticalL,
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    borderRadius: tokens.borderRadiusLarge,
    backgroundColor: tokens.colorNeutralBackground2,
    boxShadow: tokens.shadow2,
  },
  heading: {
    display: 'block',
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase500,
    lineHeight: tokens.lineHeightBase500,
    fontWeight: tokens.fontWeightSemibold,
  },
  query: {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: tokens.spacingHorizontalS,
  },
  queryTitle: {
    color: tokens.colorNeutralForeground2,
    fontWeight: tokens.fontWeightSemibold,
  },
  queryLabel: {
    padding: `${tokens.spacingVerticalXXS} ${tokens.spacingHorizontalS}`,
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    borderRadius: tokens.borderRadiusCircular,
    backgroundColor: tokens.colorNeutralBackground1,
    color: tokens.colorNeutralForeground2,
  },
  total: {
    color: tokens.colorNeutralForeground1,
    fontWeight: tokens.fontWeightSemibold,
    fontVariantNumeric: 'tabular-nums',
  },
  signals: {
    display: 'grid',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
    gap: tokens.spacingHorizontalM,
    '@media (max-width: 960px)': {
      gridTemplateColumns: '1fr',
    },
  },
  signal: {
    minWidth: 0,
    padding: tokens.spacingVerticalM,
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    borderRadius: tokens.borderRadiusMedium,
    backgroundColor: tokens.colorNeutralBackground1,
    boxShadow: 'none',
  },
  signalLabel: {
    color: tokens.colorNeutralForeground2,
  },
})

export function ReportPageReviewSummary({
  reports,
  total,
}: {
  reports: readonly ReportSummaryView[]
  total: number
}) {
  const styles = useStyles()
  const activity = deriveReportPageReviewActivity(reports)

  return (
    <Card className={styles.root} role="region" aria-label="报告复核态势">
      <Text as="h2" className={styles.heading}>
        报告复核态势
      </Text>
      <div className={styles.query} role="group" aria-label="当前查询">
        <Text className={styles.queryTitle}>当前查询</Text>
        <Text className={styles.queryLabel} size={200}>
          全部报告
        </Text>
        <Text className={styles.total}>匹配报告 {total}</Text>
      </div>
      {reports.length > 0 ? (
        <div className={styles.signals} role="group" aria-label="本页复核信号">
          <Card className={styles.signal}>
            <Text className={styles.signalLabel}>本页需优先复核</Text>{' '}
            <Badge color="warning">{activity.priorityReports}</Badge>
          </Card>
          <Card className={styles.signal}>
            <Text className={styles.signalLabel}>本页高风险发现</Text>{' '}
            <Badge color="danger">{activity.highFindings}</Badge>
          </Card>
        </div>
      ) : null}
    </Card>
  )
}
