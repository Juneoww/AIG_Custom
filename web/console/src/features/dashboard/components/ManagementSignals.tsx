/**
 * 功能：作为管理者摘要后的第二层信号，展示高风险、执行中任务和风险映射版本。
 * 实现：纯展示已校验的摘要输入，不发请求、不重算指标，也不接收完整原始响应。
 * 输入：风险摘要、风险映射版本和既有治理活动派生结果。
 * 输出：具名“管理者信号” region 及紧凑的三行语义化信息。
 * 依赖：React、Fluent UI、dashboard DTO 与 ExecutiveSummary 的 GovernanceActivity。
 */
import { Text, makeStyles, tokens } from '@fluentui/react-components'

import type { DashboardView } from '../api'
import type { GovernanceActivity } from './ExecutiveSummary'

interface ManagementSignalsProps {
  risk: DashboardView['risk']
  mappingVersions: DashboardView['mapping_versions']
  activity: GovernanceActivity
}

const useStyles = makeStyles({
  panel: {
    minWidth: 0,
    padding: tokens.spacingVerticalL,
    border: `1px solid ${tokens.colorNeutralStroke1}`,
    borderRadius: tokens.borderRadiusMedium,
    backgroundColor: tokens.colorNeutralBackground1,
    boxShadow: tokens.shadow4,
  },
  title: {
    display: 'block',
    marginBottom: tokens.spacingVerticalM,
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
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 1fr) minmax(0, 2fr)',
    alignItems: 'baseline',
    gap: tokens.spacingHorizontalM,
    paddingTop: tokens.spacingVerticalXS,
    borderTop: `1px solid ${tokens.colorNeutralStroke2}`,
  },
  label: {
    minWidth: 0,
    color: tokens.colorNeutralForeground2,
  },
  value: {
    minWidth: 0,
    margin: 0,
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase400,
    lineHeight: tokens.lineHeightBase400,
    fontWeight: tokens.fontWeightSemibold,
    textAlign: 'right',
    fontVariantNumeric: 'tabular-nums',
  },
  highRisk: {
    color: tokens.colorPaletteDarkOrangeForeground1,
  },
  mappingVersions: {
    overflowWrap: 'anywhere',
  },
})

export function ManagementSignals({ risk, mappingVersions, activity }: ManagementSignalsProps) {
  const styles = useStyles()

  return (
    <section className={styles.panel} aria-label="管理者信号">
      <Text as="h2" className={styles.title}>
        管理者信号
      </Text>
      <dl className={styles.list}>
        <div className={styles.row}>
          <dt className={styles.label}>高风险</dt>
          <dd className={`${styles.value} ${styles.highRisk}`}>{risk.high}</dd>
        </div>
        <div className={styles.row}>
          <dt className={styles.label}>执行中任务</dt>
          <dd className={styles.value}>{activity.activeTasks}</dd>
        </div>
        <div className={styles.row}>
          <dt className={styles.label}>风险映射版本</dt>
          <dd className={`${styles.value} ${styles.mappingVersions}`}>
            {mappingVersions.length > 0 ? mappingVersions.join('、') : '暂无映射版本'}
          </dd>
        </div>
      </dl>
    </section>
  )
}
