/**
 * 功能：声明身份页面、生产应用壳、角色授权和未知页路由。
 * 实现：守卫校验站内来源，并用统一导航元数据驱动已知路由的角色许可。
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
import { DashboardPage } from '../features/dashboard/DashboardPage'
import { AboutPage } from '../features/about/AboutPage'
import { AuditListPage } from '../features/admin/audit/AuditListPage'
import { BrandSettingsPage } from '../features/admin/brand/BrandSettingsPage'
import { SystemPage } from '../features/admin/system/SystemPage'
import { UserListPage } from '../features/admin/users/UserListPage'
import { AgentConfigPage } from '../features/knowledge/AgentConfigPage'
import { EvaluationPage } from '../features/knowledge/EvaluationPage'
import { FingerprintPage } from '../features/knowledge/FingerprintPage'
import { KnowledgeLayout } from '../features/knowledge/KnowledgeLayout'
import { MCPPage } from '../features/knowledge/MCPPage'
import { PromptCollectionPage } from '../features/knowledge/PromptCollectionPage'
import { VulnerabilityPage } from '../features/knowledge/VulnerabilityPage'
import { ModelListPage } from '../features/models/ModelListPage'
import { ProfilePage } from '../features/profile/ProfilePage'
import { ReportDetailPage } from '../features/reports/ReportDetailPage'
import { ReportListPage } from '../features/reports/ReportListPage'
import { TaskCreatePage } from '../features/tasks/TaskCreatePage'
import { TaskDetailPage } from '../features/tasks/TaskDetailPage'
import { TaskListPage } from '../features/tasks/TaskListPage'
import { MCPWorkbenchPage } from '../features/tasks/MCPWorkbenchPage'
import type { SubjectRole } from '../shared/api/types'
import { PageHeader } from '../shared/components/PageHeader'
import { StatePanel } from '../shared/components/StatePanel'
import { ForbiddenPage } from './ForbiddenPage'
import { NotFoundPage } from './NotFoundPage'
import { AppShell } from './layout/AppShell'
import { navigationItems, type NavigationItem } from './navigation'

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

export function RequireRole({ allowedRoles, children }: GuardProps & { allowedRoles: readonly SubjectRole[] }) {
  const { state } = useSession()
  if (state.status !== 'authenticated') return null
  if (!allowedRoles.includes(state.subject.role)) return <ForbiddenPage />
  return <GuardContent>{children}</GuardContent>
}

function PendingFeaturePage({ item }: { item: NavigationItem }) {
  return (
    <section>
      <PageHeader title={item.label} description={item.description} />
      <StatePanel
        state="empty"
        title={`${item.label}尚未接入`}
        description="当前仅提供应用壳与访问边界，真实功能将在后续开发任务中替换。"
      />
    </section>
  )
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
    element: <RequireAnonymous />,
    children: [{ index: true, element: <ResetPasswordPage /> }],
  },
]

function routeForNavigation(item: NavigationItem): RouteObject {
  if (item.id === 'knowledge') {
    return {
      path: 'knowledge',
      element: <RequireRole allowedRoles={item.allowedRoles}><KnowledgeLayout /></RequireRole>,
      children: [
        { index: true, element: <Navigate replace to="fingerprints" /> },
        { path: 'fingerprints', element: <FingerprintPage /> },
        { path: 'vulnerabilities', element: <VulnerabilityPage /> },
        { path: 'evaluations', element: <EvaluationPage /> },
        { path: 'mcp', element: <MCPPage /> },
        { path: 'prompts', element: <PromptCollectionPage /> },
        { path: 'agents', element: <AgentConfigPage /> },
        { path: 'prompts/manage', element: <RequireRole allowedRoles={['admin']}><PromptCollectionPage /></RequireRole> },
      ],
    }
  }
  const featurePage = item.id === 'overview'
    ? <DashboardPage />
    : item.id === 'tasks'
      ? <TaskListPage />
      : item.id === 'reports'
        ? <ReportListPage />
        : item.id === 'models'
          ? <ModelListPage />
          : item.id === 'users'
            ? <UserListPage />
            : item.id === 'audit'
              ? <AuditListPage />
              : item.id === 'brand'
                ? <BrandSettingsPage />
                : item.id === 'system'
                  ? <SystemPage />
          : <PendingFeaturePage item={item} />
  const element = (
    <RequireRole allowedRoles={item.allowedRoles}>
      {featurePage}
    </RequireRole>
  )
  return item.path === '/' ? { index: true, element } : { path: item.path.slice(1), element }
}

export const appRoutes: RouteObject[] = [
  ...identityRoutes,
  {
    element: <RequireAuthenticated />,
    children: [
      {
        element: <AppShell />,
        children: [
          ...navigationItems.map(routeForNavigation),
          {
            path: 'tasks/new',
            element: (
              <RequireRole allowedRoles={['user', 'admin']}>
                <TaskCreatePage />
              </RequireRole>
            ),
          },
          {
            path: 'tasks/mcp',
            element: (
              <RequireRole allowedRoles={['user', 'auditor', 'admin']}>
                <MCPWorkbenchPage />
              </RequireRole>
            ),
          },
          {
            path: 'tasks/:taskId',
            element: (
              <RequireRole allowedRoles={['user', 'auditor', 'admin']}>
                <TaskDetailPage />
              </RequireRole>
            ),
          },
          {
            path: 'reports/:reportId',
            element: (
              <RequireRole allowedRoles={['user', 'auditor', 'admin']}>
                <ReportDetailPage />
              </RequireRole>
            ),
          },
          { path: 'profile', element: <ProfilePage /> },
          { path: 'about', element: <AboutPage /> },
          { path: '*', element: <NotFoundPage /> },
        ],
      },
    ],
  },
]
