/**
 * 功能：建立控制台查询缓存和身份会话的应用级提供器边界。
 * 实现：生产复用单例 QueryClient，测试可注入隔离实例和明确会话状态。
 * 输入：React 子树、可选 QueryClient 与测试会话初态。
 * 输出：关闭自动重试的 TanStack Query 和 Session 上下文。
 * 依赖：React、TanStack Query 与身份会话提供器。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'

import { SessionProvider, type SessionState } from '../../features/auth/session'

export function createAppQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
        refetchOnWindowFocus: false,
      },
      mutations: {
        retry: false,
      },
    },
  })
}

const appQueryClient = createAppQueryClient()

export interface AppProvidersProps {
  children: ReactNode
  queryClient?: QueryClient
  sessionInitialState?: SessionState
}

export function AppProviders({ children, queryClient = appQueryClient, sessionInitialState }: AppProvidersProps) {
  return (
    <QueryClientProvider client={queryClient}>
      <SessionProvider initialState={sessionInitialState}>{children}</SessionProvider>
    </QueryClientProvider>
  )
}
