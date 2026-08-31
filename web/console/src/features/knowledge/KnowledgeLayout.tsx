/**
 * 功能：为六类规则与知识库页面提供固定分类导航和嵌套路由出口。
 * 实现：以三组语义导航组织既有 NavLink，保留当前状态与原有路由矩阵。
 * 输入：当前知识库子路由。
 * 输出：可访问分类导航与对应页面 Outlet。
 * 依赖：React Router 与 Fluent UI 设计令牌。
 */
import { makeStyles, tokens } from '@fluentui/react-components'
import { NavLink, Outlet } from 'react-router-dom'

const categoryGroups = [
  {
    label: '扫描规则',
    items: [
      { path: '/knowledge/fingerprints', label: '指纹规则' },
      { path: '/knowledge/vulnerabilities', label: '漏洞规则' },
    ],
  },
  {
    label: '评测与扩展',
    items: [
      { path: '/knowledge/evaluations', label: '安全评测集' },
      { path: '/knowledge/mcp', label: 'MCP 插件' },
    ],
  },
  {
    label: '配置资产',
    items: [
      { path: '/knowledge/prompts', label: 'Prompt 集合' },
      { path: '/knowledge/agents', label: 'Agent 配置' },
    ],
  },
] as const

const useStyles = makeStyles({
  root: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL, minWidth: 0, maxWidth: '100%' },
  navigation: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: tokens.spacingHorizontalM,
    minWidth: 0,
    maxWidth: '100%',
    paddingBottom: tokens.spacingVerticalS,
    borderBottom: `${tokens.strokeWidthThin} solid ${tokens.colorNeutralStroke2}`,
  },
  group: {
    display: 'flex',
    flex: '1 1 12rem',
    flexDirection: 'column',
    gap: tokens.spacingVerticalXS,
    minWidth: 0,
    '@media (max-width: 960px)': { flexBasis: '100%' },
  },
  groupHeading: {
    margin: 0,
    color: tokens.colorNeutralForeground2,
    fontSize: tokens.fontSizeBase200,
    fontWeight: tokens.fontWeightSemibold,
    lineHeight: tokens.lineHeightBase200,
  },
  links: { display: 'flex', flexWrap: 'wrap', gap: tokens.spacingHorizontalXS, minWidth: 0 },
  link: { color: tokens.colorNeutralForeground2, textDecorationLine: 'none', padding: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalM}`, borderRadius: tokens.borderRadiusMedium, ':hover': { backgroundColor: tokens.colorNeutralBackground2 } },
  active: { color: tokens.colorBrandForeground1, backgroundColor: tokens.colorBrandBackground2, fontWeight: tokens.fontWeightSemibold },
})

export function KnowledgeLayout() {
  const styles = useStyles()
  return <div className={styles.root}>
    <nav className={styles.navigation} aria-label="知识库分类">
      {categoryGroups.map((group) => (
        <section className={styles.group} key={group.label} aria-label={group.label}>
          <h2 className={styles.groupHeading}>{group.label}</h2>
          <div className={styles.links}>
            {group.items.map((item) => (
              <NavLink key={item.path} to={item.path} className={({ isActive }) => `${styles.link} ${isActive ? styles.active : ''}`}>
                {item.label}
              </NavLink>
            ))}
          </div>
        </section>
      ))}
    </nav>
    <Outlet />
  </div>
}
