/**
 * 功能：统一展示台账加载、空数据、错误和无权限状态。
 * 实现：用 Fluent Card、Spinner 与 Button 保持状态语义和可恢复操作一致。
 * 输入：状态类型、标题、说明及可选操作。
 * 输出：可被辅助技术播报的状态面板。
 * 依赖：Fluent UI v9。
 */
import { Button, Card, Spinner, Text, makeStyles, tokens } from '@fluentui/react-components'

type PanelState = 'loading' | 'empty' | 'error' | 'forbidden'

interface StatePanelProps {
  state: PanelState
  title: string
  description?: string
  actionLabel?: string
  onAction?: () => void
}

const useStyles = makeStyles({
  root: {
    alignItems: 'flex-start',
    padding: tokens.spacingVerticalXXL,
    gap: tokens.spacingVerticalM,
    borderRadius: tokens.borderRadiusMedium,
    boxShadow: 'none',
  },
  title: {
    fontWeight: tokens.fontWeightSemibold,
  },
  description: {
    color: tokens.colorNeutralForeground2,
  },
})

export function StatePanel({ state, title, description, actionLabel, onAction }: StatePanelProps) {
  const styles = useStyles()
  const isLoading = state === 'loading'
  const role = state === 'error' ? 'alert' : 'status'

  return (
    <Card className={styles.root} role={role} aria-live={state === 'error' ? 'assertive' : 'polite'}>
      {isLoading ? <Spinner size="tiny" labelPosition="after" label={title} /> : <Text className={styles.title}>{title}</Text>}
      {!isLoading && description ? <Text className={styles.description}>{description}</Text> : null}
      {!isLoading && actionLabel && onAction ? (
        <Button appearance="secondary" onClick={onAction}>
          {actionLabel}
        </Button>
      ) : null}
    </Card>
  )
}
