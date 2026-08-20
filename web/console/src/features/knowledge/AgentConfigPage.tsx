/**
 * 功能：浏览、创建、编辑、测试和删除既有 Agent 配置。
 * 实现：动态模板仅生成受控字段，原文编辑保持字节语义，所有写操作二次确认并隔离异步阶段。
 * 输入：Agent 名称、模板字段、YAML/JSON 原文、测试 Prompt 与当前 Subject 角色。
 * 输出：全角色只读台账或管理员单次治理请求与固定安全反馈。
 * 依赖：Fluent UI、TanStack Query、Session、StructuredEditor 与知识 API。
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
  Select,
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
  deleteAgentConfig,
  fetchAgentConfig,
  fetchAgentNames,
  fetchAgentTemplates,
  isSafeKnowledgeID,
  saveAgentConfig,
  testAgentConnection,
  testAgentPrompt,
  type AgentTemplate,
  type AgentTemplateField,
} from './api'
import { StructuredEditor, type StructuredValidationResult } from './components/StructuredEditor'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  actions: { display: 'flex', gap: tokens.spacingHorizontalXS, flexWrap: 'wrap' },
  editor: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalM },
  form: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(16rem, 1fr))', gap: tokens.spacingHorizontalM },
  wide: { gridColumn: '1 / -1' },
  advanced: { gridColumn: '1 / -1', padding: tokens.spacingVerticalS, border: `${tokens.strokeWidthThin} solid ${tokens.colorNeutralStroke2}`, borderRadius: tokens.borderRadiusMedium },
})

function initialTemplateValues(template: AgentTemplate | undefined): Record<string, string> {
  return Object.fromEntries((template?.fields ?? []).map((field) => [field.field, field.defaultValue === undefined ? '' : String(field.defaultValue)]))
}

function setNestedValue(target: Record<string, unknown>, path: string, value: unknown) {
  const segments = path.split('.')
  let cursor = target
  segments.forEach((segment, index) => {
    if (index === segments.length - 1) cursor[segment] = value
    else {
      const next: Record<string, unknown> = {}
      cursor[segment] = next
      cursor = next
    }
  })
}

function buildTemplateContent(template: AgentTemplate, values: Record<string, string>): string | undefined {
  const output: Record<string, unknown> = { type: template.id }
  for (const field of template.fields) {
    const raw = values[field.field] ?? ''
    if (!raw && !field.required) continue
    if (!raw && field.required) return undefined
    let value: unknown = raw
    if (field.type === 'number') {
      const numeric = Number(raw)
      if (!Number.isFinite(numeric) || field.minimum !== undefined && numeric < field.minimum || field.maximum !== undefined && numeric > field.maximum) return undefined
      value = numeric
    } else if (field.type === 'json') {
      try { value = JSON.parse(raw) } catch { return undefined }
    }
    setNestedValue(output, field.field, value)
  }
  return JSON.stringify(output, null, 2)
}

function TemplateField({ field, value, disabled, onChange }: { field: AgentTemplateField; value: string; disabled: boolean; onChange: (value: string) => void }) {
  if (field.type === 'select') return <Field label={field.label} required={field.required} hint={field.description}>
    <Select value={value} disabled={disabled} onChange={(_, data) => onChange(data.value)}>{field.options?.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</Select>
  </Field>
  if (field.type === 'textarea' || field.type === 'json') return <Field label={field.label} required={field.required} hint={field.description}>
    <Textarea value={value} disabled={disabled} placeholder={field.placeholder} resize="vertical" onChange={(_, data) => onChange(data.value)} />
  </Field>
  return <Field label={field.label} required={field.required} hint={field.description}>
    <Input type={field.type === 'password' ? 'password' : field.type === 'number' ? 'number' : 'text'} autoComplete={field.type === 'password' ? 'new-password' : 'off'} value={value} disabled={disabled} placeholder={field.placeholder} min={field.minimum} max={field.maximum} step={field.step} onChange={(_, data) => onChange(data.value)} />
  </Field>
}

export function AgentConfigPage() {
  const styles = useStyles()
  const { state } = useSession()
  const admin = state.status === 'authenticated' && state.subject.role === 'admin'
  const queryClient = useQueryClient()
  const namesQuery = useQuery({ queryKey: ['knowledge', 'agents'], queryFn: ({ signal }) => fetchAgentNames(signal), retry: false })
  const templatesQuery = useQuery({ queryKey: ['knowledge', 'agent-templates'], queryFn: ({ signal }) => fetchAgentTemplates(signal), retry: false, enabled: admin })
  const [selectedName, setSelectedName] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [draftName, setDraftName] = useState('')
  const [content, setContent] = useState('')
  const [valid, setValid] = useState(false)
  const [templateID, setTemplateID] = useState('')
  const [templateValues, setTemplateValues] = useState<Record<string, string>>({})
  const [confirmSave, setConfirmSave] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [prompt, setPrompt] = useState('')
  const [actionError, setActionError] = useState('')
  const [actionResult, setActionResult] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const mountedRef = useRef(true)
  const controllerRef = useRef<AbortController | null>(null)
  const configControllerRef = useRef<AbortController | null>(null)
  const epochRef = useRef(0)
  const configEpochRef = useRef(0)
  const mutexRef = useRef(false)
  const downloadURLsRef = useRef(new Set<string>())
  const downloadTimersRef = useRef(new Set<number>())
  const [configState, setConfigState] = useState<'idle' | 'loading' | 'ready' | 'error'>('idle')
  const [configReload, setConfigReload] = useState(0)
  const selectedTemplate = useMemo(() => templatesQuery.data?.find((template) => template.id === templateID), [templateID, templatesQuery.data])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      epochRef.current += 1
      controllerRef.current?.abort()
      configEpochRef.current += 1
      configControllerRef.current?.abort()
      downloadTimersRef.current.forEach((timer) => window.clearTimeout(timer))
      downloadTimersRef.current.clear()
      downloadURLsRef.current.forEach((url) => URL.revokeObjectURL(url))
      downloadURLsRef.current.clear()
    }
  }, [])

  useEffect(() => {
    configControllerRef.current?.abort()
    configControllerRef.current = null
    configEpochRef.current += 1
    if (!selectedName) {
      setConfigState('idle')
      return
    }
    setContent('')
    const controller = new AbortController()
    const epoch = configEpochRef.current
    configControllerRef.current = controller
    setConfigState('loading')
    void fetchAgentConfig(selectedName, controller.signal).then((config) => {
      if (!mountedRef.current || controller.signal.aborted || configEpochRef.current !== epoch || config.name !== selectedName) return
      setContent(config.content)
      setConfigState('ready')
    }).catch(() => {
      if (!mountedRef.current || controller.signal.aborted || configEpochRef.current !== epoch) return
      setContent('')
      setConfigState('error')
    }).finally(() => {
      if (configControllerRef.current === controller) configControllerRef.current = null
    })
    return () => controller.abort()
  }, [configReload, selectedName])

  useEffect(() => {
    if (!creating || templateID || !templatesQuery.data?.length) return
    const template = templatesQuery.data[0]
    const values = initialTemplateValues(template)
    setTemplateID(template.id)
    setTemplateValues(values)
    setContent(buildTemplateContent(template, values) ?? '')
  }, [creating, templateID, templatesQuery.data])

  const invalidateStage = () => {
    controllerRef.current?.abort()
    controllerRef.current = null
    epochRef.current += 1
    mutexRef.current = false
    setSubmitting(false)
    setActionError('')
    setActionResult('')
    setPrompt('')
    setConfirmSave(false)
    setConfirmDelete(false)
  }

  const openExisting = (name: string) => {
    invalidateStage()
    configControllerRef.current?.abort()
    configControllerRef.current = null
    configEpochRef.current += 1
    setCreating(false)
    setDraftName(name)
    setContent('')
    setSelectedName(name)
  }

  const openCreate = () => {
    invalidateStage()
    configControllerRef.current?.abort()
    configControllerRef.current = null
    configEpochRef.current += 1
    setSelectedName(null)
    setCreating(true)
    setDraftName('')
    const template = templatesQuery.data?.[0]
    setTemplateID(template?.id ?? '')
    const values = initialTemplateValues(template)
    setTemplateValues(values)
    setContent(template ? buildTemplateContent(template, values) ?? '' : '')
  }

  const closeEditor = () => {
    invalidateStage()
    configControllerRef.current?.abort()
    configControllerRef.current = null
    configEpochRef.current += 1
    setSelectedName(null)
    setCreating(false)
    setDraftName('')
    setTemplateID('')
    setTemplateValues({})
    setContent('')
    setValid(false)
    setConfigState('idle')
  }

  const changeTemplate = (id: string) => {
    const template = templatesQuery.data?.find((item) => item.id === id)
    const values = initialTemplateValues(template)
    setTemplateID(id)
    setTemplateValues(values)
    setContent(template ? buildTemplateContent(template, values) ?? '' : '')
  }

  const changeTemplateField = (field: string, value: string) => {
    if (!selectedTemplate) return
    const values = { ...templateValues, [field]: value }
    setTemplateValues(values)
    setContent(buildTemplateContent(selectedTemplate, values) ?? '')
  }

  const downloadTemplate = () => {
    if (!selectedTemplate) return
    const templateContent = content || JSON.stringify({ type: selectedTemplate.id }, null, 2)
    const url = URL.createObjectURL(new Blob([templateContent], { type: 'text/yaml;charset=utf-8' }))
    downloadURLsRef.current.add(url)
    const link = document.createElement('a')
    link.href = url
    link.download = 'Agent配置模板.yaml'
    link.click()
    const timer = window.setTimeout(() => {
      URL.revokeObjectURL(url)
      downloadURLsRef.current.delete(url)
      downloadTimersRef.current.delete(timer)
    }, 1_000)
    downloadTimersRef.current.add(timer)
  }

  const runAction = async (kind: 'save' | 'delete' | 'connect' | 'prompt') => {
    if (!admin || mutexRef.current || !isSafeKnowledgeID(draftName)) return
    mutexRef.current = true
    setSubmitting(true)
    setActionError('')
    setActionResult('')
    const epoch = ++epochRef.current
    const controller = new AbortController()
    controllerRef.current?.abort()
    controllerRef.current = controller
    try {
      if (kind === 'save') await saveAgentConfig(draftName, content, controller.signal)
      if (kind === 'delete') await deleteAgentConfig(draftName, controller.signal)
      if (kind === 'connect') await testAgentConnection(content, controller.signal)
      const output = kind === 'prompt' ? await testAgentPrompt(content, prompt, controller.signal) : ''
      if (!mountedRef.current || controller.signal.aborted || epochRef.current !== epoch) return
      setConfirmSave(false)
      setConfirmDelete(false)
      if (kind === 'save' || kind === 'delete') {
        setContent('')
        setTemplateValues({})
        setPrompt('')
        setCreating(false)
        setSelectedName(null)
        setDraftName('')
        void queryClient.invalidateQueries({ queryKey: ['knowledge', 'agents'] })
      } else setActionResult(kind === 'connect' ? '连通性测试通过。' : `Prompt 测试完成：${output}`)
    } catch {
      if (mountedRef.current && !controller.signal.aborted && epochRef.current === epoch) {
        setContent('')
        setTemplateValues({})
        setPrompt('')
        setConfirmSave(false)
        setConfirmDelete(false)
        setActionError(`${kind === 'delete' ? 'Agent 配置删除' : kind === 'save' ? 'Agent 配置保存' : 'Agent 测试'}失败，请显式重试。`)
      }
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
      mutexRef.current = false
      if (mountedRef.current && epochRef.current === epoch) setSubmitting(false)
    }
  }

  const columns: readonly DataTableColumn<string>[] = [
    { id: 'name', header: '配置名称', render: (name) => name },
    { id: 'scope', header: '访问模式', render: () => '受身份范围控制' },
    { id: 'actions', header: '操作', render: (name) => <Button appearance="subtle" aria-label={`查看 ${name}`} onClick={() => openExisting(name)}>查看</Button> },
  ]

  const editorOpen = selectedName !== null || creating
  return <section className={styles.page}>
    <PageHeader title="Agent 配置" description="浏览 Agent provider 配置；管理员可使用既有模板维护并执行受控测试。">
      {admin ? <Button appearance="primary" onClick={openCreate}>新增 Agent 配置</Button> : null}
    </PageHeader>
    {actionError ? <MessageBar role="alert" intent="error"><MessageBarBody>{actionError}</MessageBarBody></MessageBar> : null}
    {actionResult ? <MessageBar role="status" intent="success"><MessageBarBody>{actionResult}</MessageBarBody></MessageBar> : null}
    {namesQuery.isPending ? <StatePanel state="loading" title="正在加载 Agent 配置" /> : null}
    {namesQuery.isError && namesQuery.error instanceof ApiError && namesQuery.error.kind === 'forbidden' ? <StatePanel state="forbidden" title="无权查看 Agent 配置" /> : null}
    {namesQuery.isError && !(namesQuery.error instanceof ApiError && namesQuery.error.kind === 'forbidden') ? <StatePanel state="error" title="Agent 配置加载失败" actionLabel="重试" onAction={() => void namesQuery.refetch()} /> : null}
    {namesQuery.data?.length === 0 ? <StatePanel state="empty" title="暂无 Agent 配置" /> : null}
    {namesQuery.data?.length ? <DataTable caption="Agent 配置台账" columns={columns} rows={namesQuery.data} getRowKey={(name) => name} /> : null}

    {editorOpen ? <div className={styles.editor}>
      <Field label="配置名称" required><Input value={draftName} disabled={!creating || submitting || !admin} maxLength={256} onChange={(_, data) => setDraftName(data.value)} /></Field>
      {creating && templatesQuery.data ? <div className={styles.form}>
        <Field label="Provider 类型" required><Select value={templateID} disabled={submitting} onChange={(_, data) => changeTemplate(data.value)}>{templatesQuery.data.map((template) => <option key={template.id} value={template.id}>{template.name}</option>)}</Select></Field>
        <Button type="button" appearance="secondary" disabled={!selectedTemplate} onClick={downloadTemplate}>下载配置模板</Button>
        {selectedTemplate?.fields.filter((field) => field.required).map((field) => <TemplateField key={field.field} field={field} value={templateValues[field.field] ?? ''} disabled={submitting} onChange={(value) => changeTemplateField(field.field, value)} />)}
        {selectedTemplate?.fields.some((field) => !field.required) ? <details className={styles.advanced}><summary>高级设置</summary><div className={styles.form}>{selectedTemplate.fields.filter((field) => !field.required).map((field) => <TemplateField key={field.field} field={field} value={templateValues[field.field] ?? ''} disabled={submitting} onChange={(value) => changeTemplateField(field.field, value)} />)}</div></details> : null}
      </div> : null}
      {selectedName && configState === 'loading' ? <StatePanel state="loading" title="正在加载 Agent 配置原文" /> : null}
      {selectedName && configState === 'error' ? <StatePanel state="error" title="Agent 配置原文加载失败" actionLabel="重试" onAction={() => setConfigReload((current) => current + 1)} /> : null}
      {(!selectedName || configState === 'ready') ? <StructuredEditor format="yaml" label="Agent 配置原文" value={content} disabled={!admin || submitting} onChange={setContent} onValidationChange={(result: StructuredValidationResult) => setValid(result.valid)} /> : null}
      {admin ? <div className={styles.actions}>
        <Button appearance="primary" disabled={submitting || !valid || !isSafeKnowledgeID(draftName)} onClick={() => setConfirmSave(true)}>保存 Agent 配置</Button>
        {!creating ? <Button disabled={submitting} onClick={() => setConfirmDelete(true)}>删除 Agent 配置</Button> : null}
        <Button disabled={submitting || !valid} onClick={() => void runAction('connect')}>测试连通性</Button>
        <Field label="测试 Prompt"><Textarea value={prompt} disabled={submitting} maxLength={16_384} onChange={(_, data) => setPrompt(data.value)} /></Field>
        <Button disabled={submitting || !valid || !prompt.trim()} onClick={() => void runAction('prompt')}>Prompt 测试</Button>
      </div> : null}
      <div className={styles.actions}><Button disabled={submitting} onClick={closeEditor}>关闭</Button></div>
    </div> : null}
    <Dialog open={confirmSave} onOpenChange={(_, data) => { if (!data.open && !submitting) setConfirmSave(false) }}><DialogSurface aria-label="确认保存 Agent 配置"><DialogBody><DialogTitle>确认保存 Agent 配置</DialogTitle><DialogContent>配置可能包含访问凭据，保存前请确认内容和作用域无误。</DialogContent><DialogActions><Button onClick={() => setConfirmSave(false)}>取消</Button><Button appearance="primary" onClick={() => void runAction('save')}>确认保存</Button></DialogActions></DialogBody></DialogSurface></Dialog>
    <Dialog open={confirmDelete} onOpenChange={(_, data) => { if (!data.open && !submitting) setConfirmDelete(false) }}><DialogSurface aria-label="确认删除 Agent 配置"><DialogBody><DialogTitle>确认删除 Agent 配置</DialogTitle><DialogContent>确认删除“{draftName}”？</DialogContent><DialogActions><Button onClick={() => setConfirmDelete(false)}>取消</Button><Button appearance="primary" onClick={() => void runAction('delete')}>确认删除</Button></DialogActions></DialogBody></DialogSurface></Dialog>
  </section>
}
