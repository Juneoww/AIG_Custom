/**
 * 功能：验证匿名、越权与未知路由在真实浏览器中的安全降级。
 * 实现：分别访问受保护页面、管理员页面与未知站内地址，不触发遗留接口或资源请求。
 * 输入：匿名会话、普通用户会话和隔离 Vite 控制台。
 * 输出：登录重定向、站内 403/404 与 E2E 请求守卫断言。
 * 依赖：React Router 身份守卫、角色守卫与 Playwright。
 */
import { expect, signInReadyUser, test } from './helpers'

test('匿名访问受保护页面会返回登录页', async ({ page }) => {
  await page.goto('/tasks?from=e2e')

  await expect(page).toHaveURL(/\/login$/)
  await expect(page.getByRole('heading', { name: '登录平台' })).toBeVisible()
})

test('普通用户访问管理员页面和未知路径分别得到 403 与 404', async ({ page }) => {
  await signInReadyUser(page)
  await page.goto('/admin/users')
  await expect(page.getByRole('heading', { name: '无权访问' })).toBeVisible()

  await page.goto('/not-a-console-route')
  await expect(page.getByRole('heading', { name: '页面不存在' })).toBeVisible()
})
