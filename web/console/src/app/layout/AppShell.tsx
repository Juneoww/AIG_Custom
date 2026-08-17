/**
 * 功能：组合角色侧栏、身份顶栏和当前业务页面的生产应用壳。
 * 实现：从认证会话读取角色，以响应式弹性布局承载 Outlet。
 * 输入：已通过 RequireAuthenticated 的会话与当前子路由。
 * 输出：浅色监管台账式导航壳和页面主区域。
 * 依赖：React Router、Fluent UI、Session、Sidebar 与 Topbar。
 */
import { makeStyles, tokens } from '@fluentui/react-components'
import { Outlet } from 'react-router-dom'

import { useSession } from '../../features/auth/session'
import { Sidebar } from './Sidebar'
import { Topbar } from './Topbar'

const useStyles = makeStyles({
  shell: {
    minHeight: '100dvh',
    display: 'flex',
    backgroundColor: tokens.colorNeutralBackground2,
    color: tokens.colorNeutralForeground1,
    '@media (max-width: 760px)': {
      flexDirection: 'column',
    },
  },
  workspace: {
    minWidth: 0,
    flexGrow: 1,
    display: 'flex',
    flexDirection: 'column',
  },
  content: {
    minWidth: 0,
    flexGrow: 1,
    overflowX: 'auto',
    padding: '32px',
    '@media (max-width: 760px)': {
      padding: '24px 16px',
    },
  },
})

export function AppShell() {
  const styles = useStyles()
  const { state } = useSession()
  if (state.status !== 'authenticated') return null

  return (
    <div className={styles.shell}>
      <Sidebar role={state.subject.role} />
      <div className={styles.workspace}>
        <Topbar />
        <main className={styles.content}>
          <Outlet />
        </main>
      </div>
    </div>
  )
}
