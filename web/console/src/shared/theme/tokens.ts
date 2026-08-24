/**
 * 功能：定义均衡监管台账的浅色、深色语义令牌。
 * 实现：在 Fluent v9 官方主题之上收敛冷灰表面、钴蓝强调色和状态专用风险色。
 * 输入：无运行时输入。
 * 输出：两套 Fluent Theme、布局令牌与监管台账颜色令牌。
 * 依赖：Fluent UI v9 主题类型和官方 Web 主题。
 */
import { type Theme, webDarkTheme, webLightTheme } from '@fluentui/react-components'

export const ledgerLayout = {
  gridUnit: 8,
  contentMaxWidth: 1440,
  sidebarWidth: 264,
  radius: 8,
} as const

export const ledgerColors = {
  light: {
    canvas: '#f4f7fb',
    sidebar: '#f8fafc',
    surface: '#ffffff',
    text: '#172033',
    textSecondary: '#4b5870',
    border: '#d9e1ec',
    accent: '#175cd3',
    focus: '#0f62d6',
    statusHigh: '#b42318',
    statusMedium: '#b54708',
    statusLow: '#1570ef',
    statusSuccess: '#067647',
  },
  dark: {
    canvas: '#111827',
    sidebar: '#172033',
    surface: '#1d2939',
    text: '#edf2f7',
    textSecondary: '#b7c2d0',
    border: '#344054',
    accent: '#6ea8fe',
    brandBackground: '#115ea3',
    brandBackgroundHover: '#0f6cbd',
    brandBackgroundPressed: '#0f548c',
    focus: '#8ab4ff',
    statusHigh: '#fda29b',
    statusMedium: '#fec84b',
    statusLow: '#84adff',
    statusSuccess: '#75e0a7',
  },
} as const

export const ledgerLightTheme: Theme = {
  ...webLightTheme,
  colorNeutralBackground1: ledgerColors.light.surface,
  colorNeutralBackground2: ledgerColors.light.canvas,
  colorNeutralBackground3: ledgerColors.light.sidebar,
  colorNeutralForeground1: ledgerColors.light.text,
  colorNeutralForeground2: ledgerColors.light.textSecondary,
  colorNeutralStroke1: ledgerColors.light.border,
  colorBrandBackground: ledgerColors.light.accent,
  colorBrandForeground1: ledgerColors.light.accent,
  colorStrokeFocus2: ledgerColors.light.focus,
  colorPaletteRedForeground1: ledgerColors.light.statusHigh,
  colorPaletteDarkOrangeForeground1: ledgerColors.light.statusMedium,
  colorPaletteBlueForeground2: ledgerColors.light.statusLow,
  colorPaletteGreenForeground1: ledgerColors.light.statusSuccess,
}

export const ledgerDarkTheme: Theme = {
  ...webDarkTheme,
  colorNeutralBackground1: ledgerColors.dark.surface,
  colorNeutralBackground2: ledgerColors.dark.canvas,
  colorNeutralBackground3: ledgerColors.dark.sidebar,
  colorNeutralForeground1: ledgerColors.dark.text,
  colorNeutralForeground2: ledgerColors.dark.textSecondary,
  colorNeutralStroke1: ledgerColors.dark.border,
  colorBrandBackground: ledgerColors.dark.brandBackground,
  colorBrandBackgroundHover: ledgerColors.dark.brandBackgroundHover,
  colorBrandBackgroundPressed: ledgerColors.dark.brandBackgroundPressed,
  colorBrandBackgroundSelected: ledgerColors.dark.brandBackgroundHover,
  colorBrandForeground1: ledgerColors.dark.accent,
  colorStrokeFocus2: ledgerColors.dark.focus,
  colorPaletteRedForeground1: ledgerColors.dark.statusHigh,
  colorPaletteDarkOrangeForeground1: ledgerColors.dark.statusMedium,
  colorPaletteBlueForeground2: ledgerColors.dark.statusLow,
  colorPaletteGreenForeground1: ledgerColors.dark.statusSuccess,
}
