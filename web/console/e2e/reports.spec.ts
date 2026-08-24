/**
 * 功能：验证不可变报告目录、详情与受审计 PDF 导出。
 * 实现：读取隔离报告快照并通过浏览器下载 API 导出同一报告，避免直接访问任务原始结果。
 * 输入：E2E 夹具固化的 e2e-report-001 报告与管理员会话。
 * 输出：报告列表、详情、PDF 下载和安全请求断言。
 * 依赖：报告快照仓储、PDF 渲染器与 Playwright 下载事件。
 */
import { expect, signInReadyUser, test } from './helpers'

test('管理员可读取固化报告详情并导出 PDF', async ({ page }) => {
  await signInReadyUser(page, 'e2e-admin', 'e2e-admin-password')
  await page.goto('/reports')

  await expect(page.getByRole('heading', { name: '安全报告' })).toBeVisible()
  await page.getByRole('link', { name: '查看报告 e2e-report-001' }).click()
  await expect(page.getByRole('heading', { name: '安全报告详情' })).toBeVisible()
  await expect(page.getByRole('region', { name: '快照信息' })).toContainText('e2e-task-001')

  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: '导出 PDF' }).click()
  await expect((await download).suggestedFilename()).toBe('安全报告.pdf')
})

test('普通用户只能看到自己所属的报告快照', async ({ page }) => {
  await signInReadyUser(page)
  await page.goto('/reports')

  await expect(page.getByRole('link', { name: '查看报告 e2e-report-001' })).toBeVisible()
  await page.goto('/reports/e2e-report-admin-only')
  await expect(page.getByText('安全报告不存在')).toBeVisible()
})
