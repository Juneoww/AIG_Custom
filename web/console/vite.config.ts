/**
 * 功能：定义企业控制台的 Vite 构建与 Vitest 运行配置。
 * 实现：启用 React 插件，并让单元测试在 jsdom 中加载统一断言扩展。
 * 输入：控制台源码、index.html 与测试文件。
 * 输出：开发服务器、生产静态资源和测试运行环境。
 * 依赖：Vite、React 插件与 Vitest。
 */
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port: 4173,
  },
  preview: {
    host: '0.0.0.0',
    port: 4173,
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    css: true,
    server: {
      deps: {
        inline: [/@fluentui/, 'tabster'],
      },
    },
  },
})
