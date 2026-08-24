/**
 * 功能：展示监管台账的单项指标及其真实风险状态。
 * 实现：使用 Fluent Card 分层展示标签、指标值和辅助说明，风险色仅绑定状态值。
 * 输入：指标标签、值、可选辅助说明与状态。
 * 输出：具名、可读的指标分组。
 * 依赖：Fluent UI v9 与监管台账主题令牌。
 */
import { Card, Text, makeStyles, mergeClasses, tokens } from '@fluentui/react-components'
import type { ReactNode } from 'react'

type MetricStatus = 'neutral' | 'high' | 'medium' | 'low' | 'success'

interface MetricCardProps {
  label: string
  value: ReactNode
  supportingText?: string
  status?: MetricStatus
}

const useStyles = makeStyles({
  root: {
    padding: tokens.spacingVerticalL,
    gap: tokens.spacingVerticalS,
    borderRadius: tokens.borderRadiusMedium,
    boxShadow: 'none',
  },
  label: {
    color: tokens.colorNeutralForeground2,
  },
  value: {
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase600,
    lineHeight: tokens.lineHeightBase600,
    fontWeight: tokens.fontWeightSemibold,
    fontVariantNumeric: 'tabular-nums',
  },
  supporting: {
    color: tokens.colorNeutralForeground2,
  },
  high: { color: tokens.colorPaletteRedForeground1 },
  medium: { color: tokens.colorPaletteDarkOrangeForeground1 },
  low: { color: tokens.colorPaletteBlueForeground2 },
  success: { color: tokens.colorPaletteGreenForeground1 },
})

export function MetricCard({ label, value, supportingText, status = 'neutral' }: MetricCardProps) {
  const styles = useStyles()
  const statusClass = status === 'neutral' ? undefined : styles[status]

  return (
    <Card className={styles.root} role="group" aria-label={label} data-status={status}>
      <Text className={styles.label} size={200}>
        {label}
      </Text>
      <Text className={mergeClasses(styles.value, statusClass, 'ledger-metric')}>{value}</Text>
      {supportingText ? (
        <Text className={styles.supporting} size={200}>
          {supportingText}
        </Text>
      ) : null}
    </Card>
  )
}
