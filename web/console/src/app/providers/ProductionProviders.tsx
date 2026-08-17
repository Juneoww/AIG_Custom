/**
 * 功能：集中定义企业控制台生产入口的 Provider 顺序。
 * 实现：依次建立主题、查询会话和浏览器路由上下文，并保留测试注入边界。
 * 输入：React 子树、可选查询客户端、会话初态和主题初态。
 * 输出：可直接渲染的生产 Provider 组合。
 * 依赖：React Router、主题提供器与应用查询会话提供器。
 */
import { BrowserRouter } from 'react-router-dom'

import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import type { ThemeMode } from '../../shared/theme/theme-storage'
import { AppProviders, type AppProvidersProps } from './AppProviders'

interface ProductionProvidersProps extends AppProvidersProps {
  themeInitialMode?: ThemeMode
}

export function ProductionProviders({
  children,
  queryClient,
  sessionInitialState,
  themeInitialMode,
}: ProductionProvidersProps) {
  return (
    <ThemeProvider initialMode={themeInitialMode}>
      <AppProviders queryClient={queryClient} sessionInitialState={sessionInitialState}>
        <BrowserRouter>{children}</BrowserRouter>
      </AppProviders>
    </ThemeProvider>
  )
}
