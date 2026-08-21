/**
 * 功能：验证审计台账只呈现治理所需字段，并允许审计员按服务端分页读取。
 * 实现：通过真实安全 DTO 响应驱动页面，断言 metadata、IP 与请求标识不进入 DOM。
 * 输入：审计员会话、审计分页响应与可选 action 筛选。
 * 输出：原生审计表格及受控筛选请求。
 * 依赖：Testing Library、TanStack Query、Fluent UI 与审计 API 适配层。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { SessionProvider, type SessionState } from '../../auth/session'
import { ThemeProvider } from '../../../shared/theme/ThemeProvider'
import { AuditListPage } from './AuditListPage'

const auditorState: SessionState = {
  status: 'authenticated',
  subject: { id: 'auditor-1', username: 'audit-reader', role: 'auditor', must_change_password: false },
}

function response(value: unknown): Response {
  return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } })
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <ThemeProvider initialMode="light">
      <QueryClientProvider client={client}>
        <SessionProvider initialState={auditorState}>
          <MemoryRouter initialEntries={['/admin/audit']}><AuditListPage /></MemoryRouter>
        </SessionProvider>
      </QueryClientProvider>
    </ThemeProvider>,
  )
}

afterEach(() => vi.unstubAllGlobals())

describe('AuditListPage', () => {
  it('审计员可读取服务端分页台账，但敏感 metadata 不会进入页面', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response({
      items: [{
        id: 'audit-1', occurred_at: '2026-01-01T00:00:00Z', actor_user_id: 'admin-1', actor_username: 'security-admin',
        actor_role: 'admin', action: 'user.updated', resource_type: 'user', resource_id: 'user-1', outcome: 'success',
        metadata: { token: 'do-not-render', storage_path: '/private/audit' }, client_ip: '198.51.100.7', request_id: 'request-secret',
      }], total: 1, page: 1, page_size: 20,
    }))
    vi.stubGlobal('fetch', fetchMock)

    renderPage()
    await screen.findByRole('table', { name: '治理审计台账' })
    expect(screen.getByText('user.updated')).toBeTruthy()
    expect(screen.queryByText('do-not-render')).toBeNull()
    expect(screen.queryByText('/private/audit')).toBeNull()
    expect(screen.queryByText('198.51.100.7')).toBeNull()
    expect(screen.queryByText('request-secret')).toBeNull()
  })

  it('筛选动作只写入服务端查询参数，不在浏览器端过滤已返回的记录', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response({ items: [], total: 0, page: 1, page_size: 20 }))
      .mockResolvedValueOnce(response({ items: [], total: 0, page: 1, page_size: 20 }))
    vi.stubGlobal('fetch', fetchMock)

    renderPage()
    await screen.findByRole('button', { name: '应用筛选' })
    fireEvent.change(screen.getByLabelText('动作筛选'), { target: { value: 'task.created' } })
    fireEvent.click(screen.getByRole('button', { name: '应用筛选' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(String(fetchMock.mock.calls[1]?.[0])).toContain('action=task.created')
  })
})
