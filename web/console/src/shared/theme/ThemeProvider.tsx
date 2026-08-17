/**
 * 功能：向企业控制台提供浅色、深色和跟随系统三模式主题。
 * 实现：用 React Context 管理偏好，实时监听系统配色并向 FluentProvider 注入主题。
 * 输入：子组件、可选初始模式和浏览器媒体查询及主题存储。
 * 输出：Fluent 主题上下文、当前偏好、解析主题和切换函数。
 * 依赖：React、Fluent UI v9、theme-storage 与本地主题令牌。
 */
import { FluentProvider } from '@fluentui/react-components'
import { createContext, type ReactNode, useContext, useEffect, useMemo, useState } from 'react'

import { readThemeMode, type ThemeMode, writeThemeMode } from './theme-storage'
import { ledgerDarkTheme, ledgerLightTheme } from './tokens'

export type ResolvedTheme = 'light' | 'dark'

interface ThemeContextValue {
  mode: ThemeMode
  resolvedTheme: ResolvedTheme
  setMode: (mode: ThemeMode) => void
}

interface ThemeProviderProps {
  children: ReactNode
  initialMode?: ThemeMode
}

const ThemeContext = createContext<ThemeContextValue | undefined>(undefined)
const systemDarkQuery = '(prefers-color-scheme: dark)'

function browserStorage(): Storage | undefined {
  if (typeof window === 'undefined') {
    return undefined
  }

  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

function systemMediaQuery(): MediaQueryList | undefined {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return undefined
  }

  try {
    return window.matchMedia(systemDarkQuery)
  } catch {
    return undefined
  }
}

function systemPrefersDark(): boolean {
  return systemMediaQuery()?.matches ?? false
}

export function ThemeProvider({ children, initialMode }: ThemeProviderProps) {
  const [mode, setModeState] = useState<ThemeMode>(() => initialMode ?? readThemeMode(browserStorage()))
  const [systemDark, setSystemDark] = useState(systemPrefersDark)
  const resolvedTheme: ResolvedTheme = mode === 'system' ? (systemDark ? 'dark' : 'light') : mode

  useEffect(() => {
    if (mode !== 'system') {
      return undefined
    }

    const media = systemMediaQuery()
    if (!media) {
      return undefined
    }

    const handleChange = (event: MediaQueryListEvent) => setSystemDark(event.matches)
    try {
      setSystemDark(media.matches)
      media.addEventListener('change', handleChange)
    } catch {
      try {
        media.removeEventListener('change', handleChange)
      } catch {
        // 受限浏览器可能同时拒绝注册和移除监听；主题保持安全的当前解析结果。
      }
      return undefined
    }

    return () => {
      try {
        media.removeEventListener('change', handleChange)
      } catch {
        // 卸载阶段不得让媒体查询权限异常破坏宿主应用。
      }
    }
  }, [mode])

  useEffect(() => {
    const root = document.documentElement
    const previousTheme = root.getAttribute('data-theme')
    root.setAttribute('data-theme', resolvedTheme)

    return () => {
      if (previousTheme === null) {
        root.removeAttribute('data-theme')
      } else {
        root.setAttribute('data-theme', previousTheme)
      }
    }
  }, [resolvedTheme])

  const value = useMemo<ThemeContextValue>(
    () => ({
      mode,
      resolvedTheme,
      setMode(nextMode) {
        setModeState(nextMode)
        writeThemeMode(browserStorage(), nextMode)
      },
    }),
    [mode, resolvedTheme],
  )

  return (
    <ThemeContext.Provider value={value}>
      <FluentProvider theme={resolvedTheme === 'dark' ? ledgerDarkTheme : ledgerLightTheme}>
        {children}
      </FluentProvider>
    </ThemeContext.Provider>
  )
}

export function useThemeMode(): ThemeContextValue {
  const context = useContext(ThemeContext)
  if (!context) {
    throw new Error('主题上下文不可用')
  }
  return context
}
