/**
 * 功能：验证管理员用户治理和审计员全局只读导航。
 * 实现：管理员通过真实受 CSRF 保护的表单创建测试用户；审计员读取审计台账且无管理入口。
 * 输入：固定管理员/审计员账号及临时 E2E 用户名。
 * 输出：用户台账与审计台账的角色边界断言。
 * 依赖：管理员 API、审计 API 与 E2E 请求守卫。
 */
import { expect, signInReadyUser, test } from './helpers'

test('管理员可创建用户且不显示任何重置令牌', async ({ page }, testInfo) => {
  const username = `e2e-created-user-${Date.now()}-${testInfo.retry}`
  await signInReadyUser(page, 'e2e-admin', 'e2e-admin-password')
  await page.goto('/admin/users')

  await expect(page.getByRole('heading', { name: '用户管理' })).toBeVisible()
  await page.getByRole('button', { name: '新增用户' }).click()
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('临时密码').fill('e2e-created-user-password')
  await page.getByRole('button', { name: '确认创建' }).click()
  await expect(page.getByRole('table', { name: '用户台账' })).toContainText(username)
  await expect(page.getByText(/reset.*token/i)).toHaveCount(0)
})

test('审计员可读审计台账但没有用户管理入口', async ({ page }) => {
  await signInReadyUser(page, 'e2e-auditor', 'e2e-auditor-password')
  await page.goto('/admin/audit')

  await expect(page.getByRole('heading', { name: '审计事件' })).toBeVisible()
  await expect(page.getByRole('link', { name: '用户管理' })).toHaveCount(0)
  await expect(page.getByRole('table', { name: '治理审计台账' })).toContainText('e2e-admin')
})
