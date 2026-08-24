/**
 * 功能：验证普通用户的任务与附件浏览器流程。
 * 实现：通过真实 Cookie 进入任务台账，上传小型本地附件并确认创建页面不会访问原始任务结果。
 * 输入：隔离平台中的普通用户及 Playwright 内存文件。
 * 输出：任务台账、附件状态和安全请求断言。
 * 依赖：真实任务/附件 API、Vite 反向代理和 E2E 请求守卫。
 */
import { expect, signInReadyUser, test } from './helpers'

test('普通用户可查看任务台账、短轮询运行任务并上传待提交附件', async ({ page }) => {
  await signInReadyUser(page)
  await page.getByRole('link', { name: '扫描任务' }).click()

  await expect(page.getByRole('heading', { name: '扫描任务' })).toBeVisible()
  await page.getByRole('link', { name: '查看任务 e2e-task-running' }).click()
  await expect(page.getByRole('heading', { name: '任务详情' })).toBeVisible()
  await expect(page.getByRole('region', { name: '任务安全摘要' })).toContainText('执行中')

  let detailReads = 0
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/v1/platform/tasks/e2e-task-running') detailReads += 1
  })
  await expect.poll(() => detailReads, { timeout: 8_000 }).toBeGreaterThan(0)
  await page.getByRole('link', { name: '返回任务台账' }).click()
  await page.getByRole('link', { name: '创建扫描任务' }).click()
  await expect(page.getByRole('heading', { name: '创建扫描任务' })).toBeVisible()

  await page.locator('input[type="file"]').setInputFiles({
    name: 'e2e-attachment.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('isolated browser fixture'),
  })
  await page.getByRole('button', { name: '上传附件' }).click()
  await expect(page.getByRole('button', { name: '下载附件 e2e-attachment.txt' })).toBeVisible()
})

test('审计员只能读取任务台账，不能创建扫描任务', async ({ page }) => {
  await signInReadyUser(page, 'e2e-auditor', 'e2e-auditor-password')
  await page.goto('/tasks')

  await expect(page.getByRole('heading', { name: '扫描任务' })).toBeVisible()
  await expect(page.getByRole('link', { name: '创建扫描任务' })).toHaveCount(0)
  await page.goto('/tasks/new')
  await expect(page.getByRole('heading', { name: '无权访问' })).toBeVisible()
})
