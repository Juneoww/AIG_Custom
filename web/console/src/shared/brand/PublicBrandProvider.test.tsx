/**
 * 功能：验证公开品牌配置在登录前和应用壳间安全共享。
 * 实现：用真实 TanStack Query 边界请求匿名品牌，并检查去重、回退和 Logo 白名单。
 * 输入：后端 public/brand 三字段响应或固定失败响应。
 * 输出：仅产品名与安全 Logo 的品牌上下文断言。
 * 依赖：Vitest、Testing Library、TanStack Query 与公开品牌提供器。
 */
import { QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { createAppQueryClient } from '../../app/providers/AppProviders'
import { PublicBrandProvider, usePublicBrand } from './PublicBrandProvider'

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function BrandProbe({ label }: { label: string }) {
  const brand = usePublicBrand()
  return (
    <div aria-label={label}>
      <span>{brand.productName}</span>
      <output aria-label={`${label} Logo`}>{brand.logoDataURL}</output>
      <output aria-label={`${label} 字段`}>{Object.keys(brand).sort().join(',')}</output>
    </div>
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('PublicBrandProvider', () => {
  it('为多个消费者只请求一次公开品牌并忽略主色字段', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse({
        product_name: '企业模型安全监管平台',
        primary_color: '#FF00FF',
        logo_data_url: 'data:image/png;base64,AA==',
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    render(
      <QueryClientProvider client={createAppQueryClient()}>
        <PublicBrandProvider>
          <BrandProbe label="登录页品牌" />
          <BrandProbe label="应用壳品牌" />
        </PublicBrandProvider>
      </QueryClientProvider>,
    )

    expect(await screen.findAllByText('企业模型安全监管平台')).toHaveLength(2)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`${window.location.origin}/api/v1/public/brand`)
    expect(screen.getByLabelText('登录页品牌 字段')).toHaveTextContent('logoDataURL,productName')
  })

  it('请求失败时使用固定中文产品名且不重试', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 500 }))
    vi.stubGlobal('fetch', fetchMock)

    render(
      <QueryClientProvider client={createAppQueryClient()}>
        <PublicBrandProvider>
          <BrandProbe label="失败品牌" />
        </PublicBrandProvider>
      </QueryClientProvider>,
    )

    expect(screen.getByText('AI 安全治理平台')).toBeInTheDocument()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText('失败品牌 Logo')).toHaveTextContent('')
  })

  it('拒绝非 PNG 或 JPEG 的 Logo data URL', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse({
          product_name: '安全平台',
          primary_color: '#1677FF',
          logo_data_url: 'data:text/html;base64,PHNjcmlwdD4=',
        }),
      ),
    )

    render(
      <QueryClientProvider client={createAppQueryClient()}>
        <PublicBrandProvider>
          <BrandProbe label="不安全品牌" />
        </PublicBrandProvider>
      </QueryClientProvider>,
    )

    expect(await screen.findByText('安全平台')).toBeInTheDocument()
    expect(screen.getByLabelText('不安全品牌 Logo')).toHaveTextContent('')
  })
})
