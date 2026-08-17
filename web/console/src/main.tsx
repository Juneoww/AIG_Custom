/**
 * 功能：挂载带主题、查询会话与浏览器路由的企业控制台生产应用。
 * 实现：加载本地全局样式，并在严格模式下按稳定边界组合各 Provider。
 * 输入：浏览器 DOM 与 App 根组件。
 * 输出：控制台根视图。
 * 依赖：React DOM、React Router、ThemeProvider、AppProviders 与全局样式。
 */
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'

import { App } from './app/App'
import { AppProviders } from './app/providers/AppProviders'
import { ThemeProvider } from './shared/theme/ThemeProvider'
import './shared/styles/global.css'

const rootElement = document.getElementById('root')

if (!rootElement) {
  throw new Error('缺少控制台挂载节点')
}

createRoot(rootElement).render(
  <StrictMode>
    <ThemeProvider>
      <AppProviders>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </AppProviders>
    </ThemeProvider>
  </StrictMode>,
)
