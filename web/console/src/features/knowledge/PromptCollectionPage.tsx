/**
 * 功能：浏览、筛选并治理 Prompt 集合的既有结构化字段。
 * 实现：页面只消费白名单 DTO，管理员创建/编辑/删除均二次确认并隔离异步阶段。
 * 输入：Prompt 集合目录、筛选词、结构化表单和当前 Subject 角色。
 * 输出：只读台账或管理员单次治理请求，不扩展旧文件 schema。
 * 依赖：Fluent UI、TanStack Query、Session、知识 API 与共享台账组件。
 */
import {
  Button,
  Checkbox,
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
  Textarea,
  makeStyles,
  tokens,
} from '@fluentui/react-components'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'

import { ApiError } from '../../shared/api/errors'
import { DataTable, type DataTableColumn } from '../../shared/components/DataTable'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { useSession } from '../auth/session'
import {
  createPromptCollection,
  deletePromptCollection,
  fetchPromptCollections,
  isSafeKnowledgeID,
  updatePromptCollection,
  type PromptCollection,
} from './api'

const EMPTY_PROMPT: PromptCollection = {
  id: '', product: '', affiliation: '', modelVersion: '', prompt: '',
  codeExec: false, uploadFile: false, multiModal: false, webSearch: false, securityPolicies: false,
}

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  filters: { display: 'flex', gap: tokens.spacingHorizontalS, alignItems: 'end', flexWrap: 'wrap' },
  form: { display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: tokens.spacingHorizontalM },
  wide: { gridColumn: '1 / -1' },
  checks: { gridColumn: '1 / -1', display: 'flex', gap: tokens.spacingHorizontalM, flexWrap: 'wrap' },
  actions: { display: 'flex', gap: tokens.spacingHorizontalXS, flexWrap: 'wrap' },
})

