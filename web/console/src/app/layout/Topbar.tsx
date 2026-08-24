/**
 * 功能：提供产品名、主题、当前身份、主动改密和安全退出的控制台顶栏。
 * 实现：消费公共品牌、主题与会话上下文，失败退出保留身份并展示固定提示。
 * 输入：当前路由、认证 Subject 与用户主题选择。
 * 输出：响应式顶栏、改密站内来源和退出操作反馈。
 * 依赖：React、React Router、Fluent UI、公共品牌、主题和 Session 上下文。
 */
import {
  Button,
  MessageBar,
  MessageBarBody,
  Select,
  makeStyles,
  shorthands,
  tokens,
} from '@fluentui/react-components'
import { useState } from 'react'
import { Link, useLocation } from 'react-router-dom'

import { useSession } from '../../features/auth/session'
import type { SubjectRole } from '../../shared/api/types'
import { usePublicBrand } from '../../shared/brand/PublicBrandProvider'
import { useThemeMode } from '../../shared/theme/ThemeProvider'
import type { ThemeMode } from '../../shared/theme/theme-storage'

const roleLabels: Record<SubjectRole, string> = {
  admin: '管理员',
  auditor: '安全审计员',
  user: '普通用户',
}

const themeModes: readonly ThemeMode[] = ['light', 'dark', 'system']

const useStyles = makeStyles({
  root: {
    minHeight: '64px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: tokens.spacingHorizontalL,
    padding: `8px ${tokens.spacingHorizontalXXL}`,
    ...shorthands.borderBottom('1px', 'solid', tokens.colorNeutralStroke2),
    backgroundColor: tokens.colorNeutralBackground1,
    '@media (max-width: 900px)': {
      flexWrap: 'wrap',
      padding: tokens.spacingVerticalM,
    },
  },
  product: {
    minWidth: 0,
    maxWidth: '320px',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    fontWeight: tokens.fontWeightSemibold,
  },
  controls: {
    minWidth: 0,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'flex-end',
    flexWrap: 'wrap',
    gap: tokens.spacingHorizontalM,
  },
  theme: {
    minWidth: '112px',
  },
  identity: {
    display: 'flex',
    alignItems: 'center',
    gap: tokens.spacingHorizontalS,
    color: tokens.colorNeutralForeground2,
  },
  username: {
    color: tokens.colorNeutralForeground1,
    fontWeight: tokens.fontWeightSemibold,
  },
  error: {
    flexBasis: '100%',
  },
  actionLink: {
    color: tokens.colorBrandForegroundLink,
    textDecorationLine: 'none',
    ':hover': {
      color: tokens.colorBrandForegroundLinkHover,
      textDecorationLine: 'underline',
    },
  },
})

export function Topbar() {
  const styles = useStyles()
  const location = useLocation()
  const { productName } = usePublicBrand()
  const { mode, setMode } = useThemeMode()
  const { logout, state } = useSession()
  const [loggingOut, setLoggingOut] = useState(false)
  const [logoutError, setLogoutError] = useState('')

  if (state.status !== 'authenticated') return null

  const currentLocation = `${location.pathname}${location.search}${location.hash}`

  async function handleLogout() {
    if (loggingOut) return
    setLoggingOut(true)
    setLogoutError('')
    try {
      await logout()
    } catch {
      setLogoutError('退出失败，请稍后重试。')
    } finally {
      setLoggingOut(false)
    }
  }

  return (
    <header className={styles.root}>
      <span aria-label={productName} className={styles.product} title={productName}>{productName}</span>
      <div className={styles.controls}>
        <Select
          aria-label="主题模式"
          className={styles.theme}
          value={mode}
          onChange={(event) => {
            const nextMode = event.target.value as ThemeMode
            if (themeModes.includes(nextMode)) setMode(nextMode)
          }}
        >
          <option value="light">浅色</option>
          <option value="dark">深色</option>
          <option value="system">跟随系统</option>
        </Select>
        <div className={styles.identity} aria-label="个人中心">
          <Link className={styles.actionLink} to="/profile">个人资料</Link>
          <span className={styles.username}>{state.subject.username}</span>
          <span>{roleLabels[state.subject.role]}</span>
        </div>
        <Link className={styles.actionLink} to="/change-password" state={{ from: currentLocation }}>
          修改密码
        </Link>
        <Link className={styles.actionLink} to="/about">关于</Link>
        <Button appearance="subtle" disabled={loggingOut} onClick={() => void handleLogout()}>
          {loggingOut ? '正在退出' : '退出登录'}
        </Button>
        {logoutError ? (
          <MessageBar className={styles.error} intent="error">
            <MessageBarBody>{logoutError}</MessageBarBody>
          </MessageBar>
        ) : null}
      </div>
    </header>
  )
}
