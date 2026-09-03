/**
 * 功能：提供角色化浅色监管侧栏、分组二级导航与可访问的临时折叠控制。
 * 实现：从统一导航元数据筛选角色项目，以路由位置驱动分组展开和精确活动状态。
 * 输入：当前用户角色与公共品牌上下文。
 * 输出：有序主导航、扫描与凭证子菜单、完整品牌名称和折叠按钮。
 * 依赖：React、React Router、Fluent UI、导航元数据与公共品牌 Provider。
 */
import { Button, makeStyles, mergeClasses, shorthands, tokens } from '@fluentui/react-components'
import { useEffect, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'

import type { SubjectRole } from '../../shared/api/types'
import { usePublicBrand } from '../../shared/brand/PublicBrandProvider'
import type { NavigationItem, SecondaryNavigationItem } from '../navigation'
import { secondaryNavigationFor, visibleNavigationFor } from '../navigation'

interface SidebarProps {
  role: SubjectRole
}

type NavigationGroupID = 'tasks' | 'credentials'

const GROUP_FOR_PRIMARY: Readonly<Partial<Record<NavigationItem['id'], NavigationGroupID>>> = {
  tasks: 'tasks',
  models: 'credentials',
}

function isPathWithin(pathname: string, rootPath: string) {
  return pathname === rootPath || pathname.startsWith(`${rootPath}/`)
}

function matchingGroupFor(pathname: string): NavigationGroupID | undefined {
  if (isPathWithin(pathname, '/tasks')) {
    return 'tasks'
  }
  if (isPathWithin(pathname, '/models') || isPathWithin(pathname, '/knowledge/agents')) {
    return 'credentials'
  }
  return undefined
}

function isSecondaryActive(item: SecondaryNavigationItem, pathname: string, search: string) {
  const [targetPathname, targetQuery] = item.path.split('?')
  if (item.id === 'ai-infra-scan') {
    return isPathWithin(pathname, targetPathname)
  }
  if (pathname !== targetPathname) {
    return false
  }
  if (!targetQuery) {
    return true
  }

  const targetSearch = new URLSearchParams(targetQuery)
  const currentSearch = new URLSearchParams(search)
  return currentSearch.get('scan') === targetSearch.get('scan')
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
  group: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalXXS,
  },
  groupHeader: {
    minWidth: 0,
    display: 'flex',
    alignItems: 'center',
    gap: tokens.spacingHorizontalXS,
  },
  collapsedGroupHeader: {
    flexDirection: 'column',
    alignItems: 'stretch',
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
  groupLink: {
    minWidth: 0,
    flexGrow: 1,
  },
  groupToggle: {
    minWidth: '32px',
    width: '32px',
    height: '32px',
    color: tokens.colorNeutralForeground3,
  },
  compactGroupToggle: {
    width: '100%',
    minWidth: 0,
    height: '24px',
  },
  groupChevron: {
    display: 'inline-block',
    fontSize: tokens.fontSizeBase200,
    lineHeight: 1,
    transitionProperty: 'transform',
    transitionDuration: tokens.durationFaster,
  },
  expandedChevron: {
    transform: 'rotate(90deg)',
  },
  submenu: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalXXS,
    marginBottom: tokens.spacingVerticalXS,
    paddingLeft: tokens.spacingHorizontalM,
  },
  secondaryLink: {
    minHeight: '34px',
    display: 'flex',
    alignItems: 'center',
    padding: `0 ${tokens.spacingHorizontalM}`,
    ...shorthands.borderLeft('2px', 'solid', tokens.colorNeutralStroke2),
    borderRadius: `0 ${tokens.borderRadiusMedium} ${tokens.borderRadiusMedium} 0`,
    color: tokens.colorNeutralForeground3,
    fontSize: tokens.fontSizeBase200,
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
  activeSecondaryLink: {
    ...shorthands.borderLeft('2px', 'solid', tokens.colorBrandStroke1),
    backgroundColor: tokens.colorBrandBackground2,
    color: tokens.colorBrandForeground1,
    fontWeight: tokens.fontWeightSemibold,
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
  const location = useLocation()
  const [collapsed, setCollapsed] = useState(false)
  const initialGroup = matchingGroupFor(location.pathname)
  const [expandedGroups, setExpandedGroups] = useState<Record<NavigationGroupID, boolean>>({
    tasks: initialGroup === 'tasks',
    credentials: initialGroup === 'credentials',
  })

  useEffect(() => {
    const matchingGroup = matchingGroupFor(location.pathname)
    if (!matchingGroup) {
      return
    }
    setExpandedGroups((current) =>
      current[matchingGroup] ? current : { ...current, [matchingGroup]: true },
    )
  }, [location.pathname])

  const isPrimaryActive = (item: NavigationItem) => {
    if (item.id === 'models') {
      return isPathWithin(location.pathname, '/models') || isPathWithin(location.pathname, '/knowledge/agents')
    }
    if (item.id === 'knowledge') {
      return isPathWithin(location.pathname, '/knowledge') && !isPathWithin(location.pathname, '/knowledge/agents')
    }
    if (item.path === '/') {
      return location.pathname === '/'
    }
    return isPathWithin(location.pathname, item.path)
  }

  const renderPrimaryLink = (item: NavigationItem, grouped = false) => {
    const active = isPrimaryActive(item)
    return (
      <Link
        aria-current={active ? 'page' : undefined}
        aria-label={collapsed ? item.label : undefined}
        className={mergeClasses(
          styles.link,
          grouped && styles.groupLink,
          active && styles.activeLink,
          collapsed && styles.compactLink,
        )}
        title={collapsed ? item.label : undefined}
        to={item.path}
      >
        {collapsed ? <span className={styles.compactGlyph} aria-hidden="true">{item.label.slice(0, 1)}</span> : item.label}
      </Link>
    )
  }

  const renderGroup = (item: NavigationItem, groupID: NavigationGroupID) => {
    const renderedExpanded = expandedGroups[groupID] && !collapsed
    const submenuID = `${groupID}-submenu`
    const children = secondaryNavigationFor(groupID, role)

    return (
      <div className={styles.group} key={item.id}>
        <div className={mergeClasses(styles.groupHeader, collapsed && styles.collapsedGroupHeader)}>
          {renderPrimaryLink(item, true)}
          <Button
            appearance="subtle"
            aria-controls={renderedExpanded ? submenuID : undefined}
            aria-expanded={renderedExpanded}
            aria-label={`${renderedExpanded ? '收起' : '展开'}${item.label}子菜单`}
            className={mergeClasses(styles.groupToggle, collapsed && styles.compactGroupToggle)}
            onClick={() => {
              if (!collapsed) {
                setExpandedGroups((current) => ({ ...current, [groupID]: !current[groupID] }))
              }
            }}
            size="small"
          >
            <span
              aria-hidden="true"
              className={mergeClasses(styles.groupChevron, renderedExpanded && styles.expandedChevron)}
            >
              ›
            </span>
          </Button>
        </div>
        {renderedExpanded ? (
          <div className={styles.submenu} id={submenuID}>
            {children.map((child) => {
              const active = isSecondaryActive(child, location.pathname, location.search)
              return (
                <Link
                  aria-current={active ? 'page' : undefined}
                  className={mergeClasses(styles.secondaryLink, active && styles.activeSecondaryLink)}
                  key={child.id}
                  to={child.path}
                >
                  {child.label}
                </Link>
              )
            })}
          </div>
        ) : null}
      </div>
    )
  }

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
        {visibleNavigationFor(role).map((item) => {
          const groupID = GROUP_FOR_PRIMARY[item.id]
          return groupID ? renderGroup(item, groupID) : <div key={item.id}>{renderPrimaryLink(item)}</div>
        })}
      </nav>

      <div className={styles.controls}>
        <Button appearance="subtle" onClick={() => setCollapsed((value) => !value)}>
          {collapsed ? '展开导航' : '收起导航'}
        </Button>
      </div>
    </aside>
  )
}
