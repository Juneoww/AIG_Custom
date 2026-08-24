/**
 * 功能：定义企业控制台端到端测试的浏览器与本地服务基线。
 * 实现：Playwright 在测试前启动仅监听本机严格端口的 Vite 服务，并使用 Chromium 项目执行用例。
 * 输入：后续添加到 e2e 目录的测试文件与本地控制台源码。
 * 输出：端到端测试结果及失败追踪。
 * 依赖：Playwright 与 Vite 开发服务。
 */
import { defineConfig, devices } from '@playwright/test'

const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? 'http://127.0.0.1:4173'
const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  reporter: 'list',
  use: {
    baseURL,
    channel: process.env.PLAYWRIGHT_CHANNEL,
    ...(executablePath ? { launchOptions: { executablePath } } : {}),
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: process.env.PLAYWRIGHT_BASE_URL
    ? undefined
    : {
        command: 'pnpm dev --host 127.0.0.1 --strictPort',
        url: baseURL,
        reuseExistingServer: !process.env.CI,
      },
})
