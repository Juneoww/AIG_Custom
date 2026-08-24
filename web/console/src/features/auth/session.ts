/**
 * 功能：管理浏览器身份会话及登录、改密、退出和重置流程。
 * 实现：以 React 上下文保存 Subject，并通过真实 Cookie API 合同迁移状态。
 * 输入：用户凭据、服务端身份 DTO 与可选测试初始状态。
 * 输出：匿名、认证、强制改密、恢复中或安全恢复失败会话状态。
 * 依赖：React、TanStack Query、共享 API 客户端与身份 DTO；不使用浏览器持久化存储。
 */
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { createContext, createElement, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'

import {
  apiRequest,
  defaultAuthorizationGeneration,
  subscribeToUnauthorized,
  type AuthorizationGeneration,
} from '../../shared/api/client'
import { ApiError, NetworkError } from '../../shared/api/errors'
import type {
  CSRFResponse,
  ChangePasswordRequest,
  CurrentSubject,
  LoginRequest,
  LoginResponse,
  PasswordResetConfirmRequest,
} from '../../shared/api/types'
import { isPublicBrandQueryKey } from '../../shared/brand/query'

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
  authorizationGeneration?: AuthorizationGeneration
  children: ReactNode
  initialState?: SessionState
}

const SessionContext = createContext<SessionContextValue | undefined>(undefined)
const RESTORE_ERROR_MESSAGE = '无法验证登录状态，请重试。'

function clearIdentityBoundCaches(queryClient: QueryClient): void {
  queryClient.removeQueries({ predicate: ({ queryKey }) => !isPublicBrandQueryKey(queryKey) })
  queryClient.getMutationCache().clear()
}

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

function ensureNotAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw new NetworkError()
}

async function initializeAnonymousCSRF(
  signal?: AbortSignal,
  authorization: AuthorizationGeneration = defaultAuthorizationGeneration,
): Promise<void> {
  await apiRequest<CSRFResponse>('/api/v1/auth/csrf', { signal }, { authorization, unauthorized: 'suppress' })
}

async function fetchCurrentSubject(
  signal: AbortSignal,
  authorization: AuthorizationGeneration,
): Promise<CurrentSubject> {
  return apiRequest<CurrentSubject>('/api/v1/auth/me', { signal }, { authorization, unauthorized: 'suppress' })
}

export async function confirmPasswordReset(
  input: PasswordResetConfirmRequest,
  signal?: AbortSignal,
): Promise<void> {
  await initializeAnonymousCSRF(signal)
  ensureNotAborted(signal)
  await apiRequest<void>(
    '/api/v1/auth/password-resets/confirm',
    { ...jsonRequest(input), signal },
    { unauthorized: 'suppress' },
  )
}

interface SessionOperation {
  controller: AbortController
  id: number
}

