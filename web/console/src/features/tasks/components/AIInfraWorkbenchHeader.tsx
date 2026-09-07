/**
 * 功能：呈现扫描专属工作台的共用标题与操作区。
 * 实现：使用语义 h1、业务文案和调用方注入的可选操作，不改变通用页头组件。
 * 输入：标题、说明、返回路径和可选的操作节点。
 * 输出：专属工作台页头。
 * 依赖：React、Fluent UI 与共享 AI 工作台样式。
 */
import { ArrowLeftRegular } from '@fluentui/react-icons'
import { Text } from '@fluentui/react-components'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'

import { useAIInfraWorkbenchStyles } from './AIInfraWorkbench.styles'

interface AIInfraWorkbenchHeaderProps {
  action?: ReactNode
  backLink?: {
    to: string
    label: string
  }
  description?: string
  title?: string
}

export function AIInfraWorkbenchHeader({
  action,
  backLink,
  description = '集中跟踪已授权目标的扫描任务',
  title = 'AI 基础设施扫描',
}: AIInfraWorkbenchHeaderProps) {
  const styles = useAIInfraWorkbenchStyles()

  return (
    <header className={styles.header}>
      <div className={styles.headerCopy}>
        {backLink ? (
          <Link className={styles.backLink} to={backLink.to}>
            <ArrowLeftRegular aria-hidden="true" />
            <span>{backLink.label}</span>
          </Link>
        ) : null}
        <h1 className={styles.title}>{title}</h1>
        <Text className={styles.description}>{description}</Text>
      </div>
      {action ? <div className={styles.headerAction}>{action}</div> : null}
    </header>
  )
}
