/**
 * 功能：展示当前 Subject 可见的模型台账并提供角色化治理动作。
 * 实现：从 URL 规范化服务端分页，以 source/read_only 显式区分 platform 与 YAML。
 * 输入：当前会话角色、分页深链、安全目录 DTO 与用户确认操作。
 * 输出：原生模型台账、加载/空/403/失败状态和单次写请求。
 * 依赖：Fluent UI、TanStack Query、React Router、Session 与共享台账组件。
 */
import {
  Badge,
  Button,
  Dialog,
  DialogActions,
  DialogBody,
  DialogContent,
  DialogSurface,
  DialogTitle,
  MessageBar,
  MessageBarBody,
  Text,
  makeStyles,
  tokens,
} from '@fluentui/react-components'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import { useSession } from '../auth/session'
import { ApiError } from '../../shared/api/errors'
import { DataTable, type DataTableColumn } from '../../shared/components/DataTable'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { deleteModel, fetchModelCatalog, type ModelCatalogItem } from './api'
import { ModelCatalogGovernanceSummary } from './components/ModelCatalogGovernanceSummary'
import { ModelForm } from './ModelForm'
import { RotateCredentialDialog } from './RotateCredentialDialog'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL, minWidth: 0 },
  tableViewport: { minWidth: 0, overflowX: 'auto' },
  pagination: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    gap: tokens.spacingHorizontalM,
    '@media (max-width: 960px)': {
      flexDirection: 'column',
      alignItems: 'flex-start',
    },
  },
  paginationActions: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: tokens.spacingHorizontalXS,
  },
  actions: { display: 'flex', flexWrap: 'wrap', gap: tokens.spacingHorizontalXS },
  secondary: { color: tokens.colorNeutralForeground2 },
})

function pageFrom(value: string | null): number {
  if (!value || !/^\d+$/.test(value)) return 1
  const page = Number(value)
  return Number.isSafeInteger(page) && page >= 1 && page <= 1_000 ? page : 1
}

function canManage(role: 'user' | 'auditor' | 'admin', model: ModelCatalogItem): boolean {
  return model.source === 'platform' && !model.read_only &&
    (role === 'user' && model.scope === 'private' || role === 'admin' && model.scope === 'global')
}

