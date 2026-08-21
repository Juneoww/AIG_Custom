/**
 * 功能：验证品牌、系统、个人资料与关于页面的受治理浏览器边界。
 * 实现：以真实会话、公开品牌和 HTTP 响应驱动页面，锁定写权限、Logo 类型与安全版本读取。
 * 输入：管理员、审计员与普通用户会话，以及品牌、同步状态、公开版本响应。
 * 输出：受角色限制的控制、固定错误提示和不含远程版本信息的关于页。
 * 依赖：Testing Library、React Query、Fluent UI、公共品牌与管理 API 适配层。
 */
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ProfilePage } from '../profile/ProfilePage'
import { PublicBrandProvider } from '../../shared/brand/PublicBrandProvider'
import { ThemeProvider } from '../../shared/theme/ThemeProvider'
import { SessionProvider, type SessionState } from '../auth/session'
import { BrandSettingsPage } from './brand/BrandSettingsPage'
import { SystemPage } from './system/SystemPage'
import { AboutPage } from '../about/AboutPage'

const adminState: SessionState = { status: 'authenticated', subject: { id: 'admin-1', username: 'security-admin', role: 'admin', must_change_password: false } }
const auditorState: SessionState = { status: 'authenticated', subject: { id: 'auditor-1', username: 'audit-reader', role: 'auditor', must_change_password: false } }
const userState: SessionState = { status: 'authenticated', subject: { id: 'user-1', username: 'alice', role: 'user', must_change_password: false } }

function response(value: unknown, status = 200): Response {
  return new Response(value === undefined ? null : JSON.stringify(value), {
    status,
    headers: value === undefined ? undefined : { 'Content-Type': 'application/json' },
  })
}

function renderPage(page: ReactNode, state: SessionState = adminState) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <ThemeProvider initialMode="light">
      <QueryClientProvider client={client}>
        <SessionProvider initialState={state}>
          <PublicBrandProvider initialConfig={{ product_name: '受治理平台', primary_color: '#2255aa', logo_data_url: '' }}>
            <MemoryRouter>{page}</MemoryRouter>
          </PublicBrandProvider>
        </SessionProvider>
      </QueryClientProvider>
    </ThemeProvider>,
  )
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'aig_csrf=; Max-Age=0; Path=/'
})

describe('Task16 governed pages', () => {
  it('品牌设置立即拒绝 SVG，且不会发送品牌写请求', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response({ product_name: '受治理平台', primary_color: '#2255aa', logo: '', logo_mime: '', watermark: '' }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(<BrandSettingsPage />)
    await screen.findByLabelText('产品名称')
    const svg = new File(['<svg><script>alert(1)</script></svg>'], 'unsafe.svg', { type: 'image/svg+xml' })
    fireEvent.change(screen.getByLabelText('品牌 Logo 文件'), { target: { files: [svg] } })
    expect(await screen.findByRole('alert')).toHaveTextContent('仅支持 PNG 或 JPEG 图片')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('管理员保存品牌时使用当前 CSRF 且只提交品牌白名单字段', async () => {
    document.cookie = 'aig_csrf=brand-token; Path=/'
    const config = { product_name: '受治理平台', primary_color: '#2255aa', logo: '', logo_mime: '', watermark: '' }
    const fetchMock = vi.fn().mockResolvedValueOnce(response(config)).mockResolvedValueOnce(response({ ...config, product_name: '区域安全平台' }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(<BrandSettingsPage />)
    const name = await screen.findByLabelText('产品名称')
    fireEvent.change(name, { target: { value: '区域安全平台' } })
    fireEvent.click(screen.getByRole('button', { name: '保存品牌设置' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(fetchMock.mock.calls[1]?.[1]).toMatchObject({ method: 'PUT' })
    expect(new Headers(fetchMock.mock.calls[1]?.[1]?.headers).get('X-CSRF-Token')).toBe('brand-token')
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({
      product_name: '区域安全平台', primary_color: '#2255aa', watermark: '', logo: '', logo_mime: '',
    })
  })

  it('审计员只能查看数据同步状态，管理员才能受 CSRF 保护地发起同步', async () => {
    const status = { status: 0, data: { running: false, success: true, started_at: '2026-01-01T00:00:00Z', finished_at: '2026-01-01T00:01:00Z', message: '同步完成', files_updated: 3, ref: 'main' } }
    const auditFetch = vi.fn().mockResolvedValue(response(status))
    vi.stubGlobal('fetch', auditFetch)
    renderPage(<SystemPage />, auditorState)
    await screen.findByText('同步完成')
    expect(screen.queryByRole('button', { name: '发起数据同步' })).toBeNull()

    document.cookie = 'aig_csrf=system-token; Path=/'
    const adminFetch = vi.fn().mockResolvedValueOnce(response(status)).mockResolvedValueOnce(response(status))
    vi.stubGlobal('fetch', adminFetch)
    renderPage(<SystemPage />, adminState)
    await screen.findAllByText('同步完成')
    fireEvent.click(screen.getAllByRole('button', { name: '发起数据同步' })[0]!)
    await waitFor(() => expect(adminFetch).toHaveBeenCalledTimes(2))
    expect(adminFetch.mock.calls[1]?.[1]).toMatchObject({ method: 'POST' })
    expect(new Headers(adminFetch.mock.calls[1]?.[1]?.headers).get('X-CSRF-Token')).toBe('system-token')
  })

  it('个人资料只展示会话白名单身份并提供站内改密入口', () => {
    renderPage(<ProfilePage />, userState)
    expect(screen.getByText('alice')).toBeTruthy()
    expect(screen.getByText('普通用户')).toBeTruthy()
    expect(screen.getByRole('link', { name: '修改密码' })).toHaveAttribute('href', '/change-password')
  })

  it('关于页只读取本地安全版本 DTO 与配置产品名，不触发远程更新检查', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response({ version: 'v1.2.3', commit: 'abc123', build_time: '2026-01-01T00:00:00Z' }))
    vi.stubGlobal('fetch', fetchMock)
    renderPage(<AboutPage />)
    expect(await screen.findByText('v1.2.3')).toBeTruthy()
    expect(screen.getByText('受治理平台')).toBeTruthy()
    expect(String(fetchMock.mock.calls[0]?.[0])).toContain('/api/v1/version')
    expect(String(fetchMock.mock.calls[0]?.[0])).not.toContain('/system/version')
  })
})
