/**
 * 功能：展示当前 Cookie 会话中的最小个人身份资料。
 * 实现：只读取 Session Subject 白名单字段，并将密码变更导向既有站内流程。
 * 输入：已认证会话中的 id、用户名、角色与强制改密标记。
 * 输出：个人资料定义列表和修改密码站内链接。
 * 依赖：React Router、Session、PageHeader 与 Fluent UI 主题令牌。
 */
import { Link } from 'react-router-dom'
import { makeStyles, tokens } from '@fluentui/react-components'

import { useSession } from '../auth/session'
import { PageHeader } from '../../shared/components/PageHeader'

const roleLabels = { admin: '管理员', auditor: '安全审计员', user: '普通用户' } as const

const useStyles = makeStyles({
  details: { display: 'grid', gridTemplateColumns: 'max-content minmax(0, 1fr)', gap: tokens.spacingVerticalS, margin: 0 },
  term: { color: tokens.colorNeutralForeground2 },
  link: { color: tokens.colorBrandForegroundLink },
})

export function ProfilePage() {
  const styles = useStyles()
  const { state } = useSession()
  if (state.status !== 'authenticated') return null
  return (
    <section>
      <PageHeader title="个人资料" description="身份资料由当前受保护会话提供；密码不会显示或存储在浏览器。" />
      <dl className={styles.details}>
        <dt className={styles.term}>用户名</dt><dd>{state.subject.username}</dd>
        <dt className={styles.term}>角色</dt><dd>{roleLabels[state.subject.role]}</dd>
        <dt className={styles.term}>身份标识</dt><dd>{state.subject.id}</dd>
        <dt className={styles.term}>密码状态</dt><dd>{state.subject.must_change_password ? '需要修改密码' : '正常'}</dd>
      </dl>
      <p><Link className={styles.link} to="/change-password">修改密码</Link></p>
    </section>
  )
}
