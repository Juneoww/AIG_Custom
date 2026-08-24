/**
 * 功能：提供企业控制台 E2E 的真实登录动作与浏览器请求安全守卫。
 * 实现：所有规格复用同源 Cookie 登录，并在每例结束时拒绝遗留 API、Agent WebSocket、CDN、原始结果与绝对路径请求。
 * 输入：compose 种子账号、Playwright Page 与由控制台发出的浏览器请求。
 * 输出：可复用 test/expect、已登录 Page 与不含敏感请求的断言。
 * 依赖：@playwright/test、隔离控制台 Vite 服务和平台反向代理。
 */
import { expect, test as base, type Page } from '@playwright/test'

const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:4173'
const consoleOrigin = new URL(baseURL).origin

function prohibitedRequest(url: URL): string | undefined {
  if (url.pathname.startsWith('/api/v1/app/') || url.pathname.startsWith('/legacy')) return '遗留浏览器路由'
  if (/^\/api\/v1\/platform\/tasks\/[^/]+\/result$/.test(url.pathname)) return '原始任务结果'
  if (url.pathname.includes('/api/v1/agents/') || (url.protocol.startsWith('ws') && url.port === '8088')) return 'Agent WebSocket'
  if ((url.protocol === 'http:' || url.protocol === 'https:') && url.origin !== consoleOrigin) return '公网或 CDN'
  if (url.protocol === 'file:') return '绝对文件路径'
  for (const name of ['path', 'file', 'filename']) {
    const value = url.searchParams.get(name)
    if (value && (/^[A-Za-z]:[\\/]/.test(value) || /^\/(?:private|tmp|var)\//.test(value))) return '绝对文件路径'
  }
  return undefined
}

export const test = base.extend({
  page: async ({ page }, use) => {
    const violations: string[] = []
    page.on('request', (request) => {
      try {
        const reason = prohibitedRequest(new URL(request.url()))
        if (reason) violations.push(`${reason}: ${request.url()}`)
      } catch {
        violations.push(`无法解析的浏览器请求: ${request.url()}`)
      }
    })

    await use(page)
    expect(violations, `检测到不应由控制台发出的请求:\n${violations.join('\n')}`).toEqual([])
  },
})

export { expect }

export async function signIn(page: Page, username: string, password: string) {
  await page.goto('/login')
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('密码').fill(password)
  await page.getByRole('button', { name: '登录' }).click()
}

export async function signInReadyUser(page: Page, username = 'e2e-user', password = 'e2e-user-password') {
  await signIn(page, username, password)
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByRole('heading', { name: '治理总览' })).toBeVisible()
}
