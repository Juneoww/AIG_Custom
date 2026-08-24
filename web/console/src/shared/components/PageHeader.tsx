/**
 * 功能：提供监管台账页面统一的标题、说明和操作区。
 * 实现：使用语义 h1 与 Fluent 排版令牌构建紧凑、可响应的页头。
 * 输入：标题、可选说明和操作子节点。
 * 输出：带单一一级标题的页头区域。
 * 依赖：React 与 Fluent UI v9。
 */
import { Text, makeStyles, tokens } from '@fluentui/react-components'
import type { ReactNode } from 'react'

interface PageHeaderProps {
  title: string
  description?: string
  children?: ReactNode
}

const useStyles = makeStyles({
  root: {
    display: 'flex',
    alignItems: 'flex-start',
    justifyContent: 'space-between',
    gap: tokens.spacingHorizontalXXL,
    marginBottom: tokens.spacingVerticalXXL,
    '@media (max-width: 720px)': {
      flexDirection: 'column',
      gap: tokens.spacingVerticalL,
    },
  },
  copy: {
    minWidth: 0,
  },
  title: {
    margin: 0,
    color: tokens.colorNeutralForeground1,
    fontSize: tokens.fontSizeBase600,
    lineHeight: tokens.lineHeightBase600,
    fontWeight: tokens.fontWeightSemibold,
  },
  description: {
    display: 'block',
    maxWidth: '72ch',
    marginTop: tokens.spacingVerticalS,
    color: tokens.colorNeutralForeground2,
    lineHeight: tokens.lineHeightBase400,
  },
  actions: {
    display: 'flex',
    alignItems: 'center',
    gap: tokens.spacingHorizontalS,
    flexShrink: 0,
  },
})

export function PageHeader({ title, description, children }: PageHeaderProps) {
  const styles = useStyles()

  return (
    <header className={styles.root}>
      <div className={styles.copy}>
        <h1 className={styles.title}>{title}</h1>
        {description ? <Text className={styles.description}>{description}</Text> : null}
      </div>
      {children ? <div className={styles.actions}>{children}</div> : null}
    </header>
  )
}
