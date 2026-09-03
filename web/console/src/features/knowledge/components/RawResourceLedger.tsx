/**
 * 功能：复用规则资产的分页或完整目录、原文查看与管理员治理流程。
 * 实现：按目录类型隔离 URL 分页，目录仅在当前查询成功时显示；原文仅在页面内按需读取。
 * 输入：资源文案、判别式目录查询函数、白名单列和真实创建/更新/删除函数。
 * 输出：安全目录概览、原生台账、独立状态面板、原文字节编辑器和单次治理请求。
 * 依赖：Fluent UI、TanStack Query、React Router、Session 与共享台账组件。
 */
import {
  Button,
  Dialog,
  DialogActions,
  DialogBody,
  DialogContent,
  DialogSurface,
  DialogTitle,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
  makeStyles,
  tokens,
} from '@fluentui/react-components'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'

import { DataTable, type DataTableColumn } from '../../../shared/components/DataTable'
import { PageHeader } from '../../../shared/components/PageHeader'
import { StatePanel } from '../../../shared/components/StatePanel'
import { ApiError } from '../../../shared/api/errors'
import { useSession } from '../../auth/session'
import type { KnowledgePage, KnowledgePageQuery } from '../api'
import { KnowledgeCatalogBrief, type KnowledgeCatalogScope } from './KnowledgeCatalogBrief'
import { StructuredEditor, type StructuredValidationResult } from './StructuredEditor'

interface LedgerBase<T> {
  resourceKey: string
  title: string
  description: string
  resourceLabel: string
  format: 'yaml' | 'json'
  columns: readonly DataTableColumn<T>[]
  getID: (item: T) => string
  fetchRaw: (id: string, signal?: AbortSignal) => Promise<string>
  createResource: (content: string, signal?: AbortSignal) => Promise<void>
  updateResource: (id: string, content: string, signal?: AbortSignal) => Promise<void>
  deleteResource: (id: string, signal?: AbortSignal) => Promise<void>
  sampleContent?: string
  sampleFileName?: string
  currentFileName?: (id: string) => string
}

interface PaginatedLedgerProps<T> extends LedgerBase<T> {
  catalogMode?: 'paginated'
  fetchPage: (query: KnowledgePageQuery, signal?: AbortSignal) => Promise<KnowledgePage<T>>
}

export interface CompleteCatalog<T> {
  items: T[]
  total: number
}

interface CompleteLedgerProps<T> extends LedgerBase<T> {
  catalogMode: 'complete'
  fetchComplete: (signal?: AbortSignal) => Promise<CompleteCatalog<T>>
}

type RawResourceLedgerProps<T> = PaginatedLedgerProps<T> | CompleteLedgerProps<T>

interface PaginationState {
  page: number
  queryText: string
}

type EditorMode<T> = { kind: 'view' | 'edit'; item: T } | { kind: 'create' }

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL, minWidth: 0, maxWidth: '100%' },
  filters: {
    display: 'flex',
    gap: tokens.spacingHorizontalS,
    alignItems: 'end',
    flexWrap: 'wrap',
    minWidth: 0,
    '@media (max-width: 960px)': { alignItems: 'stretch', flexDirection: 'column' },
  },
  filterField: { flex: '1 1 16rem', minWidth: 0 },
  filterInput: { width: '100%', minWidth: 0 },
  actions: { display: 'flex', gap: tokens.spacingHorizontalXS, flexWrap: 'wrap' },
  editor: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalM, minWidth: 0 },
  tableViewport: { minWidth: 0, maxWidth: '100%', overflowX: 'auto' },
  pagination: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: tokens.spacingHorizontalM,
    minWidth: 0,
    '@media (max-width: 960px)': { alignItems: 'flex-start', flexDirection: 'column' },
  },
})

function normalizedPage(value: string | null): number {
  if (!value || !/^\d+$/.test(value)) return 1
  const page = Number(value)
  return Number.isSafeInteger(page) && page >= 1 && page <= 1_000 ? page : 1
}

