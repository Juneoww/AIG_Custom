/**
 * 功能：声明身份边界允许保留的唯一公开品牌查询键。
 * 实现：提供稳定只读键和严格长度匹配，避免前缀查询被误当成公开数据。
 * 输入：TanStack Query 的未知查询键。
 * 输出：是否恰好为 public-brand 查询。
 * 依赖：无运行时依赖。
 */
export const PUBLIC_BRAND_QUERY_KEY = ['public-brand'] as const

export function isPublicBrandQueryKey(queryKey: readonly unknown[]): boolean {
  return queryKey.length === 1 && queryKey[0] === PUBLIC_BRAND_QUERY_KEY[0]
}
