/**
 * 功能：管理浏览器身份会话及登录、改密、退出和重置流程。
 * 实现：以 React 上下文保存 Subject，并通过真实 Cookie API 合同迁移状态。
 * 输入：用户凭据、服务端身份 DTO 与可选测试初始状态。
 * 输出：匿名、认证、强制改密、恢复中或安全恢复失败会话状态。
 * 依赖：React、TanStack Query、共享 API 客户端与身份 DTO；不使用浏览器持久化存储。
 */
import { useQueryClient } from '@tanstack/react-query'
import { createContext, createElement, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
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
  | { status: 'restore-error'; message: string }
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
const RESTORE_ERROR_MESSAGE = '无法验证登录状态，请重试。'

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
  await apiRequest<void>('/api/v1/auth/password-resets/confirm', jsonRequest(input), {
    unauthorized: 'suppress',
  })
}

export function SessionProvider({ children, initialState }: SessionProviderProps) {
  const queryClient = useQueryClient()
  const [state, setState] = useState<SessionState>(initialState ?? { status: 'restoring' })
  const mountedRef = useRef(false)
  const operationRef = useRef(0)

  const commitLatest = useCallback(
    (operation: number, nextState: SessionState, clearQueries: boolean) => {
      if (!mountedRef.current || operationRef.current !== operation) return
      if (clearQueries) queryClient.clear()
      setState(nextState)
    },
    [queryClient],
  )

  const restore = useCallback(async () => {
    const operation = ++operationRef.current
    if (mountedRef.current) setState({ status: 'restoring' })
    try {
      const subject = await fetchCurrentSubject()
      commitLatest(operation, stateForSubject(subject), true)
    } catch (error) {
      if (!mountedRef.current || operationRef.current !== operation) return
      if (error instanceof ApiError && error.kind === 'unauthenticated') {
        commitLatest(operation, { status: 'anonymous' }, true)
        return
      }
      commitLatest(operation, { status: 'restore-error', message: RESTORE_ERROR_MESSAGE }, false)
    }
  }, [commitLatest])

  const login = useCallback(async (username: string, password: string) => {
    const operation = ++operationRef.current
    await initializeAnonymousCSRF()
    const input: LoginRequest = { username, password }
    await apiRequest<LoginResponse>('/api/v1/auth/login', jsonRequest(input), {
      unauthorized: 'suppress',
    })
    const subject = await fetchCurrentSubject()
    commitLatest(operation, stateForSubject(subject), true)
  }, [commitLatest])

  const changePassword = useCallback(async (oldPassword: string, newPassword: string) => {
    const operation = ++operationRef.current
    const input: ChangePasswordRequest = { old_password: oldPassword, new_password: newPassword }
    await apiRequest<void>('/api/v1/auth/change-password', jsonRequest(input), {
      unauthorized: 'suppress',
    })
    commitLatest(operation, { status: 'anonymous' }, true)
  }, [commitLatest])

  const logout = useCallback(async () => {
    const operation = ++operationRef.current
    await apiRequest<void>('/api/v1/auth/logout', { method: 'POST' })
    commitLatest(operation, { status: 'anonymous' }, true)
  }, [commitLatest])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      operationRef.current += 1
    }
  }, [])

  useEffect(
    () =>
      subscribeToUnauthorized(() => {
        operationRef.current += 1
        if (!mountedRef.current) return
        queryClient.clear()
        setState({ status: 'anonymous' })
      }),
    [queryClient],
  )

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