function normalizedQuery(value: string | null): string {
  return typeof value === 'string' && value.length <= 200 ? value : ''
}

export function RawResourceLedger<T>(props: RawResourceLedgerProps<T>) {
  const styles = useStyles()
  const { state } = useSession()
  const admin = state.status === 'authenticated' && state.subject.role === 'admin'
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const paginationState = useMemo<PaginationState | null>(() => {
    if (props.catalogMode === 'complete') return null
    return {
      page: normalizedPage(searchParams.get('page')),
      queryText: normalizedQuery(searchParams.get('q')),
    }
  }, [props.catalogMode, searchParams])
  const normalizedSearch = useMemo(() => {
    if (!paginationState) return ''
    const next = new URLSearchParams()
    if (paginationState.page > 1) next.set('page', String(paginationState.page))
    if (paginationState.queryText) next.set('q', paginationState.queryText)
    return next.toString()
  }, [paginationState])
  const [draftQuery, setDraftQuery] = useState(paginationState?.queryText ?? '')
  const [mode, setMode] = useState<EditorMode<T> | null>(null)
  const [content, setContent] = useState('')
  const [validation, setValidation] = useState<StructuredValidationResult>({ valid: false, message: '内容不能为空。' })
  const [confirmSave, setConfirmSave] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<T | null>(null)
  const [actionError, setActionError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const mountedRef = useRef(true)
  const operationEpochRef = useRef(0)
  const operationControllerRef = useRef<AbortController | null>(null)
  const mutationMutexRef = useRef(false)
  const rawControllerRef = useRef<AbortController | null>(null)
  const rawEpochRef = useRef(0)
  const [rawState, setRawState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const [rawReload, setRawReload] = useState(0)
  const downloadURLsRef = useRef(new Set<string>())
  const downloadTimersRef = useRef(new Set<number>())

  useEffect(() => {
    if (searchParams.toString() !== normalizedSearch) setSearchParams(normalizedSearch, { replace: true })
  }, [normalizedSearch, searchParams, setSearchParams])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      operationEpochRef.current += 1
      operationControllerRef.current?.abort()
      rawEpochRef.current += 1
      rawControllerRef.current?.abort()
      downloadTimersRef.current.forEach((timer) => window.clearTimeout(timer))
      downloadTimersRef.current.clear()
      downloadURLsRef.current.forEach((url) => URL.revokeObjectURL(url))
      downloadURLsRef.current.clear()
    }
  }, [])

  const activeID = mode && mode.kind !== 'create' ? props.getID(mode.item) : ''
  const listQuery = useQuery<KnowledgePage<T> | CompleteCatalog<T>>({
    queryKey: props.catalogMode === 'complete'
      ? ['knowledge', props.resourceKey, 'complete']
      : ['knowledge', props.resourceKey, { page: paginationState?.page ?? 1, size: 20, query: paginationState?.queryText ?? '' }],
    queryFn: ({ signal }) => {
      if (props.catalogMode === 'complete') return props.fetchComplete(signal)
      return props.fetchPage({ page: paginationState?.page ?? 1, size: 20, query: paginationState?.queryText ?? '' }, signal)
    },
    retry: false,
  })
  const catalog = listQuery.isSuccess ? listQuery.data : null
  const isCompleteCatalog = props.catalogMode === 'complete'
  const paginatedCatalog = !isCompleteCatalog && catalog ? catalog as KnowledgePage<T> : null
  const hasEmptyPage = catalog?.items.length === 0
  const hasEmptyCatalog = catalog !== null && hasEmptyPage && catalog.total === 0
  const hasOutOfRangePage = !isCompleteCatalog && catalog !== null && hasEmptyPage && catalog.total > 0
  const catalogScope: KnowledgeCatalogScope | null = catalog
    ? isCompleteCatalog
      ? { kind: 'complete', total: catalog.total, visibleItems: catalog.items.length }
      : { kind: 'paginated', total: catalog.total, page: paginatedCatalog?.page ?? 1, visibleItems: catalog.items.length }
    : null
  useEffect(() => {
    rawControllerRef.current?.abort()
    rawControllerRef.current = null
    rawEpochRef.current += 1
    setContent('')
    if (!activeID) {
      setRawState('idle')
      return
    }
    const controller = new AbortController()
    const epoch = rawEpochRef.current
    rawControllerRef.current = controller
    setRawState('loading')
    void props.fetchRaw(activeID, controller.signal).then((raw) => {
      if (!mountedRef.current || controller.signal.aborted || rawEpochRef.current !== epoch) return
      setContent(raw)
      setRawState('ready')
    }).catch(() => {
      if (!mountedRef.current || controller.signal.aborted || rawEpochRef.current !== epoch) return
      setContent('')
      setRawState('error')
    }).finally(() => {
      if (rawControllerRef.current === controller) rawControllerRef.current = null
    })
    return () => controller.abort()
  }, [activeID, props.fetchRaw, rawReload])

  const resetOperationStage = () => {
    operationControllerRef.current?.abort()
    operationControllerRef.current = null
    operationEpochRef.current += 1
    mutationMutexRef.current = false
    setSubmitting(false)
    setConfirmSave(false)
  }

  const closeEditor = () => {
    resetOperationStage()
    rawControllerRef.current?.abort()
    rawControllerRef.current = null
    rawEpochRef.current += 1
    setMode(null)
    setContent('')
    setRawState('idle')
  }

  const openMode = (nextMode: EditorMode<T>) => {
    resetOperationStage()
    rawControllerRef.current?.abort()
    rawControllerRef.current = null
    rawEpochRef.current += 1
    setContent('')
    setRawState(nextMode.kind === 'create' ? 'idle' : 'loading')
    setActionError('')
    setMode(nextMode)
  }

  const submitSearch = (event: FormEvent) => {
    event.preventDefault()
    const next = new URLSearchParams()
    const trimmed = draftQuery.trim().slice(0, 200)
    if (trimmed) next.set('q', trimmed)
    setSearchParams(next)
  }

  const downloadText = (text: string, filename: string) => {
    const url = URL.createObjectURL(new Blob([text], { type: 'text/plain;charset=utf-8' }))
    downloadURLsRef.current.add(url)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    link.click()
    const timer = window.setTimeout(() => {
      URL.revokeObjectURL(url)
      downloadURLsRef.current.delete(url)
      downloadTimersRef.current.delete(timer)
    }, 1_000)
    downloadTimersRef.current.add(timer)
  }

  const save = async () => {
    if (!admin || !mode || mode.kind === 'view' || !validation.valid || mutationMutexRef.current) return
    mutationMutexRef.current = true
    setSubmitting(true)
    setActionError('')
    const epoch = ++operationEpochRef.current
    const controller = new AbortController()
    operationControllerRef.current?.abort()
    operationControllerRef.current = controller
    try {
      if (mode.kind === 'create') await props.createResource(content, controller.signal)
      else await props.updateResource(props.getID(mode.item), content, controller.signal)
      if (!mountedRef.current || controller.signal.aborted || operationEpochRef.current !== epoch) return
      closeEditor()
      void queryClient.invalidateQueries({ queryKey: ['knowledge', props.resourceKey] })
    } catch {
      if (mountedRef.current && !controller.signal.aborted && operationEpochRef.current === epoch) {
        setContent('')
        setConfirmSave(false)
        setActionError(`${props.resourceLabel}保存失败，请显式重试。`)
      }
    } finally {
      if (operationControllerRef.current === controller) operationControllerRef.current = null
      mutationMutexRef.current = false
      if (mountedRef.current && operationEpochRef.current === epoch) setSubmitting(false)
    }
  }

  const remove = async () => {
    if (!admin || !deleteTarget || mutationMutexRef.current) return
    mutationMutexRef.current = true
    setSubmitting(true)
    setActionError('')
    const epoch = ++operationEpochRef.current
    const controller = new AbortController()
    operationControllerRef.current?.abort()
    operationControllerRef.current = controller
    try {
      await props.deleteResource(props.getID(deleteTarget), controller.signal)
      if (!mountedRef.current || controller.signal.aborted || operationEpochRef.current !== epoch) return
      setDeleteTarget(null)
      void queryClient.invalidateQueries({ queryKey: ['knowledge', props.resourceKey] })
    } catch {
      if (mountedRef.current && !controller.signal.aborted && operationEpochRef.current === epoch) setActionError(`${props.resourceLabel}删除失败，请显式重试。`)
    } finally {
      if (operationControllerRef.current === controller) operationControllerRef.current = null
      mutationMutexRef.current = false
      if (mountedRef.current && operationEpochRef.current === epoch) setSubmitting(false)
    }
  }

  const columns: readonly DataTableColumn<T>[] = [
    ...props.columns,
    {
      id: 'actions',
      header: '操作',
      render: (item) => (
        <div className={styles.actions}>
          <Button appearance="subtle" aria-label={`查看 ${props.getID(item)}`} onClick={() => openMode({ kind: 'view', item })}>查看</Button>
          {admin ? <Button appearance="subtle" aria-label={`编辑 ${props.getID(item)}`} onClick={() => openMode({ kind: 'edit', item })}>编辑</Button> : null}
          {admin ? <Button appearance="subtle" aria-label={`删除 ${props.getID(item)}`} onClick={() => { setDeleteTarget(item); setActionError('') }}>删除</Button> : null}
        </div>
      ),
    },
  ]

  return (
    <section className={styles.page}>
      <PageHeader title={props.title} description={props.description}>
        {props.sampleContent && props.sampleFileName ? <Button appearance="secondary" onClick={() => downloadText(props.sampleContent ?? '', props.sampleFileName ?? '知识样例.txt')}>下载样例</Button> : null}
        {admin ? <Button appearance="primary" aria-label={`新增${props.resourceLabel}`} onClick={() => openMode({ kind: 'create' })}>新增{props.resourceLabel}</Button> : null}
      </PageHeader>
      {actionError ? <MessageBar intent="error" role="alert"><MessageBarBody>{actionError}</MessageBarBody></MessageBar> : null}
      {catalogScope ? <KnowledgeCatalogBrief resourceLabel={props.resourceLabel} scope={catalogScope} canManage={admin} /> : null}
      {!isCompleteCatalog ? <form className={styles.filters} aria-label={`${props.resourceLabel}筛选`} onSubmit={submitSearch}>
        <Field className={styles.filterField} label="名称或说明"><Input className={styles.filterInput} value={draftQuery} maxLength={200} onChange={(_, data) => setDraftQuery(data.value)} /></Field>
        <Button type="submit">查询</Button>
        {paginationState?.queryText ? <Button type="button" appearance="secondary" onClick={() => { setDraftQuery(''); setSearchParams({}) }}>清除</Button> : null}
      </form> : null}

      {mode ? (
        <section className={styles.editor} aria-label={`${props.resourceLabel}${mode.kind === 'view' ? '详情' : '编辑'}`}>
          {activeID && rawState === 'loading' ? <StatePanel state="loading" title={`正在加载${props.resourceLabel}原文`} /> : null}
          {activeID && rawState === 'error' ? <StatePanel state="error" title={`${props.resourceLabel}原文加载失败`} actionLabel="重试" onAction={() => setRawReload((current) => current + 1)} /> : null}
          {(!activeID || rawState === 'ready') ? (
            <>
              <StructuredEditor
                key={`${mode.kind}:${activeID || 'new'}`}
                format={props.format}
                label={`${props.resourceLabel}原文`}
                value={content}
                disabled={mode.kind === 'view' || submitting}
                onChange={setContent}
                onValidationChange={setValidation}
              />
              <div className={styles.actions}>
                {activeID && props.currentFileName ? <Button appearance="secondary" onClick={() => downloadText(content, props.currentFileName?.(activeID) ?? '知识数据.txt')}>下载当前数据</Button> : null}
                {mode.kind !== 'view' ? <Button appearance="primary" disabled={!validation.valid || submitting} onClick={() => setConfirmSave(true)}>保存{props.resourceLabel}</Button> : null}
                <Button appearance="secondary" disabled={submitting} onClick={closeEditor}>关闭</Button>
              </div>
            </>
          ) : null}
        </section>
      ) : null}

      {listQuery.isPending ? <StatePanel state="loading" title={`正在加载${props.resourceLabel}目录`} /> : null}
      {listQuery.isError && listQuery.error instanceof ApiError && listQuery.error.kind === 'forbidden' ? <StatePanel state="forbidden" title={`无权查看${props.resourceLabel}`} /> : null}
      {listQuery.isError && !(listQuery.error instanceof ApiError && listQuery.error.kind === 'forbidden') ? <StatePanel state="error" title={`${props.resourceLabel}目录加载失败`} actionLabel="重试" onAction={() => void listQuery.refetch()} /> : null}
      {catalog?.items.length && !hasEmptyCatalog && !hasOutOfRangePage ? <div className={styles.tableViewport}><DataTable caption={`${props.resourceLabel}台账`} columns={columns} rows={catalog.items} getRowKey={props.getID} /></div> : null}
      {paginatedCatalog ? (
        <nav className={styles.pagination} aria-label={`${props.resourceLabel}分页`}>
          <span>共 {paginatedCatalog.total} 条，第 {paginatedCatalog.page} 页</span>
          <div className={styles.actions}>
            <Button disabled={paginatedCatalog.page <= 1} onClick={() => setSearchParams(paginatedCatalog.page > 2 ? { page: String(paginatedCatalog.page - 1), ...(paginationState?.queryText ? { q: paginationState.queryText } : {}) } : paginationState?.queryText ? { q: paginationState.queryText } : {})}>上一页</Button>
            <Button disabled={paginatedCatalog.page * paginatedCatalog.size >= paginatedCatalog.total} onClick={() => setSearchParams({ page: String(paginatedCatalog.page + 1), ...(paginationState?.queryText ? { q: paginationState.queryText } : {}) })}>下一页</Button>
          </div>
        </nav>
      ) : null}

      <Dialog open={confirmSave} onOpenChange={(_, data) => { if (!data.open && !submitting) setConfirmSave(false) }}>
        <DialogSurface aria-label={`确认保存${props.resourceLabel}`}><DialogBody>
          <DialogTitle>确认保存{props.resourceLabel}</DialogTitle>
          <DialogContent>保存会影响后续扫描，已完成任务与报告快照保持不变。服务端将再次校验权限、格式并记录审计。</DialogContent>
          <DialogActions><Button disabled={submitting} onClick={() => setConfirmSave(false)}>取消</Button><Button appearance="primary" disabled={submitting} onClick={() => void save()}>确认保存</Button></DialogActions>
        </DialogBody></DialogSurface>
      </Dialog>
      <Dialog open={deleteTarget !== null} onOpenChange={(_, data) => { if (!data.open && !submitting) setDeleteTarget(null) }}>
        <DialogSurface aria-label={`确认删除${props.resourceLabel}`}><DialogBody>
          <DialogTitle>确认删除{props.resourceLabel}</DialogTitle>
          <DialogContent>删除“{deleteTarget ? props.getID(deleteTarget) : ''}”后，仅后续扫描不再使用该内容。</DialogContent>
          <DialogActions><Button disabled={submitting} onClick={() => setDeleteTarget(null)}>取消</Button><Button appearance="primary" disabled={submitting} onClick={() => void remove()}>确认删除</Button></DialogActions>
        </DialogBody></DialogSurface>
      </Dialog>
    </section>
  )
}