export function PromptCollectionPage() {
  const styles = useStyles()
  const { state } = useSession()
  const admin = state.status === 'authenticated' && state.subject.role === 'admin'
  const queryClient = useQueryClient()
  const query = useQuery({ queryKey: ['knowledge', 'prompts'], queryFn: ({ signal }) => fetchPromptCollections(signal), retry: false })
  const [filter, setFilter] = useState('')
  const [editingID, setEditingID] = useState<string | 'create' | null>(null)
  const [draft, setDraft] = useState<PromptCollection>(EMPTY_PROMPT)
  const [confirmSave, setConfirmSave] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<PromptCollection | null>(null)
  const [viewTarget, setViewTarget] = useState<PromptCollection | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [actionError, setActionError] = useState('')
  const mountedRef = useRef(true)
  const controllerRef = useRef<AbortController | null>(null)
  const epochRef = useRef(0)
  const mutexRef = useRef(false)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      epochRef.current += 1
      controllerRef.current?.abort()
    }
  }, [])

  const filtered = useMemo(() => {
    const needle = filter.trim().toLocaleLowerCase()
    if (!needle) return query.data ?? []
    return (query.data ?? []).filter((item) => [item.id, item.product, item.affiliation, item.modelVersion].some((value) => value.toLocaleLowerCase().includes(needle)))
  }, [filter, query.data])

  const resetOperationStage = () => {
    controllerRef.current?.abort()
    controllerRef.current = null
    epochRef.current += 1
    mutexRef.current = false
    setSubmitting(false)
    setConfirmSave(false)
  }

  const openCreate = () => {
    resetOperationStage()
    setEditingID('create')
    setDraft(EMPTY_PROMPT)
    setActionError('')
  }

  const openEdit = (item: PromptCollection) => {
    resetOperationStage()
    setEditingID(item.id)
    setDraft({ ...item })
    setActionError('')
  }

  const closeForm = () => {
    resetOperationStage()
    setEditingID(null)
    setDraft(EMPTY_PROMPT)
  }

  const save = async () => {
    if (!admin || !editingID || !isSafeKnowledgeID(draft.id) || mutexRef.current) return
    mutexRef.current = true
    setSubmitting(true)
    setActionError('')
    const epoch = ++epochRef.current
    const controller = new AbortController()
    controllerRef.current?.abort()
    controllerRef.current = controller
    try {
      if (editingID === 'create') await createPromptCollection(draft, controller.signal)
      else await updatePromptCollection(editingID, draft, controller.signal)
      if (!mountedRef.current || controller.signal.aborted || epochRef.current !== epoch) return
      closeForm()
      void queryClient.invalidateQueries({ queryKey: ['knowledge', 'prompts'] })
    } catch {
      if (mountedRef.current && !controller.signal.aborted && epochRef.current === epoch) {
        setDraft(EMPTY_PROMPT)
        setConfirmSave(false)
        setActionError('Prompt 集合保存失败，请显式重试。')
      }
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
      mutexRef.current = false
      if (mountedRef.current && epochRef.current === epoch) setSubmitting(false)
    }
  }

  const remove = async () => {
    if (!admin || !deleteTarget || mutexRef.current) return
    mutexRef.current = true
    setSubmitting(true)
    const epoch = ++epochRef.current
    const controller = new AbortController()
    controllerRef.current?.abort()
    controllerRef.current = controller
    try {
      await deletePromptCollection(deleteTarget.id, controller.signal)
      if (!mountedRef.current || controller.signal.aborted || epochRef.current !== epoch) return
      setDeleteTarget(null)
      void queryClient.invalidateQueries({ queryKey: ['knowledge', 'prompts'] })
    } catch {
      if (mountedRef.current && !controller.signal.aborted && epochRef.current === epoch) setActionError('Prompt 集合删除失败，请显式重试。')
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
      mutexRef.current = false
      if (mountedRef.current && epochRef.current === epoch) setSubmitting(false)
    }
  }

  const columns: readonly DataTableColumn<PromptCollection>[] = [
    { id: 'id', header: '集合 ID', render: (item) => item.id },
    { id: 'product', header: '产品', render: (item) => item.product || '未提供' },
    { id: 'affiliation', header: '机构', render: (item) => item.affiliation || '未提供' },
    { id: 'version', header: '模型版本', render: (item) => item.modelVersion || '未提供' },
    { id: 'policy', header: '安全策略', render: (item) => item.securityPolicies ? '启用' : '未启用' },
    {
      id: 'actions', header: '操作', render: (item) => admin ? <div className={styles.actions}>
        <Button appearance="subtle" aria-label={`查看 ${item.id}`} onClick={() => setViewTarget(item)}>查看</Button>
        <Button appearance="subtle" aria-label={`编辑 ${item.id}`} onClick={() => openEdit(item)}>编辑</Button>
        <Button appearance="subtle" aria-label={`删除 ${item.id}`} onClick={() => { setDeleteTarget(item); setActionError('') }}>删除</Button>
      </div> : <Button appearance="subtle" aria-label={`查看 ${item.id}`} onClick={() => setViewTarget(item)}>查看</Button>,
    },
  ]

  return <section className={styles.page}>
    <PageHeader title="Prompt 集合" description="浏览并筛选 AI 应用检查 Prompt；仅管理员可维护既有字段结构。">
      {admin ? <Button appearance="primary" onClick={openCreate}>新增 Prompt 集合</Button> : null}
    </PageHeader>
    {actionError ? <MessageBar role="alert" intent="error"><MessageBarBody>{actionError}</MessageBarBody></MessageBar> : null}
    <div className={styles.filters}><Field label="集合、产品或机构"><Input value={filter} maxLength={200} onChange={(_, data) => setFilter(data.value)} /></Field></div>
    {editingID ? <form className={styles.form} aria-label="Prompt 集合表单" onSubmit={(event) => { event.preventDefault(); if (isSafeKnowledgeID(draft.id)) setConfirmSave(true) }}>
      <Field label="集合 ID" required><Input value={draft.id} disabled={editingID !== 'create' || submitting} maxLength={256} onChange={(_, data) => setDraft((current) => ({ ...current, id: data.value }))} /></Field>
      <Field label="产品"><Input value={draft.product} disabled={submitting} maxLength={512} onChange={(_, data) => setDraft((current) => ({ ...current, product: data.value }))} /></Field>
      <Field label="机构"><Input value={draft.affiliation} disabled={submitting} maxLength={512} onChange={(_, data) => setDraft((current) => ({ ...current, affiliation: data.value }))} /></Field>
      <Field label="模型版本"><Input value={draft.modelVersion} disabled={submitting} maxLength={512} onChange={(_, data) => setDraft((current) => ({ ...current, modelVersion: data.value }))} /></Field>
      <Field className={styles.wide} label="Prompt" required><Textarea value={draft.prompt} disabled={submitting} resize="vertical" onChange={(_, data) => setDraft((current) => ({ ...current, prompt: data.value }))} /></Field>
      <div className={styles.checks}>
        <Checkbox checked={draft.codeExec} disabled={submitting} label="代码执行" onChange={(_, data) => setDraft((current) => ({ ...current, codeExec: data.checked === true }))} />
        <Checkbox checked={draft.uploadFile} disabled={submitting} label="文件上传" onChange={(_, data) => setDraft((current) => ({ ...current, uploadFile: data.checked === true }))} />
        <Checkbox checked={draft.multiModal} disabled={submitting} label="多模态" onChange={(_, data) => setDraft((current) => ({ ...current, multiModal: data.checked === true }))} />
        <Checkbox checked={draft.webSearch} disabled={submitting} label="联网搜索" onChange={(_, data) => setDraft((current) => ({ ...current, webSearch: data.checked === true }))} />
        <Checkbox checked={draft.securityPolicies} disabled={submitting} label="安全策略" onChange={(_, data) => setDraft((current) => ({ ...current, securityPolicies: data.checked === true }))} />
      </div>
      <div className={styles.actions}><Button type="submit" appearance="primary" disabled={submitting || !isSafeKnowledgeID(draft.id)}>保存 Prompt 集合</Button><Button type="button" disabled={submitting} onClick={closeForm}>取消</Button></div>
    </form> : null}
    {query.isPending ? <StatePanel state="loading" title="正在加载 Prompt 集合" /> : null}
    {query.isError && query.error instanceof ApiError && query.error.kind === 'forbidden' ? <StatePanel state="forbidden" title="无权查看 Prompt 集合" /> : null}
    {query.isError && !(query.error instanceof ApiError && query.error.kind === 'forbidden') ? <StatePanel state="error" title="Prompt 集合加载失败" actionLabel="重试" onAction={() => void query.refetch()} /> : null}
    {query.data && filtered.length === 0 ? <StatePanel state="empty" title="暂无匹配的 Prompt 集合" /> : null}
    {filtered.length ? <DataTable caption="Prompt 集合台账" columns={columns} rows={filtered} getRowKey={(item) => item.id} /> : null}
    <Dialog open={viewTarget !== null} onOpenChange={(_, data) => { if (!data.open) setViewTarget(null) }}><DialogSurface aria-label="Prompt 集合详情"><DialogBody><DialogTitle>Prompt 集合详情</DialogTitle><DialogContent><dl><dt>集合 ID</dt><dd>{viewTarget?.id}</dd><dt>产品</dt><dd>{viewTarget?.product || '未提供'}</dd><dt>机构</dt><dd>{viewTarget?.affiliation || '未提供'}</dd><dt>模型版本</dt><dd>{viewTarget?.modelVersion || '未提供'}</dd><dt>Prompt</dt><dd><pre>{viewTarget?.prompt}</pre></dd><dt>能力边界</dt><dd>{[viewTarget?.codeExec ? '代码执行' : '', viewTarget?.uploadFile ? '文件上传' : '', viewTarget?.multiModal ? '多模态' : '', viewTarget?.webSearch ? '联网搜索' : '', viewTarget?.securityPolicies ? '安全策略' : ''].filter(Boolean).join('、') || '未启用'}</dd></dl></DialogContent><DialogActions><Button onClick={() => setViewTarget(null)}>关闭</Button></DialogActions></DialogBody></DialogSurface></Dialog>
    <Dialog open={confirmSave} onOpenChange={(_, data) => { if (!data.open && !submitting) setConfirmSave(false) }}><DialogSurface aria-label="确认保存 Prompt 集合"><DialogBody><DialogTitle>确认保存 Prompt 集合</DialogTitle><DialogContent>变更仅影响后续扫描，服务端将校验权限并记录审计。</DialogContent><DialogActions><Button onClick={() => setConfirmSave(false)}>取消</Button><Button appearance="primary" onClick={() => void save()}>确认保存</Button></DialogActions></DialogBody></DialogSurface></Dialog>
    <Dialog open={deleteTarget !== null} onOpenChange={(_, data) => { if (!data.open && !submitting) setDeleteTarget(null) }}><DialogSurface aria-label="确认删除 Prompt 集合"><DialogBody><DialogTitle>确认删除 Prompt 集合</DialogTitle><DialogContent>确认删除“{deleteTarget?.id}”？</DialogContent><DialogActions><Button onClick={() => setDeleteTarget(null)}>取消</Button><Button appearance="primary" onClick={() => void remove()}>确认删除</Button></DialogActions></DialogBody></DialogSurface></Dialog>
  </section>
}
