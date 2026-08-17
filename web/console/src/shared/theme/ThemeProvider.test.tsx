/**
 * 功能：验证企业控制台三模式主题与安全存储边界。
 * 实现：模拟系统配色媒体查询，渲染真实主题 Provider 并触发模式切换。
 * 输入：localStorage 中的主题偏好及系统深浅色变化。
 * 输出：解析后的主题、持久化键和 Fluent 根节点状态断言。
 * 依赖：Vitest、Testing Library、React 与 Fluent UI v9。
 */
import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ThemeProvider, useThemeMode } from './ThemeProvider'
import { readThemeMode, THEME_STORAGE_KEY, writeThemeMode } from './theme-storage'
import { ledgerColors, ledgerDarkTheme, ledgerLayout } from './tokens'

type MediaListener = (event: MediaQueryListEvent) => void

function installMatchMedia(initialDark: boolean) {
  let matches = initialDark
  const listeners = new Set<MediaListener>()
  const media = {
    get matches() {
      return matches
    },
    media: '(prefers-color-scheme: dark)',
    onchange: null,
    addEventListener: vi.fn((_type: string, listener: MediaListener) => {
      listeners.add(listener)
    }),
    removeEventListener: vi.fn((_type: string, listener: MediaListener) => {
      listeners.delete(listener)
    }),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  } as unknown as MediaQueryList

  vi.stubGlobal('matchMedia', vi.fn(() => media))

  return {
    setDark(nextDark: boolean) {
      matches = nextDark
      act(() => {
        listeners.forEach((listener) =>
          listener({ matches: nextDark, media: media.media } as MediaQueryListEvent),
        )
      })
    },
  }
}

function ThemeProbe() {
  const { mode, resolvedTheme, setMode } = useThemeMode()

  return (
    <div>
      <output aria-label="主题模式">{mode}</output>
      <output aria-label="生效主题">{resolvedTheme}</output>
      <button type="button" onClick={() => setMode('light')}>
        浅色
      </button>
      <button type="button" onClick={() => setMode('dark')}>
        深色
      </button>
      <button type="button" onClick={() => setMode('system')}>
        跟随系统
      </button>
    </div>
  )
}

