/**
 * 功能：为六类规则与知识库页面提供固定分类导航和嵌套路由出口。
 * 实现：使用语义 NavLink 保持当前状态，分类顺序与既有能力矩阵一致。
 * 输入：当前知识库子路由。
 * 输出：可访问分类导航与对应页面 Outlet。
 * 依赖：React Router 与 Fluent UI 设计令牌。
 */
import { makeStyles, tokens } from '@fluentui/react-components'
import { NavLink, Outlet } from 'react-router-dom'

const categories = [
  { path: '/knowledge/fingerprints', label: '指纹规则' },
  { path: '/knowledge/vulnerabilities', label: '漏洞规则' },
  { path: '/knowledge/evaluations', label: '安全评测集' },
  { path: '/knowledge/mcp', label: 'MCP 插件' },
  { path: '/knowledge/prompts', label: 'Prompt 集合' },
  { path: '/knowledge/agents', label: 'Agent 配置' },
] as const

const useStyles = makeStyles({
  root: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL, minWidth: 0 },
  navigation: { display: 'flex', flexWrap: 'wrap', gap: tokens.spacingHorizontalXS, paddingBottom: tokens.spacingVerticalS, borderBottom: `${tokens.strokeWidthThin} solid ${tokens.colorNeutralStroke2}` },
  link: { color: tokens.colorNeutralForeground2, textDecorationLine: 'none', padding: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalM}`, borderRadius: tokens.borderRadiusMedium, ':hover': { backgroundColor: tokens.colorNeutralBackground2 } },
  active: { color: tokens.colorBrandForeground1, backgroundColor: tokens.colorBrandBackground2, fontWeight: tokens.fontWeightSemibold },
})

export function KnowledgeLayout() {
  const styles = useStyles()
  return <div className={styles.root}>
    <nav className={styles.navigation} aria-label="知识库分类">
      {categories.map((item) => <NavLink key={item.path} to={item.path} className={({ isActive }) => `${styles.link} ${isActive ? styles.active : ''}`}>{item.label}</NavLink>)}
    </nav>
    <Outlet />
  </div>
}
