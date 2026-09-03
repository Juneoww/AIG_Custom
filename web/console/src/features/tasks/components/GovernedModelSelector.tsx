/**
 * 功能：为扫描任务从受治理模型目录选择一个模型 ID。
 * 实现：分页读取安全目录，仅展示白名单标签，并保留无模型与已删除模型的受控状态。
 * 输入：当前 opaque 模型 ID、变更回调和可选禁用状态。
 * 输出：模型 ID 或 undefined；不输出模型凭据、地址或备注。
 * 依赖：Fluent UI、TanStack Query、React Router 与模型目录 API。
 */
import { Button, Field, MessageBar, MessageBarBody, Select, makeStyles, tokens } from '@fluentui/react-components'
import { useInfiniteQuery } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'

import { fetchModelCatalog } from '../../models/api'
import { ApiError } from '../../../shared/api/errors'
import { MODEL_CATALOG_PAGE_SIZE, modelOptionLabel, nextCatalogPage, selectableModels } from '../governedModels'

const useStyles = makeStyles({
  container: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalS },
  actions: { display: 'flex', alignItems: 'center', gap: tokens.spacingHorizontalS },
})

export interface GovernedModelSelectorProps {
  value?: string
  onChange: (modelID: string | undefined) => void
  disabled?: boolean
}

export function GovernedModelSelector({ value, onChange, disabled = false }: GovernedModelSelectorProps) {
  const styles = useStyles()
  const clearedModelIDRef = useRef<string | undefined>(undefined)
  const catalog = useInfiniteQuery({
    queryKey: ['governed-model-catalog'],
    initialPageParam: 1,
    queryFn: async ({ pageParam, signal }) => {
      const page = await fetchModelCatalog({ page: pageParam, pageSize: MODEL_CATALOG_PAGE_SIZE }, signal)
      if (page.page !== pageParam || page.page_size !== MODEL_CATALOG_PAGE_SIZE) throw new ApiError('unexpected-response', 200)
      return page
    },
    getNextPageParam: (lastPage, _allPages, lastPageParam, allPageParams) =>
      lastPage.page === lastPageParam ? nextCatalogPage(lastPage, allPageParams) : undefined,
    retry: false,
  })
  const models = selectableModels(catalog.data?.pages.flatMap((page) => page.items) ?? [])
  const selectedModelID = typeof value === 'string' && value !== '' ? value : undefined
  const selectedIsAvailable = selectedModelID !== undefined && models.some((model) => model.id === selectedModelID)
  const firstPageLoaded = catalog.data?.pageParams[0] === 1 && catalog.data.pages[0] !== undefined
  const unavailableSelectedModel = firstPageLoaded && selectedModelID !== undefined && !selectedIsAvailable
  const firstPageFailed = catalog.isError && !catalog.data
  const emptyCatalog = Boolean(catalog.data) && models.length === 0

  useEffect(() => {
    if (unavailableSelectedModel && clearedModelIDRef.current !== selectedModelID) {
      clearedModelIDRef.current = selectedModelID
      onChange(undefined)
      return
    }
    if (!unavailableSelectedModel) clearedModelIDRef.current = undefined
  }, [onChange, selectedModelID, unavailableSelectedModel])

  return (
    <Field label="扫描模型">
      <div className={styles.container}>
        {catalog.isPending ? <span role="status">正在加载模型…</span> : null}
        {firstPageFailed ? (
          <MessageBar intent="error">
            <MessageBarBody>模型目录加载失败</MessageBarBody>
            <Button appearance="transparent" disabled={catalog.isFetching} onClick={() => void catalog.refetch()}>
              {catalog.isFetching ? '正在重试模型…' : '重试加载模型'}
            </Button>
          </MessageBar>
        ) : null}
        <Select
          aria-label="扫描模型"
          value={value ?? ''}
          disabled={disabled || catalog.isPending}
          onChange={(_, data) => onChange(data.value || undefined)}
        >
          <option value="">不使用模型</option>
          {models.map((model) => <option key={model.id} value={model.id}>{modelOptionLabel(model)}</option>)}
        </Select>
        {unavailableSelectedModel ? <MessageBar intent="warning"><MessageBarBody>已选模型不可用，已清除选择。</MessageBarBody></MessageBar> : null}
        {emptyCatalog ? <Link to="/models">前往凭证配置 → 模型配置</Link> : null}
        {catalog.isFetchNextPageError ? (
          <MessageBar intent="error">
            <MessageBarBody>加载更多模型失败</MessageBarBody>
            <Button appearance="transparent" disabled={catalog.isFetchingNextPage} onClick={() => void catalog.fetchNextPage()}>
              {catalog.isFetchingNextPage ? '正在重试更多模型…' : '重试加载更多模型'}
            </Button>
          </MessageBar>
        ) : null}
        {catalog.hasNextPage && !catalog.isFetchNextPageError ? (
          <div className={styles.actions}>
            <Button disabled={disabled || catalog.isFetchingNextPage} onClick={() => void catalog.fetchNextPage()}>
              {catalog.isFetchingNextPage ? '正在加载更多模型…' : '加载更多模型'}
            </Button>
          </div>
        ) : null}
      </div>
    </Field>
  )
}
