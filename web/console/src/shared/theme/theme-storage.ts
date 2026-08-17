/**
 * 功能：读写企业控制台唯一允许持久化的主题偏好。
 * 实现：仅访问固定 localStorage 键，并将非法或不可读值安全回退为浅色。
 * 输入：浏览器 Storage 与 light、dark、system 三种模式。
 * 输出：已校验主题模式或一次固定键写入。
 * 依赖：浏览器 Storage API。
 */
export const THEME_STORAGE_KEY = 'aig-console-theme'

export type ThemeMode = 'light' | 'dark' | 'system'

const themeModes = new Set<ThemeMode>(['light', 'dark', 'system'])

export function readThemeMode(storage: Storage | undefined): ThemeMode {
  if (!storage) {
    return 'light'
  }

  try {
    const storedMode = storage.getItem(THEME_STORAGE_KEY)
    return storedMode && themeModes.has(storedMode as ThemeMode) ? (storedMode as ThemeMode) : 'light'
  } catch {
    return 'light'
  }
}

export function writeThemeMode(storage: Storage | undefined, mode: ThemeMode): void {
  if (!storage) {
    return
  }

  try {
    storage.setItem(THEME_STORAGE_KEY, mode)
  } catch {
    // 浏览器拒绝持久化时仍保留本次内存态选择。
  }
}
