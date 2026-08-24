/**
 * 功能：向登录页和应用壳共享匿名安全品牌配置。
 * 实现：用单一 TanStack Query 请求未知响应，按后端边界校验后仅投影产品名和安全 Logo。
 * 输入：GET /api/v1/public/brand 未知响应与可选测试初始品牌。
 * 输出：固定回退产品名及 PNG/JPEG data URL 品牌上下文。
 * 依赖：React、TanStack Query、共享 API 客户端与公开品牌 DTO。
 */
import { useQuery } from '@tanstack/react-query'
import { createContext, useContext, useMemo, type ReactNode } from 'react'

import { apiRequest } from '../api/client'
import type { PublicBrandConfig } from '../api/types'
import { PUBLIC_BRAND_QUERY_KEY } from './query'

export interface PublicBrand {
  productName: string
  logoDataURL: string
}

interface PublicBrandProviderProps {
  children: ReactNode
  initialConfig?: PublicBrandConfig
}

export const DEFAULT_PUBLIC_BRAND: PublicBrand = {
  productName: 'AI 安全治理平台',
  logoDataURL: '',
}

const BrandContext = createContext<PublicBrand>(DEFAULT_PUBLIC_BRAND)
const SAFE_PRIMARY_COLOR_PATTERN = /^#[0-9A-Fa-f]{6}$/
const SAFE_LOGO_PATTERN =
  /^data:image\/(?:png|jpeg);base64,((?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?)$/
const MAX_PRODUCT_NAME_CODE_POINTS = 128
const MAX_LOGO_BYTES = 1024 * 1024

function hasPublicBrandShape(config: unknown): config is PublicBrandConfig {
  if (typeof config !== 'object' || config === null) {
    return false
  }
  const candidate = config as Record<string, unknown>
  return (
    typeof candidate.product_name === 'string' &&
    typeof candidate.primary_color === 'string' &&
    SAFE_PRIMARY_COLOR_PATTERN.test(candidate.primary_color) &&
    typeof candidate.logo_data_url === 'string'
  )
}

function safeLogoDataURL(value: string): string {
  if (value === '') {
    return ''
  }
  const match = SAFE_LOGO_PATTERN.exec(value)
  const payload = match?.[1]
  if (!payload) {
    return ''
  }
  const padding = payload.endsWith('==') ? 2 : payload.endsWith('=') ? 1 : 0
  const decodedBytes = (payload.length / 4) * 3 - padding
  return decodedBytes <= MAX_LOGO_BYTES ? value : ''
}

function projectPublicBrand(config: unknown): PublicBrand {
  if (!hasPublicBrandShape(config)) {
    return DEFAULT_PUBLIC_BRAND
  }
  const trimmedProductName = config.product_name.trim()
  const productName =
    trimmedProductName.length > 0 &&
    Array.from(trimmedProductName).length <= MAX_PRODUCT_NAME_CODE_POINTS
      ? trimmedProductName
      : DEFAULT_PUBLIC_BRAND.productName
  const logoDataURL = safeLogoDataURL(config.logo_data_url)
  return { productName, logoDataURL }
}

export function PublicBrandProvider({ children, initialConfig }: PublicBrandProviderProps) {
  const query = useQuery({
    queryKey: PUBLIC_BRAND_QUERY_KEY,
    queryFn: ({ signal }) =>
      apiRequest<unknown>(
        '/api/v1/public/brand',
        { signal },
        { unauthorized: 'suppress' },
      ),
    initialData: initialConfig,
    enabled: initialConfig === undefined,
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: Number.POSITIVE_INFINITY,
  })
  const value = useMemo(() => projectPublicBrand(query.data), [query.data])

  return <BrandContext.Provider value={value}>{children}</BrandContext.Provider>
}

export function usePublicBrand(): PublicBrand {
  return useContext(BrandContext)
}
