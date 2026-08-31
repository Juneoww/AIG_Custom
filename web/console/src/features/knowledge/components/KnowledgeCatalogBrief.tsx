/**
 * 功能：展示知识库目录的安全范围、当前显示状态和治理权限边界。
 * 实现：仅用调用方传入的判别式数字范围与权限标记渲染，不请求数据或读取路由、会话状态。
 * 输入：分页目录或完整目录的安全显示范围、资源名称与可治理标记。
 * 输出：具名目录概览区域、空态与可治理/只读查看的可访问语义。
 * 依赖：React 与 Fluent UI v9。
 */
import { Badge, Card, Text, makeStyles, tokens } from '@fluentui/react-components'

export type PaginatedKnowledgeCatalogScope = {
  kind: 'paginated'
  total: number
  page: number
  visibleItems: number
}

export type CompleteKnowledgeCatalogScope = {
  kind: 'complete'
  total: number
  visibleItems: number
}

export type KnowledgeCatalogScope = PaginatedKnowledgeCatalogScope | CompleteKnowledgeCatalogScope

export interface KnowledgeCatalogBriefProps {
  resourceLabel: string
  scope: KnowledgeCatalogScope
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
    lineHeight: tokens.lineHeightBase500,
    fontWeight: tokens.fontWeightSemibold,
  },
  scope: {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: tokens.spacingHorizontalS,
  },
  scopeTitle: {
    color: tokens.colorNeutralForeground2,
    fontWeight: tokens.fontWeightSemibold,
  },
  scopeLabel: {
    padding: `${tokens.spacingVerticalXXS} ${tokens.spacingHorizontalS}`,
    border: `1px solid ${tokens.colorNeutralStroke2}`,
    borderRadius: tokens.borderRadiusCircular,
    backgroundColor: tokens.colorNeutralBackground1,
    color: tokens.colorNeutralForeground2,
  },
  scopeValue: {
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
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: tokens.spacingHorizontalS,
  },
  boundaryCopy: {
    color: tokens.colorNeutralForeground2,
  },
  empty: {
    color: tokens.colorNeutralForeground2,
  },
})

export function KnowledgeCatalogBrief({
  resourceLabel,
  scope,
  canManage,
}: KnowledgeCatalogBriefProps) {
  const styles = useStyles()
  const noMatchedAssets = scope.total === 0
  const emptyServerPage = scope.kind === 'paginated' && scope.total > 0 && scope.visibleItems === 0
  const governanceLabel = canManage ? '可治理' : '只读查看'
  const governanceCopy = canManage
    ? '管理员可创建、编辑和删除当前类别资产。'
    : '当前角色可查看目录与原文，不可修改资产。'

  return (
    <Card className={styles.root} role="region" aria-label="资产目录概览">
      <Text as="h2" className={styles.heading}>
        资产目录概览
      </Text>
      <div className={styles.scope} role="group" aria-label="当前资源范围">
        <Text className={styles.scopeTitle}>当前资源范围</Text>
        <Text className={styles.scopeLabel} size={200}>
          {resourceLabel}
        </Text>
        {scope.kind === 'paginated' ? (
          <>
            <Text className={styles.scopeValue}>匹配资源 {scope.total}</Text>
            <Text className={styles.scopeValue}>服务器第 {scope.page} 页</Text>
          </>
        ) : (
          <Text className={styles.scopeValue}>当前目录 {scope.total} 项</Text>
        )}
      </div>
      {noMatchedAssets ? (
        <Text className={styles.empty} role="status">暂无{resourceLabel}</Text>
      ) : emptyServerPage ? (
        <Text className={styles.empty} role="status">当前页没有{resourceLabel}</Text>
      ) : (
        <div className={styles.signals} role="group" aria-label="当前显示情况">
          <Card className={styles.signal}>
            <Text className={styles.signalLabel}>
              {scope.kind === 'paginated' ? `本页资产 ${scope.visibleItems}` : `当前显示 ${scope.visibleItems} 项`}
            </Text>
          </Card>
          <Card className={styles.signal}>
            <div className={styles.boundary} role="group" aria-label="治理边界">
              <Text className={styles.signalLabel}>当前权限</Text>
              <Badge color={canManage ? 'brand' : 'informative'}>{governanceLabel}</Badge>
              <Text className={styles.boundaryCopy}>{governanceCopy}</Text>
            </div>
          </Card>
        </div>
      )}
      {noMatchedAssets || emptyServerPage ? (
        <div className={styles.boundary} role="group" aria-label="治理边界">
          <Text className={styles.signalLabel}>当前权限</Text>
          <Badge color={canManage ? 'brand' : 'informative'}>{governanceLabel}</Badge>
          <Text className={styles.boundaryCopy}>{governanceCopy}</Text>
        </div>
      ) : null}
    </Card>
  )
}
