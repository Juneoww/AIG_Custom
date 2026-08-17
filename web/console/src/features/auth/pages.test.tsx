/**
 * 功能：验证中文身份表单的可访问性、敏感输入边界和固定错误提示。
 * 实现：在隔离会话提供器中模拟提交，检查标签、自动填充和表单状态。
 * 输入：用户键入的账号、密码与手工重置凭据。
 * 输出：页面行为与安全断言。
 * 依赖：React Testing Library、Vitest、Fluent UI 与身份页面。
 */
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { AppProviders, createAppQueryClient } from '../../app/providers/AppProviders'
import { ChangePasswordPage } from './ChangePasswordPage'
import { LoginPage } from './LoginPage'
import { ResetPasswordPage } from './ResetPasswordPage'
import type { SessionState } from './session'

function renderPage(page: React.ReactNode, initialState: SessionState = { status: 'anonymous' }) {
  return render(
    <AppProviders queryClient={createAppQueryClient()} sessionInitialState={initialState}>
      {page}
    </AppProviders>,
  )
}

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

beforeEach(() => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
  window.history.replaceState({}, '', '/')
  document.cookie = 'aig_csrf=; Max-Age=0; Path=/'
})

describe('LoginPage', () => {
  it('提供中文可访问标签和正确的浏览器自动填充语义', () => {
    renderPage(<LoginPage />)

    expect(screen.getByRole('heading', { name: '登录平台' })).toBeInTheDocument()
    expect(screen.getByLabelText(/^用户名/)).toHaveAttribute('autocomplete', 'username')
    expect(screen.getByLabelText(/^密码/)).toHaveAttribute('autocomplete', 'current-password')
    expect(screen.getByRole('button', { name: '登录' })).toBeDisabled()
    expect(screen.queryByText(/^A\.I\.G$/)).not.toBeInTheDocument()
  })

  it('认证失败展示固定中文错误且保留当前输入', async () => {
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(() => {
        document.cookie = 'aig_csrf=bootstrap-token; Path=/'
        return Promise.resolve(jsonResponse({ csrf_token: 'bootstrap-token' }))
      })
      .mockResolvedValueOnce(new Response(null, { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(<LoginPage />)

    fireEvent.change(screen.getByLabelText(/^用户名/), { target: { value: 'operator' } })
    fireEvent.change(screen.getByLabelText(/^密码/), { target: { value: 'incorrect-password' } })
    fireEvent.click(screen.getByRole('button', { name: '登录' }))

    expect(await screen.findByText('用户名或密码不正确。')).toBeInTheDocument()
    expect(screen.getByLabelText(/^用户名/)).toHaveValue('operator')
    expect(screen.getByLabelText(/^密码/)).toHaveValue('incorrect-password')
  })
})

describe('ChangePasswordPage', () => {
  it('要求确认新密码且不会在不一致时调用接口', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    renderPage(<ChangePasswordPage />, {
      status: 'must-change',
      subject: { id: 'user-2', username: 'new-operator', role: 'user', must_change_password: true },
    })

    fireEvent.change(screen.getByLabelText(/^当前密码/), { target: { value: 'temporary-password' } })
    fireEvent.change(screen.getByLabelText(/^新密码/), { target: { value: 'replacement-password' } })
    fireEvent.change(screen.getByLabelText(/^确认新密码/), { target: { value: 'different-password' } })
    fireEvent.click(screen.getByRole('button', { name: '更新密码' }))

    expect(await screen.findByText('两次输入的新密码不一致。')).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('ResetPasswordPage', () => {
  it.each(['/reset-password?token=url-secret', '/reset-password#token=fragment-secret'])(
    '不会从地址 %s 读取或提交重置凭据',
    async (url) => {
      window.history.replaceState({}, '', url)
      const fetchMock = vi.fn()
      vi.stubGlobal('fetch', fetchMock)

      renderPage(<ResetPasswordPage />)

      expect(screen.getByLabelText(/^重置凭据/)).toHaveValue('')
      expect(screen.getByLabelText(/^临时密码/)).toHaveValue('')
      await waitFor(() => expect(fetchMock).not.toHaveBeenCalled())
    },
  )

  it('只把用户手工输入的凭据放入确认请求体且不写入浏览器存储', async () => {
    const localSet = vi.spyOn(Storage.prototype, 'setItem')
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(() => {
        document.cookie = 'aig_csrf=reset-csrf; Path=/'
        return Promise.resolve(jsonResponse({ csrf_token: 'reset-csrf' }))
      })
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(<ResetPasswordPage />)

    fireEvent.change(screen.getByLabelText(/^重置凭据/), { target: { value: 'manually-pasted-token' } })
    fireEvent.change(screen.getByLabelText(/^临时密码/), { target: { value: 'temporary-password' } })
    fireEvent.change(screen.getByLabelText(/^确认临时密码/), { target: { value: 'temporary-password' } })
    fireEvent.click(screen.getByRole('button', { name: '重置密码' }))

    expect(await screen.findByText('密码已重置，请使用临时密码重新登录。')).toBeInTheDocument()
    const request = fetchMock.mock.calls[1]?.[1] as RequestInit
    expect(JSON.parse(String(request.body))).toEqual({
      token: 'manually-pasted-token',
      temporary_password: 'temporary-password',
    })
    expect(localSet).not.toHaveBeenCalled()
    localSet.mockRestore()
  })
})
