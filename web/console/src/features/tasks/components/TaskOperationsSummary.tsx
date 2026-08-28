/**
 * 功能：展示当前任务分页的运行态势与筛选上下文。
 * 实现：仅用 TaskSummary 状态在本地派生三类当前页信号，不请求数据或修改路由。
 * 输入：当前页任务、服务端匹配总数、查询标签与可选的筛选清除回调。
 * 输出：具名任务运行态势区域、查询信息和非空页面的信号卡片。
 * 依赖：React、Fluent UI v9 与共享任务 DTO。
 */
import { Button, Card, Text, makeStyles, mergeClasses, tokens } from '@fluentui/react-components'

import type { TaskSummary } from '../../../shared/api/types'

const activeStatuses = new Set<TaskSummary['status']>(['dispatching', 'running'])
const pendingStatuses = new Set<TaskSummary['status']>(['pending'])
const attentionStatuses = new Set<TaskSummary['status']>(['failed', 'dispatch_failed', 'dispatch_unknown'])

export interface TaskPageActivity {
  active: number
  pending: number
  attention: number
}

export function deriveTaskPageActivity(tasks: readonly TaskSummary[]): TaskPageActivity {
  const activity: TaskPageActivity = { active: 0, pending: 0, attention: 0 }

  for (const task of tasks) {
    if (activeStatuses.has(task.status)) {
      activity.active += 1
    } else if (pendingStatuses.has(task.status)) {
      activity.pending += 1
    } else if (attentionStatuses.has(task.status)) {
      activity.attention += 1
    }
  }

  return activity
}

interface TaskOperationsSummaryProps {
  tasks: readonly TaskSummary[]
  total: number
  filterLabels: readonly string[]
  onClearFilters?: () => void
}

const useStyles = makeStyles({
  root: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalL,
    padding: tokens.spacingVerticalL,
    border: `1px solid ${tokens.colorNeutralStroke1}`,
    borderRadius: tokens.borderRadiusLarge,
    backgroundColor: tokens.colorNeutralBackground1,
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
  filterLabels: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: tokens.spacingHorizontalXS,
  },
  filterLabel: {
    padding: `${tokens.spacingVerticalXXS} ${tokens.spacingHorizontalS}`,
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    borderRadius: tokens.borderRadiusCircular,
    backgroundColor: tokens.colorNeutralBackground2,
    color: tokens.colorNeutralForeground2,
  },
  total: {
    color: tokens.colorNeutralForeground1,
    fontWeight: tokens.fontWeightSemibold,
    fontVariantNumeric: 'tabular-nums',
  },
  clear: {
    marginLeft: 'auto',
  },
  signals: {
    display: 'grid',
    gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
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
    backgroundColor: tokens.colorNeutralBackground2,
    boxShadow: 'none',
  },
  signalLabel: {
    display: 'block',
    color: tokens.colorNeutralForeground2,
  },
  signalValue: {
    display: 'block',
    marginTop: tokens.spacingVerticalXS,
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase600,
    lineHeight: tokens.lineHeightBase600,
    fontWeight: tokens.fontWeightSemibold,
    fontVariantNumeric: 'tabular-nums',
  },
  activeValue: {
    color: tokens.colorBrandForeground1,
  },
  attentionValue: {
    color: tokens.colorStatusWarningForeground1,
  },
})

export function TaskOperationsSummary({
  tasks,
  total,
  filterLabels,
  onClearFilters,
}: TaskOperationsSummaryProps) {
  const styles = useStyles()
  const activity = deriveTaskPageActivity(tasks)

  return (
    <Card className={styles.root} role="region" aria-label="任务运行态势">
      <Text as="h2" className={styles.heading}>
        任务运行态势
      </Text>
      <div className={styles.query} role="group" aria-label="当前查询">
        <Text className={styles.queryTitle}>当前查询</Text>
        <div className={styles.filterLabels}>
          {filterLabels.map((label) => (
            <Text key={label} className={styles.filterLabel} size={200}>
              {label}
            </Text>
          ))}
        </div>
        <Text className={styles.total}>匹配任务 {total}</Text>
        {onClearFilters ? (
          <Button className={styles.clear} appearance="subtle" onClick={onClearFilters}>
            清除筛选
          </Button>
        ) : null}
      </div>
      {tasks.length > 0 ? (
        <div className={styles.signals} role="group" aria-label="本页运行信号">
          <Card className={styles.signal}>
            <Text className={styles.signalLabel}>本页正在执行</Text>{' '}
            <Text className={mergeClasses(styles.signalValue, styles.activeValue)}>{activity.active}</Text>
          </Card>
          <Card className={styles.signal}>
            <Text className={styles.signalLabel}>本页等待调度</Text>{' '}
            <Text className={styles.signalValue}>{activity.pending}</Text>
          </Card>
          <Card className={styles.signal}>
            <Text className={styles.signalLabel}>本页需关注</Text>{' '}
            <Text className={mergeClasses(styles.signalValue, styles.attentionValue)}>{activity.attention}</Text>
          </Card>
        </div>
      ) : null}
    </Card>
  )
}
