/**
 * 功能：提供企业安全治理控制台的浅色产品壳。
 * 实现：使用 FluentProvider、语义化布局与官方设计令牌建立侧栏和工作区基线。
 * 输入：无组件属性。
 * 输出：中文控制台根级 React 视图。
 * 依赖：Fluent UI v9 与 React JSX 运行时。
 */
import {
  Badge,
  FluentProvider,
  Text,
  makeStyles,
  shorthands,
  tokens,
  webLightTheme,
} from '@fluentui/react-components'

const useStyles = makeStyles({
  provider: {
    minHeight: '100dvh',
    backgroundColor: tokens.colorNeutralBackground2,
  },
  shell: {
    minHeight: '100dvh',
    display: 'grid',
    gridTemplateColumns: '264px minmax(0, 1fr)',
    backgroundColor: tokens.colorNeutralBackground2,
    color: tokens.colorNeutralForeground1,
    '@media (max-width: 760px)': {
      gridTemplateColumns: '1fr',
    },
  },
  sidebar: {
    display: 'flex',
    flexDirection: 'column',
    minWidth: 0,
    padding: '24px 20px',
    ...shorthands.borderRight('1px', 'solid', tokens.colorNeutralStroke2),
    backgroundColor: tokens.colorNeutralBackground1,
    '@media (max-width: 760px)': {
      ...shorthands.borderRight('0'),
      ...shorthands.borderBottom('1px', 'solid', tokens.colorNeutralStroke2),
    },
  },
  brand: {
    display: 'flex',
    alignItems: 'center',
    columnGap: tokens.spacingHorizontalM,
    minWidth: 0,
  },
  brandMark: {
    width: '40px',
    height: '40px',
    flexShrink: 0,
    display: 'grid',
    placeItems: 'center',
    borderRadius: tokens.borderRadiusMedium,
    backgroundColor: tokens.colorBrandBackground,
    color: tokens.colorNeutralForegroundOnBrand,
    fontSize: tokens.fontSizeBase400,
    fontWeight: tokens.fontWeightSemibold,
  },
  brandCopy: {
    minWidth: 0,
    display: 'flex',
    flexDirection: 'column',
    rowGap: '2px',
  },
  productName: {
    fontSize: tokens.fontSizeBase400,
    lineHeight: tokens.lineHeightBase400,
    fontWeight: tokens.fontWeightSemibold,
  },
  productType: {
    color: tokens.colorNeutralForeground3,
  },
  navigation: {
    marginTop: '36px',
  },
  navigationLabel: {
    display: 'block',
    marginBottom: tokens.spacingVerticalS,
    color: tokens.colorNeutralForeground3,
  },
  currentItem: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    columnGap: tokens.spacingHorizontalS,
    minHeight: '40px',
    padding: `0 ${tokens.spacingHorizontalM}`,
    borderRadius: tokens.borderRadiusMedium,
    backgroundColor: tokens.colorBrandBackground2,
    color: tokens.colorBrandForeground1,
    fontWeight: tokens.fontWeightSemibold,
  },
  sidebarNote: {
    marginTop: 'auto',
    paddingTop: '28px',
    color: tokens.colorNeutralForeground3,
    lineHeight: tokens.lineHeightBase300,
    '@media (max-width: 760px)': {
      marginTop: tokens.spacingVerticalL,
      paddingTop: 0,
    },
  },
  workspace: {
    minWidth: 0,
    display: 'flex',
    flexDirection: 'column',
  },
  topbar: {
    minHeight: '64px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    columnGap: tokens.spacingHorizontalM,
    padding: `0 ${tokens.spacingHorizontalXXL}`,
    ...shorthands.borderBottom('1px', 'solid', tokens.colorNeutralStroke2),
    backgroundColor: tokens.colorNeutralBackground1,
    '@media (max-width: 760px)': {
      padding: `0 ${tokens.spacingHorizontalL}`,
    },
  },
  content: {
    flexGrow: 1,
    padding: '56px 48px',
    '@media (max-width: 760px)': {
      padding: '36px 20px',
    },
  },
  contentInner: {
    width: '100%',
    maxWidth: '880px',
  },
  stageLabel: {
    color: tokens.colorBrandForeground1,
    fontWeight: tokens.fontWeightSemibold,
  },
  heading: {
    display: 'block',
    marginTop: tokens.spacingVerticalS,
    fontSize: tokens.fontSizeHero800,
    lineHeight: tokens.lineHeightHero800,
    fontWeight: tokens.fontWeightSemibold,
    letterSpacing: '-0.025em',
    '@media (max-width: 760px)': {
      fontSize: tokens.fontSizeBase600,
      lineHeight: tokens.lineHeightBase600,
    },
  },
  description: {
    display: 'block',
    maxWidth: '600px',
    marginTop: tokens.spacingVerticalL,
    color: tokens.colorNeutralForeground2,
    fontSize: tokens.fontSizeBase400,
    lineHeight: tokens.lineHeightBase500,
  },
  statusPanel: {
    maxWidth: '600px',
    marginTop: '40px',
    paddingTop: tokens.spacingVerticalL,
    ...shorthands.borderTop('1px', 'solid', tokens.colorNeutralStroke1),
    display: 'grid',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
    columnGap: tokens.spacingHorizontalXXL,
    rowGap: tokens.spacingVerticalL,
    '@media (max-width: 520px)': {
      gridTemplateColumns: '1fr',
    },
  },
  statusItem: {
    display: 'flex',
    flexDirection: 'column',
    rowGap: tokens.spacingVerticalXS,
  },
  statusTerm: {
    color: tokens.colorNeutralForeground3,
  },
})

