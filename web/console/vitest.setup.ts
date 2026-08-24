/**
 * 功能：为控制台单元测试注册浏览器 DOM 断言。
 * 实现：在每个 Vitest 测试文件前加载 jest-dom 的 Vitest 适配入口。
 * 输入：Vitest 的 jsdom 环境。
 * 输出：扩展后的 expect 断言。
 * 依赖：@testing-library/jest-dom、Testing Library 与 Vitest。
 */
import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

afterEach(cleanup)
