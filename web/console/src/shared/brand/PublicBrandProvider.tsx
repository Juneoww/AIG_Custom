/**
 * 功能：向登录页和应用壳共享匿名安全品牌配置。
 * 实现：用单一 TanStack Query 请求公开三字段 DTO，仅投影产品名和安全 Logo。
 * 输入：GET /api/v1/public/brand 响应与可选测试初始品牌。
 * 输出：固定回退产品名及 PNG/JPEG data URL 品牌上下文。
 * 依赖：React、TanStack Query、共享 API 客户端与公开品牌 DTO。
 */
import { useQuery } from '@tanstack/react-query'
import { createContext, useContext, useMemo, type ReactNode } from 'react'

import { apiRequest } from '../api/client'
import type { PublicBrandConfig } from '../api/types'

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
const SAFE_LOGO_PATTERN = /^data:image\/(?:png|jpeg);base64,[A-Za-z0-9+/]+={0,2}$/

function projectPublicBrand(config: PublicBrandConfig | undefined): PublicBrand {
  const productName = config?.product_name.trim() || DEFAULT_PUBLIC_BRAND.productName
  const logoDataURL =
    config && SAFE_LOGO_PATTERN.test(config.logo_data_url) ? config.logo_data_url : ''
  return { productName, logoDataURL }
}

export function PublicBrandProvider({ children, initialConfig }: PublicBrandProviderProps) {
  const query = useQuery({
    queryKey: ['public-brand'],
    queryFn: () =>
      apiRequest<PublicBrandConfig>(
        '/api/v1/public/brand',
        {},
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
