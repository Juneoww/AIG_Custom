/**
 * 功能：在隔离平台中验证浏览器身份主流程。
 * 实现：通过真实登录页完成管理员会话与首次改密用户的跳转，并保持 Cookie/CSRF 由浏览器处理。
 * 输入：compose 种子提供的固定测试账号；控制台与平台的同源测试地址。
 * 输出：Playwright 端到端断言及失败追踪，不记录密码或会话令牌。
 * 依赖：Playwright、隔离 PostgreSQL、测试平台与 Vite 控制台。
 */
import { expect, signIn, test } from './helpers'

test('管理员通过真实 Cookie 与 CSRF 登录后进入治理总览', async ({ page }) => {
  await signIn(page, 'e2e-admin', 'e2e-admin-password')

  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByRole('heading', { name: '治理总览' })).toBeVisible()
  await expect(page.getByText('e2e-admin', { exact: true })).toBeVisible()
})

test('首次登录用户只能进入强制改密流程', async ({ page }) => {
  await signIn(page, 'e2e-first-login', 'e2e-first-login-password')

  await expect(page).toHaveURL(/\/change-password/)
  await expect(page.getByRole('heading', { name: '更新初始密码' })).toBeVisible()
})
