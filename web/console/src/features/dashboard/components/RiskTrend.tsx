/**
 * 功能：以可访问的紧凑列图展示最近 30 日快照平均安全分趋势。
 * 实现：使用语义 figure、列表和主题令牌渲染响应式 30 个 UTC 日桶，不在浏览器重算指标。
 * 输入：服务端已聚合的 30 个 TrendPoint。
 * 输出：可由辅助技术逐日读取的无渐变趋势图。
 * 依赖：React、Fluent UI v9 与 dashboard DTO。
 */
import { Text, makeStyles, tokens } from '@fluentui/react-components'

import type { TrendPoint } from '../api'

interface RiskTrendProps {
  points: readonly TrendPoint[]
}

const useStyles = makeStyles({
  figure: {
    margin: 0,
  },
  title: {
    display: 'block',
    marginBottom: tokens.spacingVerticalL,
    color: tokens.colorNeutralForeground1,
    fontWeight: tokens.fontWeightSemibold,
  },
  plot: {
    display: 'grid',
    gridTemplateColumns: 'repeat(30, minmax(0, 1fr))',
    columnGap: tokens.spacingHorizontalXXS,
    alignItems: 'end',
    minWidth: 0,
    height: '176px',
    margin: 0,
    padding: `${tokens.spacingVerticalM} ${tokens.spacingHorizontalS}`,
    listStyleType: 'none',
    borderBottom: `1px solid ${tokens.colorNeutralStroke1}`,
  },
  point: {
    display: 'flex',
    alignItems: 'flex-end',
    justifyContent: 'center',
    height: '100%',
    minWidth: 0,
  },
  bar: {
    width: '100%',
    maxWidth: '6px',
    minHeight: '4px',
    borderRadius: tokens.borderRadiusSmall,
    backgroundColor: tokens.colorPaletteGreenForeground1,
  },
  emptyBar: {
    backgroundColor: tokens.colorNeutralStroke2,
  },
  axis: {
    display: 'flex',
    justifyContent: 'space-between',
    marginTop: tokens.spacingVerticalS,
    color: tokens.colorNeutralForeground3,
  },
})

const dayFormatter = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'UTC',
  month: '2-digit',
  day: '2-digit',
})

function dayLabel(value: string): string {
  return dayFormatter.format(new Date(value))
}

export function RiskTrend({ points }: RiskTrendProps) {
  const styles = useStyles()
  const first = points.at(0)
  const last = points.at(-1)

  return (
    <figure className={styles.figure} aria-label="最近 30 日快照平均安全分">
      <Text className={styles.title}>快照平均安全分趋势</Text>
      <ol className={styles.plot}>
        {points.map((point) => (
          <li
            className={styles.point}
            key={point.date}
            aria-label={`${dayLabel(point.date)}，完成 ${point.completed} 份报告，平均安全分${point.security_score ?? '暂无'}`}
          >
            <span
              className={`${styles.bar} ${point.security_score === null ? styles.emptyBar : ''}`}
              style={{ height: `${point.security_score ?? 0}%` }}
            />
          </li>
        ))}
      </ol>
      <div className={styles.axis} aria-hidden="true">
        <Text size={200}>{first ? dayLabel(first.date) : ''}</Text>
        <Text size={200}>{last ? dayLabel(last.date) : ''}</Text>
      </div>
    </figure>
  )
}
