/**
 * 功能：通过真实平台、Agent 和 Python 进程验证 Skills ZIP 的成功、失败、取消链路。
 * 实现：仅在隔离模型协议夹具配置后运行；浏览器使用种子账号、治理模型和标准附件接口。
 * 输入：SKILLS_E2E_MODEL_URL、SKILLS_E2E_INVALID_MODEL_URL、SKILLS_E2E_SLOW_MODEL_URL。
 * 输出：安全请求、Skills 身份、不可变报告及页面布局断言；模型夹具不用于评估实际检出能力。
 */
import type { Page, TestInfo } from '@playwright/test'
import { expect, signInReadyUser, test } from './helpers'

function skillZip(): Buffer {
  const name = Buffer.from('SKILL.md')
  const body = Buffer.from('---\nname: protocol-fixture\ndescription: A harmless fixture for the scan pipeline.\n---\nRead only the provided files.\n')
  let crc = 0xffffffff
  for (const byte of body) {
    crc ^= byte
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1))
  }
  crc = (crc ^ 0xffffffff) >>> 0
  const local = Buffer.alloc(30)
  local.writeUInt32LE(0x04034b50, 0)
  local.writeUInt16LE(20, 4)
  local.writeUInt32LE(crc, 14)
  local.writeUInt32LE(body.length, 18)
  local.writeUInt32LE(body.length, 22)
  local.writeUInt16LE(name.length, 26)
  const central = Buffer.alloc(46)
  central.writeUInt32LE(0x02014b50, 0)
  central.writeUInt16LE(20, 4)
  central.writeUInt16LE(20, 6)
  central.writeUInt32LE(crc, 16)
  central.writeUInt32LE(body.length, 20)
  central.writeUInt32LE(body.length, 24)
  central.writeUInt16LE(name.length, 28)
  const end = Buffer.alloc(22)
  end.writeUInt32LE(0x06054b50, 0)
  end.writeUInt16LE(1, 8)
  end.writeUInt16LE(1, 10)
  end.writeUInt32LE(central.length + name.length, 12)
  end.writeUInt32LE(local.length + name.length + body.length, 16)
  return Buffer.concat([local, name, body, central, name, end])
}

async function createThroughPage(page: Page, baseURL: string, testInfo?: TestInfo) {
  await signInReadyUser(page)
  const csrf = await (await page.request.get('/api/v1/auth/csrf')).json()
  const response = await page.request.post('/api/v1/platform/models', {
    headers: { 'X-CSRF-Token': csrf.csrf_token },
    data: { name: `Skills 协议夹具 ${Date.now()}`, provider_model: 'skills-fixture', base_url: baseURL, token: 'local-protocol-fixture-only', scope: 'private' },
  })
  expect(response.status()).toBe(201)
  const model = await response.json()
  await page.goto('/tasks/skills/new')
  const submit = page.getByRole('button', { name: '创建 Skills 扫描任务', exact: true })
  await expect(submit).toBeDisabled()
  await page.getByRole('combobox', { name: /扫描模型/ }).selectOption(model.id)
  await expect(submit).toBeDisabled()
  await page.getByLabel('Skills ZIP 包', { exact: true }).setInputFiles({ name: 'protocol-fixture.zip', mimeType: 'application/zip', buffer: skillZip() })
  await expect(submit).toBeDisabled()
  await page.getByRole('button', { name: '上传 Skills 包', exact: true }).click()
  await expect(page.getByLabel('已上传 Skills 包', { exact: true })).toBeVisible()
  await page.getByLabel('任务说明 / 备注（可选）').fill('SKILLS_E2E_REMARK_ONLY')
  await expect(submit).toBeEnabled()
  if (testInfo) {
    for (const width of [1440, 768, 320]) {
      await page.setViewportSize({ width, height: 1000 })
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
      const modelBounds = await page.getByRole('combobox', { name: /扫描模型/ }).boundingBox()
      expect(modelBounds).not.toBeNull()
      expect(modelBounds!.x + modelBounds!.width).toBeLessThanOrEqual(width)
      for (const theme of ['浅色', '深色']) {
        await page.getByRole('combobox', { name: '主题模式' }).selectOption({ label: theme })
        await page.screenshot({ path: testInfo.outputPath(`skills-new-${width}-${theme}.png`), fullPage: true })
      }
    }
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.getByRole('combobox', { name: '主题模式' }).selectOption({ label: '浅色' })
  }
  const created = page.waitForResponse((item) => item.request().method() === 'POST' && new URL(item.url()).pathname === '/api/v1/platform/tasks')
  await submit.click()
  const creation = await created
  expect(creation.status()).toBe(202)
  const input = creation.request().postDataJSON()
  expect(input).toMatchObject({ task_type: 'skills_scan', content: '', params: { model_id: model.id } })
  expect(Object.keys(input.params)).toEqual(['model_id'])
  expect(input.attachment_ids).toHaveLength(1)
  const task = await creation.json()
  await expect(page).toHaveURL(new RegExp(`/tasks/skills/${task.id}$`))
  expect(task.input_summary).toEqual({ language: 'zh', model_id: model.id, scan_mode: 'static' })
  return task.id as string
}

async function taskStatus(page: Page, id: string) {
  const response = await page.request.get(`/api/v1/platform/tasks/${id}`)
  expect(response.status()).toBe(200)
  return (await response.json()).status as string
}

