/**
 * 功能：展示模型目录当前查询与当前页的治理态势。
 * 实现：仅从调用方传入的安全目录 DTO 和可配置谓词派生计数，不请求数据或读取路由。
 * 输入：ModelCatalogPage 与 ModelCatalogItem 的可配置判定函数。
 * 输出：具名模型治理态势区域、查询上下文和非空页面的治理信号。
 * 依赖：React、Fluent UI v9 与模型安全 DTO。
 */
import { Badge, Card, Text, makeStyles, tokens } from '@fluentui/react-components'

import type { ModelCatalogItem, ModelCatalogPage } from '../api'

export interface ModelCatalogGovernanceSignals {
  manageableModels: number
  disabledModels: number
  readOnlyModels: number
}

export function deriveModelCatalogGovernanceSignals(
  items: readonly ModelCatalogItem[],
  isManageable: (item: ModelCatalogItem) => boolean,
): ModelCatalogGovernanceSignals {
  return {
    manageableModels: items.filter(isManageable).length,
    disabledModels: items.filter((item) => item.disabled).length,
    readOnlyModels: items.filter((item) => item.read_only).length,
  }
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
    backgroundColor: tokens.colorNeutralBackground1,
    boxShadow: 'none',
  },
  signalLabel: {
    color: tokens.colorNeutralForeground2,
  },
  emptyState: {
    color: tokens.colorNeutralForeground2,
  },
})

export interface ModelCatalogGovernanceSummaryProps {
  catalog: ModelCatalogPage
  isManageable: (item: ModelCatalogItem) => boolean
}

export function ModelCatalogGovernanceSummary({
  catalog,
  isManageable,
}: ModelCatalogGovernanceSummaryProps) {
  const styles = useStyles()
  const signals = deriveModelCatalogGovernanceSignals(catalog.items, isManageable)
  const hasCurrentPageItems = catalog.items.length > 0

  return (
    <Card className={styles.root} role="region" aria-label="模型治理态势">
      <Text as="h2" className={styles.heading}>
        模型治理态势
      </Text>
      <div className={styles.query} role="group" aria-label="当前查询">
        <Text className={styles.queryTitle}>当前查询</Text>
        <Text className={styles.queryLabel} size={200}>
          匹配模型 {catalog.total}
        </Text>
        <Text className={styles.total}>服务器第 {catalog.page} 页</Text>
      </div>
      {hasCurrentPageItems ? (
        <div className={styles.signals} role="group" aria-label="本页治理信号">
          <Card className={styles.signal}>
            <Text className={styles.signalLabel}>可配置模型</Text>{' '}
            <Badge color="brand">{signals.manageableModels}</Badge>
          </Card>
          <Card className={styles.signal}>
            <Text className={styles.signalLabel}>已停用</Text>{' '}
            <Badge color="warning">{signals.disabledModels}</Badge>
          </Card>
          <Card className={styles.signal}>
            <Text className={styles.signalLabel}>只读项</Text>{' '}
            <Badge color="informative">{signals.readOnlyModels}</Badge>
          </Card>
        </div>
      ) : (
        <Text className={styles.emptyState}>{catalog.total === 0 ? '暂无可见模型' : '当前页没有模型'}</Text>
      )}
    </Card>
  )
}
