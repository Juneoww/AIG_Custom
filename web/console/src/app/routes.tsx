/**
 * 功能：声明身份页面路由以及匿名、已认证和强制改密守卫。
 * 实现：守卫只读取会话上下文并保留来源路径，不在模块加载时请求网络。
 * 输入：当前路由位置、会话状态与可选受保护子视图。
 * 输出：目标页面、Outlet 或安全重定向。
 * 依赖：React Router、身份页面与 Session 上下文。
 */
import type { ReactNode } from 'react'
import { Navigate, Outlet, useLocation, type RouteObject } from 'react-router-dom'

import { ChangePasswordPage } from '../features/auth/ChangePasswordPage'
import { LoginPage } from '../features/auth/LoginPage'
import { ResetPasswordPage } from '../features/auth/ResetPasswordPage'
import { useSession } from '../features/auth/session'

interface GuardProps {
  children?: ReactNode
}

function GuardContent({ children }: GuardProps) {
  return children ?? <Outlet />
}

function RestoringSession() {
  return <p role="status">正在验证登录状态</p>
}

export function RequireAuthenticated({ children }: GuardProps) {
  const { state } = useSession()
  const location = useLocation()

  if (state.status === 'restoring') return <RestoringSession />
  if (state.status === 'anonymous') {
    return <Navigate replace to="/login" state={{ from: location.pathname }} />
  }
  if (state.status === 'must-change') {
    return <Navigate replace to="/change-password" state={{ from: location.pathname }} />
  }
  return <GuardContent>{children}</GuardContent>
}

export function RequirePasswordChange({ children }: GuardProps) {
  const { state } = useSession()
  const location = useLocation()

  if (state.status === 'restoring') return <RestoringSession />
  if (state.status === 'anonymous') {
    return <Navigate replace to="/login" state={{ from: location.pathname }} />
  }
  if (state.status === 'authenticated') return <Navigate replace to="/" />
  return <GuardContent>{children}</GuardContent>
}

export function RequireAnonymous({ children }: GuardProps) {
  const { state } = useSession()

  if (state.status === 'restoring') return <RestoringSession />
  if (state.status === 'must-change') return <Navigate replace to="/change-password" />
  if (state.status === 'authenticated') return <Navigate replace to="/" />
  return <GuardContent>{children}</GuardContent>
}

export const identityRoutes: RouteObject[] = [
  {
    path: '/login',
    element: <RequireAnonymous />,
    children: [{ index: true, element: <LoginPage /> }],
  },
  {
    path: '/change-password',
    element: <RequirePasswordChange />,
    children: [{ index: true, element: <ChangePasswordPage /> }],
  },
  {
    path: '/reset-password',
    element: <ResetPasswordPage />,
  },
]
