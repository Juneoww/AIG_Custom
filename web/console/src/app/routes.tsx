/**
 * 功能：声明身份页面路由以及匿名、已认证和强制改密守卫。
 * 实现：守卫校验站内来源并贯穿登录、强制改密和重新登录链路，不在模块加载时请求网络。
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

function RestoreSessionError({ message }: { message: string }) {
  const { restore } = useSession()
  return (
    <div role="alert">
      <p>{message}</p>
      <button type="button" onClick={() => void restore()}>
        重试
      </button>
    </div>
  )
}

const AUTH_PATHS = ['/login', '/change-password', '/reset-password']

function safeReturnTo(value: unknown): string {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) {
    return '/'
  }

  try {
    const target = new URL(value, window.location.origin)
    if (target.origin !== window.location.origin) return '/'
    if (!target.pathname.startsWith('/') || target.pathname.startsWith('//') || target.pathname.includes('\\')) {
      return '/'
    }
    if (AUTH_PATHS.some((path) => target.pathname === path || target.pathname.startsWith(`${path}/`))) return '/'
    return `${target.pathname}${target.search}${target.hash}`
  } catch {
    return '/'
  }
}

function returnToFromState(state: unknown): string {
  return safeReturnTo((state as { from?: unknown } | null)?.from)
}

function currentLocation(location: ReturnType<typeof useLocation>): string {
  return `${location.pathname}${location.search}${location.hash}`
}

export function RequireAuthenticated({ children }: GuardProps) {
  const { state } = useSession()
  const location = useLocation()

  if (state.status === 'restoring') return <RestoringSession />
  if (state.status === 'restore-error') return <RestoreSessionError message={state.message} />
  if (state.status === 'anonymous') {
    return <Navigate replace to="/login" state={{ from: safeReturnTo(currentLocation(location)) }} />
  }
  if (state.status === 'must-change') {
    return <Navigate replace to="/change-password" state={{ from: safeReturnTo(currentLocation(location)) }} />
  }
  return <GuardContent>{children}</GuardContent>
}

export function RequirePasswordChange({ children }: GuardProps) {
  const { state } = useSession()
  const location = useLocation()

  if (state.status === 'restoring') return <RestoringSession />
  if (state.status === 'restore-error') return <RestoreSessionError message={state.message} />
  if (state.status === 'anonymous') {
    return <Navigate replace to="/login" state={{ from: returnToFromState(location.state) }} />
  }
  if (state.status === 'authenticated') return <Navigate replace to={returnToFromState(location.state)} />
  return <GuardContent>{children}</GuardContent>
}

export function RequireAnonymous({ children }: GuardProps) {
  const { state } = useSession()
  const location = useLocation()
  const returnTo = returnToFromState(location.state)

  if (state.status === 'restoring') return <RestoringSession />
  if (state.status === 'restore-error') return <RestoreSessionError message={state.message} />
  if (state.status === 'must-change') {
    return <Navigate replace to="/change-password" state={{ from: returnTo }} />
  }
  if (state.status === 'authenticated') return <Navigate replace to={returnTo} />
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
