/**
 * 功能：提供角色化浅色监管侧栏与可访问的临时折叠控制。
 * 实现：从统一导航元数据筛选角色项目，以 NavLink 呈现当前页且不持久化布局。
 * 输入：当前用户角色与公共品牌上下文。
 * 输出：有序主导航、完整品牌名称和折叠按钮。
 * 依赖：React、React Router、Fluent UI、导航元数据与公共品牌 Provider。
 */
import { Button, makeStyles, mergeClasses, shorthands, tokens } from '@fluentui/react-components'
import { useState } from 'react'
import { NavLink } from 'react-router-dom'

import type { SubjectRole } from '../../shared/api/types'
import { usePublicBrand } from '../../shared/brand/PublicBrandProvider'
import { visibleNavigationFor } from '../navigation'

interface SidebarProps {
  role: SubjectRole
}

const useStyles = makeStyles({
  root: {
    width: '256px',
    minWidth: '256px',
    minHeight: '100dvh',
    display: 'flex',
    flexDirection: 'column',
    padding: '24px 16px',
    ...shorthands.borderRight('1px', 'solid', tokens.colorNeutralStroke2),
    backgroundColor: tokens.colorNeutralBackground3,
    transitionProperty: 'width, min-width',
    transitionDuration: tokens.durationNormal,
    '@media (max-width: 760px)': {
      width: '100%',
      minWidth: 0,
      minHeight: 'auto',
      ...shorthands.borderRight('0'),
      ...shorthands.borderBottom('1px', 'solid', tokens.colorNeutralStroke2),
    },
  },
  collapsed: {
    width: '80px',
    minWidth: '80px',
    '@media (max-width: 760px)': {
      width: '100%',
    },
  },
  brand: {
    minWidth: 0,
    display: 'flex',
    alignItems: 'center',
    gap: tokens.spacingHorizontalM,
    marginBottom: tokens.spacingVerticalXXL,
  },
  brandMark: {
    width: '40px',
    height: '40px',
    flexShrink: 0,
    display: 'grid',
    placeItems: 'center',
    overflow: 'hidden',
    borderRadius: tokens.borderRadiusMedium,
    backgroundColor: tokens.colorBrandBackground,
    color: tokens.colorNeutralForegroundOnBrand,
    fontWeight: tokens.fontWeightSemibold,
  },
  logo: {
    width: '100%',
    height: '100%',
    objectFit: 'contain',
  },
  productName: {
    minWidth: 0,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    fontWeight: tokens.fontWeightSemibold,
  },
  hidden: {
    display: 'none',
  },
  nav: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalXS,
  },
  link: {
    minHeight: '40px',
    display: 'flex',
    alignItems: 'center',
    gap: tokens.spacingHorizontalM,
    padding: `0 ${tokens.spacingHorizontalM}`,
    borderRadius: tokens.borderRadiusMedium,
    color: tokens.colorNeutralForeground2,
    textDecorationLine: 'none',
    ':hover': {
      backgroundColor: tokens.colorNeutralBackground1Hover,
      color: tokens.colorNeutralForeground1,
    },
    ':focus-visible': {
      outlineColor: tokens.colorStrokeFocus2,
      outlineStyle: 'solid',
      outlineWidth: '2px',
      outlineOffset: '2px',
    },
  },
  activeLink: {
    backgroundColor: tokens.colorBrandBackground2,
    color: tokens.colorBrandForeground1,
    fontWeight: tokens.fontWeightSemibold,
  },
  compactLink: {
    justifyContent: 'center',
    padding: 0,
  },
  compactGlyph: {
    width: '24px',
    textAlign: 'center',
  },
  controls: {
    marginTop: 'auto',
    paddingTop: tokens.spacingVerticalXXL,
  },
})

export function Sidebar({ role }: SidebarProps) {
  const styles = useStyles()
  const { productName, logoDataURL } = usePublicBrand()
  const [collapsed, setCollapsed] = useState(false)

  return (
    <aside className={mergeClasses(styles.root, collapsed && styles.collapsed)} aria-label="平台导航">
      <div className={styles.brand}>
        <span className={styles.brandMark} aria-hidden="true">
          {logoDataURL ? <img alt="" className={styles.logo} src={logoDataURL} /> : '安'}
        </span>
        <span
          aria-label={productName}
          className={mergeClasses(styles.productName, collapsed && styles.hidden)}
          title={productName}
        >
          {productName}
        </span>
      </div>

      <nav className={styles.nav} aria-label="主导航">
        {visibleNavigationFor(role).map((item) => (
          <NavLink
            aria-label={collapsed ? item.label : undefined}
            className={({ isActive }) =>
              mergeClasses(styles.link, isActive && styles.activeLink, collapsed && styles.compactLink)
            }
            end={item.path === '/'}
            key={item.id}
            title={collapsed ? item.label : undefined}
            to={item.path}
          >
            {collapsed ? <span className={styles.compactGlyph} aria-hidden="true">{item.label.slice(0, 1)}</span> : item.label}
          </NavLink>
        ))}
      </nav>

      <div className={styles.controls}>
        <Button appearance="subtle" onClick={() => setCollapsed((value) => !value)}>
          {collapsed ? '展开导航' : '收起导航'}
        </Button>
      </div>
    </aside>
  )
}
