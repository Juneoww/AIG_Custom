/**
 * 功能：展示 Agent 配置目录的安全范围与当前角色操作边界。
 * 实现：仅根据调用方传入的目录总量和维护权限渲染 Fluent UI 纯展示区域。
 * 输入：安全的 Agent 配置总量与可维护标记；不接收名称、原文、模板、Prompt 或测试输出。
 * 输出：具名目录概览、权限语义与进入本地工作区前的操作提示。
 * 敏感数据边界：组件不请求数据、不读取路由或会话状态，也不持有任何配置原文。
 */
import { Badge, Card, Text, makeStyles, tokens } from '@fluentui/react-components'

export interface AgentConfigurationBriefProps {
  total: number
  canManage: boolean
}

const useStyles = makeStyles({
  root: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalL,
    minWidth: '0',
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
    fontWeight: tokens.fontWeightSemibold,
    lineHeight: tokens.lineHeightBase500,
  },
  scope: {
    display: 'flex',
    alignItems: 'center',
    flexWrap: 'wrap',
    gap: tokens.spacingHorizontalS,
    minWidth: '0',
  },
  scopeLabel: {
    color: tokens.colorNeutralForeground2,
    fontWeight: tokens.fontWeightSemibold,
  },
  scopeValue: {
    padding: `${tokens.spacingVerticalXXS} ${tokens.spacingHorizontalS}`,
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    borderRadius: tokens.borderRadiusCircular,
    backgroundColor: tokens.colorNeutralBackground1,
    color: tokens.colorNeutralForeground1,
    fontVariantNumeric: 'tabular-nums',
    fontWeight: tokens.fontWeightSemibold,
  },
  signals: {
    display: 'grid',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
    gap: tokens.spacingHorizontalM,
    minWidth: '0',
    '@media (max-width: 960px)': {
      gridTemplateColumns: '1fr',
    },
  },
  signal: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalXS,
    minWidth: '0',
    padding: tokens.spacingVerticalM,
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    borderRadius: tokens.borderRadiusMedium,
    backgroundColor: tokens.colorNeutralBackground1,
    boxShadow: 'none',
  },
  signalLabel: {
    color: tokens.colorNeutralForeground2,
  },
  boundary: {
    display: 'flex',
    alignItems: 'center',
    flexWrap: 'wrap',
    gap: tokens.spacingHorizontalS,
    minWidth: '0',
  },
  guidance: {
    color: tokens.colorNeutralForeground2,
  },
})

export function AgentConfigurationBrief({ total, canManage }: AgentConfigurationBriefProps) {
  const styles = useStyles()
  const permissionLabel = canManage ? '可维护与验证' : '可查看原文，不可修改'
  const permissionCopy = canManage
    ? '当前角色可在受控流程中维护配置并执行单次验证。'
    : '当前角色仅可查看已选择配置的原文。'

  return (
    <Card className={styles.root} role="region" aria-label="Agent 配置目录概览">
      <Text as="h2" className={styles.heading}>Agent 配置目录概览</Text>
      <div className={styles.scope} role="group" aria-label="当前目录范围">
        <Text className={styles.scopeLabel}>当前目录范围</Text>
        <Text className={styles.scopeValue}>当前目录 {total} 项</Text>
      </div>
      <div className={styles.signals} role="group" aria-label="目录操作边界">
        <Card className={styles.signal}>
          <Text className={styles.signalLabel}>目录范围</Text>
          <Text>完整 Agent 配置目录</Text>
        </Card>
        <Card className={styles.signal}>
          <Text className={styles.signalLabel}>当前权限</Text>
          <div className={styles.boundary}>
            <Badge color={canManage ? 'brand' : 'informative'}>{permissionLabel}</Badge>
            <Text>{permissionCopy}</Text>
          </div>
        </Card>
      </div>
      <Text className={styles.guidance}>选择一项后在本地工作区查看或维护原文</Text>
    </Card>
  )
}
