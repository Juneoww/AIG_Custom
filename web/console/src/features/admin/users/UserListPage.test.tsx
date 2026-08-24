/**
 * 功能：验证管理员用户台账的真实读取与受治理写操作。
 * 实现：在 Query、会话、主题和内存路由中驱动页面，不伪造角色或 API 返回。
 * 输入：管理员身份、用户分页响应和 204/201 写操作响应。
 * 输出：原生用户表格、单次禁用请求与不回显临时密码的创建流程。
 * 依赖：Testing Library、TanStack Query、Fluent UI 与用户 API 适配层。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { SessionProvider, type SessionState } from '../../auth/session'
import { ThemeProvider } from '../../../shared/theme/ThemeProvider'
import { UserListPage } from './UserListPage'

const adminState: SessionState = {
  status: 'authenticated',
  subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false },
}

function response(value: unknown, status = 200): Response {
  return new Response(value === undefined ? null : JSON.stringify(value), {
    status,
    headers: value === undefined ? undefined : { 'Content-Type': 'application/json' },
  })
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <ThemeProvider initialMode="light">
      <QueryClientProvider client={client}>
        <SessionProvider initialState={adminState}>
          <MemoryRouter initialEntries={['/admin/users']}><UserListPage /></MemoryRouter>
        </SessionProvider>
      </QueryClientProvider>
    </ThemeProvider>,
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'aig_csrf=; Max-Age=0; Path=/'
})

describe('UserListPage', () => {
  it('展示原生用户台账并以单次 204 禁用当前行', async () => {
    document.cookie = 'aig_csrf=page-token; Path=/'
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({
        items: [{ id: 'user-1', username: 'alice', role: 'user', active: true, must_change_password: false, created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z' }],
        total: 1, page: 1, page_size: 20,
      }))
      .mockResolvedValueOnce(response(undefined, 204))
    vi.stubGlobal('fetch', fetchMock)

    renderPage()
    await screen.findByRole('table', { name: '用户台账' })
    expect(screen.getByText('alice')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '禁用 alice' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(String(fetchMock.mock.calls[1]?.[0])).toContain('/api/v1/platform/admin/users/user-1/active')
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({ method: 'PUT' })
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).get('X-CSRF-Token')).toBe('page-token')
  })

  it('创建用户后清除临时密码且不把密码写入台账', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: [], total: 0, page: 1, page_size: 20 }))
      .mockResolvedValueOnce(response({
        id: 'user-2', username: 'bob', role: 'auditor', active: true, must_change_password: true,
        created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
      }, 201))
    vi.stubGlobal('fetch', fetchMock)

    renderPage()
    await screen.findByRole('button', { name: '新增用户' })
    fireEvent.click(screen.getByRole('button', { name: '新增用户' }))
    fireEvent.change(screen.getByLabelText('用户名'), { target: { value: 'bob' } })
    fireEvent.change(screen.getByLabelText('临时密码'), { target: { value: 'one-time-only' } })
    fireEvent.change(screen.getByLabelText('角色'), { target: { value: 'auditor' } })
    fireEvent.click(screen.getByRole('button', { name: '确认创建' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(screen.queryByDisplayValue('one-time-only')).toBeNull()
    expect(screen.queryByText('one-time-only')).toBeNull()
  })
})
