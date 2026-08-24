/**
 * 功能：验证浅色、深色和跟随系统主题在真实浏览器中的持久化行为。
 * 实现：切换顶栏选择器、检查根元素主题属性并刷新页面确认本地偏好恢复。
 * 输入：管理员会话和 Playwright 模拟的系统配色。
 * 输出：三种主题模式的可见状态断言。
 * 依赖：ThemeProvider、localStorage 与顶栏主题选择器。
 */
import { expect, signInReadyUser, test } from './helpers'

test('主题选择覆盖系统配色且刷新后保持', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'dark' })
  await signInReadyUser(page, 'e2e-admin', 'e2e-admin-password')
  const theme = page.getByLabel('主题模式')

  await theme.selectOption('system')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await theme.selectOption('light')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
  await page.reload()
  await expect(page.getByLabel('主题模式')).toHaveValue('light')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')

  await page.getByLabel('主题模式').selectOption('dark')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
})
