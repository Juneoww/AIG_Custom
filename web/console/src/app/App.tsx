/**
 * 功能：渲染企业控制台生产路由并共享匿名可读的公共品牌配置。
 * 实现：AppContent 使用声明式路由，App 在其外建立唯一公共品牌查询边界。
 * 输入：上层 BrowserRouter、主题、查询和会话 Provider。
 * 输出：身份页面或角色化应用壳。
 * 依赖：React Router、公共品牌 Provider 与应用路由树。
 */
import { useRoutes } from 'react-router-dom'

import { PublicBrandProvider } from '../shared/brand/PublicBrandProvider'
import { appRoutes } from './routes'

export function AppContent() {
  return useRoutes(appRoutes)
}

export function App() {
  return (
    <PublicBrandProvider>
      <AppContent />
    </PublicBrandProvider>
  )
}