export function ModelListPage() {
  const styles = useStyles()
  const { state } = useSession()
  const role = state.status === 'authenticated' ? state.subject.role : 'auditor'
  const queryClient = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const page = pageFrom(searchParams.get('page'))
  const normalized = page > 1 ? `page=${page}` : ''
  const [editing, setEditing] = useState<ModelCatalogItem | 'create' | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<ModelCatalogItem | null>(null)
  const [rotateTarget, setRotateTarget] = useState<ModelCatalogItem | null>(null)
  const [actionError, setActionError] = useState('')
  const [deleting, setDeleting] = useState(false)
  const mountedRef = useRef(true)
  const deleteMutexRef = useRef(false)
  const deleteControllerRef = useRef<AbortController | null>(null)

  useEffect(() => {
    if (searchParams.toString() !== normalized) setSearchParams(normalized, { replace: true })
  }, [normalized, searchParams, setSearchParams])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      deleteControllerRef.current?.abort()
    }
  }, [])

  const query = useQuery({
    queryKey: ['models', { page, pageSize: 20 }],
    queryFn: ({ signal }) => fetchModelCatalog({ page, pageSize: 20 }, signal),
    retry: false,
  })
  const modelCatalog = query.isSuccess ? query.data : null
  const hasEmptyPage = modelCatalog?.items.length === 0
  const hasEmptyCatalog = modelCatalog !== null && hasEmptyPage && modelCatalog.total === 0
  const hasOutOfRangePage = modelCatalog !== null && hasEmptyPage && modelCatalog.total > 0

  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['models'] })
  }

  const confirmDelete = async () => {
    if (!deleteTarget || deleteMutexRef.current || !canManage(role, deleteTarget)) return
    deleteMutexRef.current = true
    setDeleting(true)
    setActionError('')
    const controller = new AbortController()
    deleteControllerRef.current?.abort()
    deleteControllerRef.current = controller
    try {
      await deleteModel(deleteTarget.id, controller.signal)
      if (!mountedRef.current || controller.signal.aborted) return
      setDeleteTarget(null)
      void refresh()
    } catch {
      if (mountedRef.current && !controller.signal.aborted) setActionError('模型删除失败，请显式重试。')
    } finally {
      if (deleteControllerRef.current === controller) deleteControllerRef.current = null
      deleteMutexRef.current = false
      if (mountedRef.current) setDeleting(false)
    }
  }

  const columns: readonly DataTableColumn<ModelCatalogItem>[] = [
    { id: 'id', header: '配置ID', render: (model) => model.id },
    { id: 'name', header: '模型名称', render: (model) => model.name },
    { id: 'provider', header: '模型ID', render: (model) => model.provider_model || '未配置' },
    {
      id: 'scope',
      header: '作用范围',
      render: (model) => <Badge color={model.scope === 'global' ? 'brand' : 'subtle'}>{model.scope === 'global' ? '全局' : '私有'}</Badge>,
    },
    {
      id: 'source',
      header: '来源',
      render: (model) => <Badge color={model.source === 'yaml' ? 'informative' : 'brand'}>{model.source === 'yaml' ? 'YAML 只读' : '数据库'}</Badge>,
    },
    {
      id: 'status',
      header: '状态',
      render: (model) => {
        if (model.disabled) return <Badge color="warning">已停用</Badge>
        if (model.read_only) return <Badge color="informative">只读</Badge>
        return <Badge color={canManage(role, model) ? 'brand' : 'subtle'}>{canManage(role, model) ? '可配置' : '受限'}</Badge>
      },
    },
    {
      id: 'action',
      header: '操作',
      render: (model) => canManage(role, model) ? (
        <div className={styles.actions}>
          <Button appearance="subtle" aria-label={`编辑 ${model.name}`} onClick={() => { setEditing(model); setActionError('') }}>编辑</Button>
          <Button appearance="subtle" aria-label={`删除 ${model.name}`} onClick={() => { setDeleteTarget(model); setActionError('') }}>删除</Button>
          {role === 'admin' && model.scope === 'global' ? (
            <Button appearance="subtle" aria-label={`轮换加密 ${model.name}`} onClick={() => { setRotateTarget(model); setActionError('') }}>轮换加密</Button>
          ) : null}
        </div>
      ) : <Text className={styles.secondary}>只读</Text>,
    },
  ]

  return (
    <section className={styles.page}>
      <PageHeader title="模型与凭据" description="查看当前权限范围内的模型配置。Token 始终由服务端加密保存，不回传浏览器。">
        {role === 'user' || role === 'admin' ? (
          <Button appearance="primary" onClick={() => { setEditing('create'); setActionError('') }}>
            {role === 'admin' ? '新增全局模型' : '新增私有模型'}
          </Button>
        ) : null}
      </PageHeader>

      {actionError ? <MessageBar intent="error" role="alert"><MessageBarBody>{actionError}</MessageBarBody></MessageBar> : null}
      {modelCatalog ? (
        <ModelCatalogGovernanceSummary
          catalog={modelCatalog}
          isManageable={(item) => canManage(role, item)}
        />
      ) : null}
      {editing ? (
        <section aria-label="模型配置工作区">
          <ModelForm
            key={editing === 'create' ? `create:${role}` : `${editing.source}:${editing.id}`}
            role={role === 'admin' ? 'admin' : 'user'}
            model={editing === 'create' ? undefined : editing}
            onCancel={() => setEditing(null)}
            onSaved={() => { setEditing(null); void refresh() }}
          />
        </section>
      ) : null}

      {query.isPending ? <StatePanel state="loading" title="正在加载模型目录" /> : null}
      {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? (
        <StatePanel state="forbidden" title="无权查看模型目录" />
      ) : null}
      {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? (
        <StatePanel state="error" title="暂时无法加载模型目录" description="请稍后重试。" actionLabel="重试" onAction={() => void query.refetch()} />
      ) : null}
      {hasEmptyCatalog ? <StatePanel state="empty" title="暂无可见模型" description="在当前角色允许的范围内创建模型后将在此显示。" /> : null}
      {hasOutOfRangePage ? <StatePanel state="empty" title="当前页没有模型" description="可返回上一页继续查看匹配模型。" /> : null}
      {modelCatalog?.items.length ? (
        <div className={styles.tableViewport}>
          <DataTable caption="受治理模型台账" columns={columns} rows={modelCatalog.items} getRowKey={(model) => `${model.source}:${model.id}`} />
        </div>
      ) : null}
      {modelCatalog ? (
        <nav className={styles.pagination} aria-label="模型分页">
          <span>共 {modelCatalog.total} 条，第 {modelCatalog.page} 页</span>
          <div className={styles.paginationActions}>
            <Button appearance="secondary" disabled={modelCatalog.page <= 1} onClick={() => setSearchParams(modelCatalog.page > 2 ? { page: String(modelCatalog.page - 1) } : {})}>上一页</Button>
            <Button appearance="secondary" disabled={modelCatalog.page * modelCatalog.page_size >= modelCatalog.total} onClick={() => setSearchParams({ page: String(modelCatalog.page + 1) })}>下一页</Button>
          </div>
        </nav>
      ) : null}

      <Dialog open={deleteTarget !== null} onOpenChange={(_, data) => { if (!data.open && !deleting) setDeleteTarget(null) }}>
        <DialogSurface aria-label="删除模型">
          <DialogBody>
            <DialogTitle>删除模型</DialogTitle>
            <DialogContent>删除后将无法在新任务中选用“{deleteTarget?.name}”。服务端会再次校验权限并记录审计事件。</DialogContent>
            <DialogActions>
              <Button appearance="secondary" disabled={deleting} onClick={() => setDeleteTarget(null)}>取消</Button>
              <Button appearance="primary" disabled={deleting} onClick={() => void confirmDelete()}>{deleting ? '正在删除' : '确认删除'}</Button>
            </DialogActions>
          </DialogBody>
        </DialogSurface>
      </Dialog>

      {rotateTarget ? (
        <RotateCredentialDialog
          key={`${rotateTarget.source}:${rotateTarget.id}`}
          open
          model={rotateTarget}
          onClose={() => setRotateTarget(null)}
          onCompleted={() => { setRotateTarget(null); void refresh() }}
        />
      ) : null}
    </section>
  )
}
