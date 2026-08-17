/**
 * 功能：挂载企业控制台 React 根组件。
 * 实现：在严格模式下把 App 渲染到 index.html 的 root 节点。
 * 输入：浏览器 DOM 与 App 根组件。
 * 输出：可交互的控制台页面。
 * 依赖：React DOM。
 */
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { App } from './app/App'

const rootElement = document.getElementById('root')

if (!rootElement) {
  throw new Error('缺少控制台挂载节点')
}

createRoot(rootElement).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
