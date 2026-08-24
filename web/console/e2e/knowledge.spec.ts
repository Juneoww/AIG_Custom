/**
 * 功能：验证知识库的真实目录读取、管理员编辑入口与非管理员只读边界。
 * 实现：管理员打开指纹规则台账和编辑器；审计员直达治理路径时收到站内 403。
 * 输入：内置知识数据、管理员与审计员会话。
 * 输出：知识库页面、编辑按钮可见性与授权断言。
 * 依赖：知识领域适配 API、RawResourceLedger 与 E2E 请求守卫。
 */
import { expect, signInReadyUser, test } from './helpers'

test('管理员可查看指纹规则并进入新建编辑器', async ({ page }) => {
  await signInReadyUser(page, 'e2e-admin', 'e2e-admin-password')
  await page.goto('/knowledge/fingerprints')

  await expect(page.getByRole('heading', { name: '指纹规则' })).toBeVisible()
  await page.getByRole('button', { name: '新增指纹规则' }).click()
  await expect(page.locator('section[aria-label="指纹规则编辑"]')).toBeVisible()
  await expect(page.getByRole('button', { name: '保存指纹规则' })).toBeVisible()
})

test('审计员不得进入 Prompt 集合治理直达路由', async ({ page }) => {
  await signInReadyUser(page, 'e2e-auditor', 'e2e-auditor-password')
  await page.goto('/knowledge/prompts/manage')

  await expect(page.getByRole('heading', { name: '无权访问' })).toBeVisible()
})
