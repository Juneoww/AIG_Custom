/**
 * 功能：从已校验 DashboardView 呈现首层风险态势与治理动态。
 * 实现：纯展示与本地派生，不发请求、不重算服务端安全分。
 * 输入：DashboardView。
 * 输出：具名“管理者摘要” region。
 * 依赖：React、Fluent UI 和 dashboard DTO。
 */
import { Text, makeStyles, tokens } from '@fluentui/react-components'

import type { TaskSummary } from '../../../shared/api/types'
import type { DashboardView, TrendPoint } from '../api'

export interface GovernanceActivity {
  completedScans: number
  activeTasks: number
  queuedTasks: number
}

export function deriveGovernanceActivity(
  trend: readonly TrendPoint[],
  recentTasks: readonly TaskSummary[],
): GovernanceActivity {
  const completedScans = trend.reduce((total, point) => total + point.completed, 0)
  let activeTasks = 0
  let queuedTasks = 0

  for (const task of recentTasks) {
    if (task.status === 'pending' || task.status === 'dispatching' || task.status === 'running') {
      activeTasks += 1
    }
    if (task.status === 'pending' || task.status === 'dispatching') {
      queuedTasks += 1
    }
  }

  return { completedScans, activeTasks, queuedTasks }
}

interface ExecutiveSummaryProps {
  view: DashboardView
}

const useStyles = makeStyles({
  summary: {
    padding: tokens.spacingVerticalXL,
    border: `1px solid ${tokens.colorNeutralStroke1}`,
    borderRadius: tokens.borderRadiusLarge,
    backgroundColor: tokens.colorNeutralBackground1,
    boxShadow: tokens.shadow4,
  },
  title: {
    display: 'block',
    marginBottom: tokens.spacingVerticalL,
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase500,
    lineHeight: tokens.lineHeightBase500,
    fontWeight: tokens.fontWeightSemibold,
  },
  axes: {
    display: 'grid',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
    gap: tokens.spacingHorizontalXL,
    '@media (max-width: 960px)': {
      gridTemplateColumns: '1fr',
    },
  },
  axis: {
    minWidth: 0,
  },
  axisTitle: {
    margin: `0 0 ${tokens.spacingVerticalM} 0`,
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase400,
    lineHeight: tokens.lineHeightBase400,
    fontWeight: tokens.fontWeightSemibold,
  },
  list: {
    display: 'grid',
    gap: tokens.spacingVerticalS,
    margin: 0,
  },
  row: {
    display: 'flex',
    alignItems: 'baseline',
    justifyContent: 'space-between',
    gap: tokens.spacingHorizontalM,
    paddingTop: tokens.spacingVerticalXS,
    borderTop: `1px solid ${tokens.colorNeutralStroke2}`,
  },
  label: {
    color: tokens.colorNeutralForeground2,
    fontVariantNumeric: 'tabular-nums',
  },
  value: {
    margin: 0,
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase500,
    lineHeight: tokens.lineHeightBase500,
    fontWeight: tokens.fontWeightSemibold,
    fontVariantNumeric: 'tabular-nums',
  },
  highRisk: {
    color: tokens.colorPaletteDarkOrangeForeground1,
  },
  completedScansValue: {
    color: tokens.colorPaletteGreenForeground1,
  },
  notice: {
    display: 'block',
    marginTop: tokens.spacingVerticalM,
    color: tokens.colorNeutralForeground2,
  },
})

export function ExecutiveSummary({ view }: ExecutiveSummaryProps) {
  const styles = useStyles()
  const governance = deriveGovernanceActivity(view.trend, view.recent_tasks)

  return (
    <section className={styles.summary} role="region" aria-label="管理者摘要">
      <Text as="h2" className={styles.title}>
        管理者摘要
      </Text>
      <div className={styles.axes}>
        <div className={styles.axis} role="group" aria-labelledby="executive-summary-risk-title">
          <h3 id="executive-summary-risk-title" className={styles.axisTitle}>
            风险态势
          </h3>
          <dl className={styles.list}>
            <div className={styles.row}>
              <dt className={styles.label}>当前总体安全分</dt>
              <dd className={styles.value}>{view.security_score ?? '暂无'}</dd>
            </div>
            <div className={styles.row}>
              <dt className={styles.label}>高风险</dt>
              <dd className={`${styles.value} ${styles.highRisk}`}>{view.risk.high}</dd>
            </div>
            <div className={styles.row}>
              <dt className={styles.label}>中风险</dt>
              <dd className={styles.value}>{view.risk.medium}</dd>
            </div>
            <div className={styles.row}>
              <dt className={styles.label}>低风险</dt>
              <dd className={styles.value}>{view.risk.low}</dd>
            </div>
          </dl>
          {view.mapping_versions.length > 1 ? (
            <Text className={styles.notice}>当前总览包含多个风险映射版本</Text>
          ) : null}
        </div>
        <div className={styles.axis} role="group" aria-labelledby="executive-summary-governance-title">
          <h3 id="executive-summary-governance-title" className={styles.axisTitle}>
            本期治理动态
          </h3>
          <dl className={styles.list}>
            <div className={styles.row}>
              <dt className={styles.label}>近 30 日已完成扫描</dt>
              <dd className={`${styles.value} ${styles.completedScansValue}`}>{governance.completedScans}</dd>
            </div>
            <div className={styles.row}>
              <dt className={styles.label}>执行中任务</dt>
              <dd className={styles.value}>{governance.activeTasks}</dd>
            </div>
            <div className={styles.row}>
              <dt className={styles.label}>待调度任务</dt>
              <dd className={styles.value}>{governance.queuedTasks}</dd>
            </div>
          </dl>
        </div>
      </div>
    </section>
  )
}