export function SessionProvider({
  authorizationGeneration = defaultAuthorizationGeneration,
  children,
  initialState,
}: SessionProviderProps) {
  const queryClient = useQueryClient()
  const [state, setState] = useState<SessionState>(initialState ?? { status: 'restoring' })
  const mountedRef = useRef(false)
  const operationRef = useRef(0)
  const controllerRef = useRef<AbortController | null>(null)

  const beginOperation = useCallback((): SessionOperation => {
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    return { controller, id: ++operationRef.current }
  }, [])

  const isLatest = useCallback(
    (operation: SessionOperation) =>
      mountedRef.current &&
      operationRef.current === operation.id &&
      controllerRef.current === operation.controller &&
      !operation.controller.signal.aborted,
    [],
  )

  const finishLatest = useCallback(
    (operation: SessionOperation) => {
      if (!isLatest(operation)) return false
      controllerRef.current = null
      return true
    },
    [isLatest],
  )

  const commitLatest = useCallback(
    (operation: SessionOperation, nextState: SessionState, clearQueries: boolean) => {
      if (!finishLatest(operation)) return false
      authorizationGeneration.advance()
      if (clearQueries) clearIdentityBoundCaches(queryClient)
      setState(nextState)
      return true
    },
    [authorizationGeneration, finishLatest, queryClient],
  )

  const restore = useCallback(async () => {
    const operation = beginOperation()
    if (mountedRef.current) setState({ status: 'restoring' })
    try {
      const subject = await fetchCurrentSubject(operation.controller.signal, authorizationGeneration)
      commitLatest(operation, stateForSubject(subject), true)
    } catch (error) {
      if (!isLatest(operation)) return
      if (error instanceof ApiError && error.kind === 'unauthenticated') {
        commitLatest(operation, { status: 'anonymous' }, true)
        return
      }
      commitLatest(operation, { status: 'restore-error', message: RESTORE_ERROR_MESSAGE }, true)
    }
  }, [authorizationGeneration, beginOperation, commitLatest, isLatest])

  const login = useCallback(async (username: string, password: string) => {
    const operation = beginOperation()
    try {
      await initializeAnonymousCSRF(operation.controller.signal, authorizationGeneration)
    } catch (error) {
      if (!finishLatest(operation)) return
      throw error
    }
    if (!isLatest(operation)) return

    const input: LoginRequest = { username, password }
    try {
      await apiRequest<LoginResponse>(
        '/api/v1/auth/login',
        { ...jsonRequest(input), signal: operation.controller.signal },
        { authorization: authorizationGeneration, unauthorized: 'suppress' },
      )
    } catch (error) {
      if (!finishLatest(operation)) return
      throw error
    }
    if (!isLatest(operation)) return

    try {
      const subject = await fetchCurrentSubject(operation.controller.signal, authorizationGeneration)
      commitLatest(operation, stateForSubject(subject), true)
    } catch (error) {
      if (!isLatest(operation)) return
      if (error instanceof ApiError && error.kind === 'unauthenticated') {
        commitLatest(operation, { status: 'anonymous' }, true)
      } else {
        finishLatest(operation)
      }
      throw error
    }
  }, [authorizationGeneration, beginOperation, commitLatest, finishLatest, isLatest])

  const changePassword = useCallback(async (oldPassword: string, newPassword: string) => {
    const operation = beginOperation()
    const input: ChangePasswordRequest = { old_password: oldPassword, new_password: newPassword }
    try {
      await apiRequest<void>(
        '/api/v1/auth/change-password',
        { ...jsonRequest(input), signal: operation.controller.signal },
        { authorization: authorizationGeneration, unauthorized: 'suppress' },
      )
      commitLatest(operation, { status: 'anonymous' }, true)
      return
    } catch (error) {
      if (!isLatest(operation)) return
      if (!(error instanceof ApiError) || error.kind !== 'unauthenticated') {
        finishLatest(operation)
        throw error
      }
    }

    try {
      await fetchCurrentSubject(operation.controller.signal, authorizationGeneration)
    } catch (checkError) {
      if (!isLatest(operation)) return
      if (checkError instanceof ApiError && checkError.kind === 'unauthenticated') {
        commitLatest(operation, { status: 'anonymous' }, true)
      } else {
        commitLatest(operation, { status: 'restore-error', message: RESTORE_ERROR_MESSAGE }, true)
      }
      throw checkError
    }

    if (!finishLatest(operation)) return
    throw new ApiError('unauthenticated', 401)
  }, [authorizationGeneration, beginOperation, commitLatest, finishLatest, isLatest])

  const logout = useCallback(async () => {
    const operation = beginOperation()
    try {
      await apiRequest<void>(
        '/api/v1/auth/logout',
        { method: 'POST', signal: operation.controller.signal },
        { authorization: authorizationGeneration },
      )
    } catch (error) {
      if (!finishLatest(operation)) return
      throw error
    }
    commitLatest(operation, { status: 'anonymous' }, true)
  }, [authorizationGeneration, beginOperation, commitLatest, finishLatest])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      operationRef.current += 1
      controllerRef.current?.abort()
      controllerRef.current = null
    }
  }, [])

  useEffect(
    () =>
      subscribeToUnauthorized((event) => {
        if (
          event.authorization !== authorizationGeneration ||
          event.generation !== authorizationGeneration.current()
        ) {
          return
        }
        controllerRef.current?.abort()
        controllerRef.current = null
        operationRef.current += 1
        if (!mountedRef.current) return
        authorizationGeneration.advance()
        clearIdentityBoundCaches(queryClient)
        setState({ status: 'anonymous' })
      }),
    [authorizationGeneration, queryClient],
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
