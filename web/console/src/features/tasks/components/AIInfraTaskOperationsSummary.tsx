/**
 * 功能：展示 AI 基础设施扫描当前查询与当前页的真实运行态势。
 * 实现：按受限任务状态派生四项指标，始终呈现零值且明确统计范围。
 * 输入：当前页安全任务摘要和服务端当前查询匹配总数。
 * 输出：带语义图标和可访问名称的四张指标卡。
 * 依赖：React、Fluent UI、Fluent Icons、任务 DTO 与共享工作台样式。
 */
import { AlertRegular, ClipboardTaskRegular, ClockRegular, PlayCircleRegular } from '@fluentui/react-icons'
import { Text, mergeClasses } from '@fluentui/react-components'

import type { TaskSummary } from '../../../shared/api/types'
import { useAIInfraWorkbenchStyles } from './AIInfraWorkbench.styles'

export interface AIInfraTaskMetrics {
  matching: number
  executing: number
  waiting: number
  attention: number
}

export function deriveAIInfraTaskMetrics(tasks: readonly TaskSummary[], total: number): AIInfraTaskMetrics {
  const metrics: AIInfraTaskMetrics = { matching: total, executing: 0, waiting: 0, attention: 0 }

  for (const task of tasks) {
    switch (task.status) {
      case 'running':
        metrics.executing += 1
        break
      case 'pending':
      case 'dispatching':
        metrics.waiting += 1
        break
      case 'failed':
      case 'dispatch_failed':
      case 'dispatch_unknown':
        metrics.attention += 1
        break
      default:
        break
    }
  }

  return metrics
}

interface AIInfraTaskOperationsSummaryProps {
  tasks: readonly TaskSummary[]
  total: number
  label?: string
}

interface MetricCardProps {
  label: string
  scope: string
  value: number
  icon: typeof ClipboardTaskRegular
  tone?: 'matching' | 'running' | 'waiting' | 'attention'
}

function MetricCard({ label, scope, value, icon: Icon, tone }: MetricCardProps) {
  const styles = useAIInfraWorkbenchStyles()
  const toneClass = tone === 'matching'
    ? styles.metricMatching
    : tone === 'running'
      ? styles.metricRunning
    : tone === 'waiting'
      ? styles.metricWaiting
      : tone === 'attention'
        ? styles.metricAttention
        : undefined
  const accessibleName = `${scope}${label} ${value}`

  return (
    <div className={styles.metric} role="group" aria-label={accessibleName}>
      <span className={mergeClasses(styles.metricIcon, toneClass)} aria-hidden="true"><Icon /></span>
      <div className={styles.metricContent}>
        <Text className={styles.metricLabel}>{label}</Text>
        <Text className={styles.metricValue}>{value}</Text>
        <Text className={styles.metricScope}>{scope}</Text>
      </div>
    </div>
  )
}

export function AIInfraTaskOperationsSummary({ tasks, total, label = 'AI 基础设施扫描' }: AIInfraTaskOperationsSummaryProps) {
  const styles = useAIInfraWorkbenchStyles()
  const metrics = deriveAIInfraTaskMetrics(tasks, total)

  return (
    <section className={styles.surface} role="region" aria-label={`${label}运行态势`}>
      <h2 className={styles.sectionHeading}>任务运行态势</h2>
      <div className={styles.metrics}>
        <MetricCard label="匹配任务" scope="当前查询" value={metrics.matching} icon={ClipboardTaskRegular} tone="matching" />
        <MetricCard label="正在执行" scope="当前页" value={metrics.executing} icon={PlayCircleRegular} tone="running" />
        <MetricCard label="等待调度" scope="当前页" value={metrics.waiting} icon={ClockRegular} tone="waiting" />
        <MetricCard label="需关注" scope="当前页" value={metrics.attention} icon={AlertRegular} tone="attention" />
      </div>
    </section>
  )
}