async function reportsFor(page: Page, id: string) {
  const response = await page.request.get('/api/v1/platform/reports?page=1&page_size=100')
  expect(response.status()).toBe(200)
  const result = await response.json()
  return (result.items as { id: string; task_id: string; task_type: string; risk: { score: number; high: number } }[]).filter((item) => item.task_id === id)
}

async function fixtureRequests(modelURL: string) {
  const response = await fetch(new URL('/stats', modelURL), { signal: AbortSignal.timeout(5_000) })
  expect(response.status).toBe(200)
  return (await response.json()).requests as number
}

test('Skills ZIP 经真实执行链路生成独立报告', async ({ page }, testInfo) => {
  test.skip(!process.env.SKILLS_E2E_MODEL_URL, '需要隔离模型协议夹具和 Skills Agent')
  test.setTimeout(120_000)
  const id = await createThroughPage(page, process.env.SKILLS_E2E_MODEL_URL!, testInfo)
  await expect.poll(() => taskStatus(page, id), { timeout: 90_000 }).toBe('succeeded')
  await expect(page.getByRole('region', { name: '任务安全摘要' })).toContainText('已完成')
  await page.screenshot({ path: testInfo.outputPath('skills-completed.png'), fullPage: true })
  await page.setViewportSize({ width: 320, height: 1000 })
  await expect.poll(() => page.getByRole('region', { name: '任务安全摘要' }).locator(':scope > div > div').evaluateAll(
    (facts) => facts.length > 0 && facts.every((fact) => fact.scrollWidth <= fact.clientWidth),
  )).toBe(true)
  await page.screenshot({ path: testInfo.outputPath('skills-detail-320.png'), fullPage: true })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await expect.poll(async () => (await reportsFor(page, id)).length).toBe(1)
  const [report] = await reportsFor(page, id)
  expect(report).toMatchObject({ task_type: 'skills_scan', risk: { score: 60, high: 1 } })
  const reportResponse = await page.request.get(`/api/v1/platform/reports/${report.id}`)
  expect(await reportResponse.text()).not.toContain('SKILLS_E2E_REMARK_ONLY')
  await page.goto('/reports')
  await page.getByRole('link', { name: `查看报告 ${report.id}`, exact: true }).click()
  await expect(page.getByRole('region', { name: '快照信息' })).toContainText(id)
  await page.screenshot({ path: testInfo.outputPath('skills-report.png'), fullPage: true })
  await page.goto('/tasks/skills')
  await expect(page.getByRole('table')).toContainText(id)
  await expect.poll(() => page.getByRole('table').locator('tbody tr td:first-child').evaluateAll(
    (cells) => cells.length > 0 && cells.every((cell) => cell.scrollWidth <= cell.clientWidth + 1),
  )).toBe(true)
  const mcp = await (await page.request.get('/api/v1/platform/tasks?task_type=mcp_scan&page=1&page_size=100')).json()
  expect(mcp.items.map((item: { id: string }) => item.id)).not.toContain(id)
  for (const width of [1440, 768, 320]) {
    await page.setViewportSize({ width, height: 1000 })
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath(`skills-list-${width}.png`), fullPage: true })
  }
})

test('损坏的模型结果使 Skills 失败且没有成功报告', async ({ page }) => {
  test.skip(!process.env.SKILLS_E2E_INVALID_MODEL_URL, '需要损坏输出模型协议夹具')
  test.setTimeout(120_000)
  const id = await createThroughPage(page, process.env.SKILLS_E2E_INVALID_MODEL_URL!)
  await expect.poll(() => taskStatus(page, id), { timeout: 90_000 }).toBe('failed')
  await expect(page.getByRole('region', { name: '任务安全摘要' })).toContainText('执行失败')
  expect(await reportsFor(page, id)).toEqual([])
})

test('取消执行中的 Skills 任务后不产生报告', async ({ page }) => {
  test.skip(!process.env.SKILLS_E2E_SLOW_MODEL_URL, '需要延迟响应模型协议夹具')
  test.setTimeout(120_000)
  const modelURL = process.env.SKILLS_E2E_SLOW_MODEL_URL!
  const before = await fixtureRequests(modelURL)
  const id = await createThroughPage(page, modelURL)
  await expect.poll(() => taskStatus(page, id), { timeout: 30_000 }).toBe('running')
  // 等待 Python 已调用延迟模型，避免只验证尚未执行的调度任务取消。
  await expect.poll(() => fixtureRequests(modelURL), { timeout: 30_000 }).toBeGreaterThan(before)
  const activeRequests = await fixtureRequests(modelURL)
  await page.getByRole('button', { name: '取消任务', exact: true }).click()
  await expect.poll(() => taskStatus(page, id), { timeout: 30_000 }).toBe('cancelled')
  await expect(page.getByRole('region', { name: '任务安全摘要' })).toContainText('已取消')
  expect(await reportsFor(page, id)).toEqual([])
  await new Promise((resolve) => setTimeout(resolve, 21_000))
  expect(await fixtureRequests(modelURL)).toBe(activeRequests)
  expect(await taskStatus(page, id)).toBe('cancelled')
  expect(await reportsFor(page, id)).toEqual([])
})