export function App() {
  const styles = useStyles()

  return (
    <FluentProvider className={styles.provider} theme={webLightTheme}>
      <div className={styles.shell}>
        <aside className={styles.sidebar} aria-label="平台导航">
          <div className={styles.brand}>
            <span className={styles.brandMark} aria-hidden="true">
              安
            </span>
            <div className={styles.brandCopy}>
              <Text className={styles.productName}>AI 安全治理平台</Text>
              <Text className={styles.productType} size={200}>
                企业控制台
              </Text>
            </div>
          </div>

          <nav className={styles.navigation} aria-label="主导航">
            <Text className={styles.navigationLabel} size={200}>
              治理工作区
            </Text>
            <div className={styles.currentItem} aria-current="page">
              <span>工作台</span>
              <Badge appearance="tint" color="brand" size="small">
                构建中
              </Badge>
            </div>
          </nav>

          <Text className={styles.sidebarNote} size={200}>
            后续能力将逐步接入平台真实服务。
          </Text>
        </aside>

        <div className={styles.workspace}>
          <header className={styles.topbar}>
            <Text weight="semibold">工作台</Text>
            <Badge appearance="outline" color="informative">
              工程初始化
            </Badge>
          </header>

          <main className={styles.content}>
            <section className={styles.contentInner} aria-labelledby="console-heading">
              <Text className={styles.stageLabel} size={200}>
                控制台基础工程
              </Text>
              <Text as="h1" className={styles.heading} id="console-heading">
                工作台正在构建
              </Text>
              <Text className={styles.description}>
                当前已建立可复现的前端开发与测试环境。业务页面将在后续阶段连接平台真实接口。
              </Text>

              <div className={styles.statusPanel} aria-label="建设状态">
                <div className={styles.statusItem}>
                  <Text className={styles.statusTerm} size={200}>
                    当前阶段
                  </Text>
                  <Text weight="semibold">工程骨架</Text>
                </div>
                <div className={styles.statusItem}>
                  <Text className={styles.statusTerm} size={200}>
                    数据来源
                  </Text>
                  <Text weight="semibold">尚未接入</Text>
                </div>
              </div>
            </section>
          </main>
        </div>
      </div>
    </FluentProvider>
  )
}