describe('ThemeProvider', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    document.documentElement.removeAttribute('data-theme')
  })

  it('defaults to light and does not persist unrelated business data', () => {
    installMatchMedia(true)
    const setItem = vi.spyOn(Storage.prototype, 'setItem')

    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    )

    expect(screen.getByLabelText('主题模式')).toHaveTextContent('light')
    expect(screen.getByLabelText('生效主题')).toHaveTextContent('light')
    expect(setItem.mock.calls.every(([key]) => key === THEME_STORAGE_KEY)).toBe(true)
  })

  it('restores only a valid stored preference and falls back to light for invalid data', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'unexpected')
    localStorage.setItem('business-session', 'must-remain-unread')
    const getItem = vi.spyOn(Storage.prototype, 'getItem')
    installMatchMedia(true)

    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    )

    expect(screen.getByLabelText('主题模式')).toHaveTextContent('light')
    expect(getItem).toHaveBeenCalledTimes(1)
    expect(getItem).toHaveBeenCalledWith(THEME_STORAGE_KEY)
  })

  it('resolves system mode and responds to live operating-system changes', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'system')
    const media = installMatchMedia(false)

    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    )

    expect(screen.getByLabelText('生效主题')).toHaveTextContent('light')
    media.setDark(true)
    expect(screen.getByLabelText('生效主题')).toHaveTextContent('dark')
    expect(document.documentElement).toHaveAttribute('data-theme', 'dark')
  })

  it('lets an explicit choice override subsequent system changes', () => {
    const media = installMatchMedia(false)

    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '深色' }))
    media.setDark(false)

    expect(screen.getByLabelText('主题模式')).toHaveTextContent('dark')
    expect(screen.getByLabelText('生效主题')).toHaveTextContent('dark')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('dark')
  })

  it('survives a browser that rejects access to the localStorage property', () => {
    installMatchMedia(false)
    const descriptor = Object.getOwnPropertyDescriptor(window, 'localStorage')
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      get() {
        throw new DOMException('blocked', 'SecurityError')
      },
    })

    try {
      expect(() =>
        render(
          <ThemeProvider>
            <ThemeProbe />
          </ThemeProvider>,
        ),
      ).not.toThrow()
      fireEvent.click(screen.getByRole('button', { name: '深色' }))
      expect(screen.getByLabelText('生效主题')).toHaveTextContent('dark')
    } finally {
      if (descriptor) {
        Object.defineProperty(window, 'localStorage', descriptor)
      }
    }
  })

  it('falls back safely when Storage methods reject reads or writes', () => {
    const rejectingStorage = {
      getItem() {
        throw new DOMException('blocked', 'SecurityError')
      },
      setItem() {
        throw new DOMException('blocked', 'SecurityError')
      },
    } as unknown as Storage

    expect(readThemeMode(rejectingStorage)).toBe('light')
    expect(() => writeThemeMode(rejectingStorage, 'dark')).not.toThrow()
  })

  it('falls back to light when matchMedia rejects access', () => {
    vi.stubGlobal('matchMedia', vi.fn(() => {
      throw new DOMException('blocked', 'SecurityError')
    }))

    expect(() =>
      render(
        <ThemeProvider initialMode="system">
          <ThemeProbe />
        </ThemeProvider>,
      ),
    ).not.toThrow()
    expect(screen.getByLabelText('生效主题')).toHaveTextContent('light')
  })

  it('does not crash when media listener registration or removal fails', () => {
    const rejectingAdd = {
      matches: false,
      media: '(prefers-color-scheme: dark)',
      addEventListener() {
        throw new DOMException('blocked', 'SecurityError')
      },
      removeEventListener: vi.fn(),
    } as unknown as MediaQueryList
    vi.stubGlobal('matchMedia', vi.fn(() => rejectingAdd))

    expect(() =>
      render(
        <ThemeProvider initialMode="system">
          <ThemeProbe />
        </ThemeProvider>,
      ),
    ).not.toThrow()

    vi.unstubAllGlobals()
    const rejectingRemove = {
      matches: false,
      media: '(prefers-color-scheme: dark)',
      addEventListener: vi.fn(),
      removeEventListener() {
        throw new DOMException('blocked', 'SecurityError')
      },
    } as unknown as MediaQueryList
    vi.stubGlobal('matchMedia', vi.fn(() => rejectingRemove))
    const mounted = render(
      <ThemeProvider initialMode="system">
        <ThemeProbe />
      </ThemeProvider>,
    )

    expect(() => mounted.unmount()).not.toThrow()
  })

  it('restores the host page theme attribute when unmounted', () => {
    document.documentElement.setAttribute('data-theme', 'host-theme')
    installMatchMedia(false)
    const mounted = render(
      <ThemeProvider initialMode="dark">
        <ThemeProbe />
      </ThemeProvider>,
    )

    expect(document.documentElement).toHaveAttribute('data-theme', 'dark')
    mounted.unmount()
    expect(document.documentElement).toHaveAttribute('data-theme', 'host-theme')
  })
})

function relativeLuminance(hex: string): number {
  const channels = hex
    .slice(1)
    .match(/.{2}/g)!
    .map((channel) => Number.parseInt(channel, 16) / 255)
    .map((channel) => (channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4))
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

function contrastRatio(foreground: string, background: string): number {
  const lighter = Math.max(relativeLuminance(foreground), relativeLuminance(background))
  const darker = Math.min(relativeLuminance(foreground), relativeLuminance(background))
  return (lighter + 0.05) / (darker + 0.05)
}

describe('regulatory ledger tokens', () => {
  it('keeps the layout on an 8px grid and body text above WCAG AA in both themes', () => {
    expect(ledgerLayout.gridUnit).toBe(8)
    expect(contrastRatio(ledgerColors.light.text, ledgerColors.light.canvas)).toBeGreaterThanOrEqual(4.5)
    expect(contrastRatio(ledgerColors.dark.text, ledgerColors.dark.canvas)).toBeGreaterThanOrEqual(4.5)
  })

  it('uses a deep blue-gray dark canvas rather than pure black', () => {
    expect(ledgerColors.dark.canvas).not.toBe('#000000')
    expect(ledgerColors.dark.surface).not.toBe('#000000')
  })

  it('keeps every dark on-brand interaction state above WCAG AA', () => {
    const onBrand = ledgerDarkTheme.colorNeutralForegroundOnBrand
    const backgrounds = [
      ledgerDarkTheme.colorBrandBackground,
      ledgerDarkTheme.colorBrandBackgroundHover,
      ledgerDarkTheme.colorBrandBackgroundPressed,
      ledgerDarkTheme.colorBrandBackgroundSelected,
    ]

    expect(ledgerDarkTheme.colorBrandForeground1).toBe(ledgerColors.dark.accent)
    backgrounds.forEach((background) => {
      expect(contrastRatio(onBrand, background)).toBeGreaterThanOrEqual(4.5)
    })
  })
})
