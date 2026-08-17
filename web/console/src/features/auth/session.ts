/**
 * 功能：管理浏览器身份会话及登录、改密、退出和重置流程。
 * 实现：以 React 上下文保存 Subject，并通过真实 Cookie API 合同迁移状态。
 * 输入：用户凭据、服务端身份 DTO 与可选测试初始状态。
 * 输出：anonymous、authenticated、must-change 或 restoring 会话状态。
 * 依赖：React、共享 API 客户端与身份 DTO；不使用浏览器持久化存储。
 */
import { createContext, createElement, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'

import { apiRequest, subscribeToUnauthorized } from '../../shared/api/client'
import { ApiError } from '../../shared/api/errors'
import type {
  CSRFResponse,
  ChangePasswordRequest,
  CurrentSubject,
  LoginRequest,
  LoginResponse,
  PasswordResetConfirmRequest,
} from '../../shared/api/types'

export type SessionState =
  | { status: 'restoring' }
  | { status: 'anonymous' }
  | { status: 'authenticated'; subject: CurrentSubject }
  | { status: 'must-change'; subject: CurrentSubject }

export interface SessionContextValue {
  state: SessionState
  restore: () => Promise<void>
  login: (username: string, password: string) => Promise<void>
  changePassword: (oldPassword: string, newPassword: string) => Promise<void>
  logout: () => Promise<void>
}

export interface SessionProviderProps {
  children: ReactNode
  initialState?: SessionState
}

const SessionContext = createContext<SessionContextValue | undefined>(undefined)

function stateForSubject(subject: CurrentSubject): SessionState {
  return subject.must_change_password
    ? { status: 'must-change', subject }
    : { status: 'authenticated', subject }
}

function jsonRequest<T>(body: T): Pick<RequestInit, 'method' | 'headers' | 'body'> {
  return {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

async function initializeAnonymousCSRF(): Promise<void> {
  await apiRequest<CSRFResponse>('/api/v1/auth/csrf')
}

async function fetchCurrentSubject(): Promise<CurrentSubject> {
  return apiRequest<CurrentSubject>('/api/v1/auth/me')
}

export async function confirmPasswordReset(input: PasswordResetConfirmRequest): Promise<void> {
  await initializeAnonymousCSRF()
  await apiRequest<void>('/api/v1/auth/password-resets/confirm', jsonRequest(input))
}

export function SessionProvider({ children, initialState }: SessionProviderProps) {
  const [state, setState] = useState<SessionState>(initialState ?? { status: 'restoring' })

  const restore = useCallback(async () => {
    try {
      const subject = await fetchCurrentSubject()
      setState(stateForSubject(subject))
    } catch (error) {
      if (error instanceof ApiError && error.kind === 'unauthenticated') {
        setState({ status: 'anonymous' })
        return
      }
      setState({ status: 'anonymous' })
    }
  }, [])

  const login = useCallback(async (username: string, password: string) => {
    await initializeAnonymousCSRF()
    const input: LoginRequest = { username, password }
    await apiRequest<LoginResponse>('/api/v1/auth/login', jsonRequest(input))
    const subject = await fetchCurrentSubject()
    setState(stateForSubject(subject))
  }, [])

  const changePassword = useCallback(async (oldPassword: string, newPassword: string) => {
    const input: ChangePasswordRequest = { old_password: oldPassword, new_password: newPassword }
    await apiRequest<void>('/api/v1/auth/change-password', jsonRequest(input))
    setState({ status: 'anonymous' })
  }, [])

  const logout = useCallback(async () => {
    await apiRequest<void>('/api/v1/auth/logout', jsonRequest({}))
    setState({ status: 'anonymous' })
  }, [])

  useEffect(() => subscribeToUnauthorized(() => setState({ status: 'anonymous' })), [])

  useEffect(() => {
    if (initialState !== undefined) return
    void restore()
  }, [initialState, restore])

  const value = useMemo<SessionContextValue>(
    () => ({ state, restore, login, changePassword, logout }),
    [changePassword, login, logout, restore, state],
  )

  return createElement(SessionContext.Provider, { value }, children)
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext)
  if (!value) throw new Error('身份页面必须位于 SessionProvider 内')
  return value
}
