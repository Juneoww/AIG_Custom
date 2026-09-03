/**
 * 功能：提供扫描任务选择受治理模型时的安全目录规则。
 * 实现：仅保留启用模型，按白名单生成标签，并保护分页不会无限推进。
 * 输入：模型目录 API 已校验的模型项与分页元数据。
 * 输出：可安全展示的模型项、标签和下一页编号。
 * 依赖：模型目录 DTO。
 */
import type { ModelCatalogItem, ModelCatalogPage } from '../models/api'

export const MODEL_CATALOG_PAGE_SIZE = 100
const MAX_MODEL_CATALOG_PAGE = 1_000

type CatalogFingerprintItem = Pick<ModelCatalogItem, 'id' | 'name' | 'provider_model' | 'scope' | 'disabled' | 'source' | 'read_only'>

export function catalogPageFingerprint(items: readonly CatalogFingerprintItem[]): string {
  const tuples = items.map((model) => [
    model.id,
    model.name,
    model.provider_model,
    model.scope,
    model.disabled,
    model.source,
    model.read_only,
  ])
  tuples.sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)))
  return JSON.stringify(tuples)
}

export function hasRepeatedCatalogPage(
  items: readonly CatalogFingerprintItem[],
  acceptedPages: readonly (readonly CatalogFingerprintItem[])[],
): boolean {
  const fingerprint = catalogPageFingerprint(items)
  return acceptedPages.some((page) => catalogPageFingerprint(page) === fingerprint)
}

export function canonicalModels(items: readonly ModelCatalogItem[]): readonly ModelCatalogItem[] {
  const seenIDs = new Set<string>()
  return items.filter((model) => {
    if (seenIDs.has(model.id)) return false
    seenIDs.add(model.id)
    return true
  })
}

export function selectableModels(items: readonly ModelCatalogItem[]): readonly ModelCatalogItem[] {
  return canonicalModels(items).filter((model) => !model.disabled)
}

export function modelOptionLabel(model: Pick<ModelCatalogItem, 'name' | 'provider_model' | 'scope'>): string {
  return `${model.name}（${model.provider_model}，${model.scope === 'private' ? '私有' : '全局'}）`
}

export function nextCatalogPage(
  page: Pick<ModelCatalogPage, 'page' | 'page_size' | 'total'>,
  loadedPageParams: readonly number[] = [],
): number | undefined {
  if (!Number.isSafeInteger(page.page) || !Number.isSafeInteger(page.page_size) || !Number.isSafeInteger(page.total) ||
    page.page < 1 || page.page > MAX_MODEL_CATALOG_PAGE || page.page_size < 1 || page.page_size > MODEL_CATALOG_PAGE_SIZE ||
    page.total < 1 || page.page * page.page_size >= page.total) return undefined
  const nextPage = page.page + 1
  return nextPage <= MAX_MODEL_CATALOG_PAGE && !loadedPageParams.includes(nextPage) ? nextPage : undefined
}
