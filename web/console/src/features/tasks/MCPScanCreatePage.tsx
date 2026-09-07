/**
 * 功能：通过仓库/代码包或已保存 MCP 连接创建专属扫描。
 * 实现：来源互斥、切换取消上传并清空临时数据；同次未知写入显式重试复用幂等键。
 * 输入：HTTPS 仓库、File、安全连接选项与治理模型；输出：专属创建请求和 MCP 详情导航。
 */
import { Button, Checkbox, Field, Input, MessageBar, MessageBarBody, Radio, RadioGroup, Select, Text } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ApiError } from '../../shared/api/errors'
import { StatePanel } from '../../shared/components/StatePanel'
import { useSession } from '../auth/session'
import { fetchConnectionOptions, isMCPUnknownOutcome, mcpErrorMessage, type MCPMutation } from '../mcp-connections/api'
import { AIInfraWorkbenchHeader } from './components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from './components/AIInfraWorkbench.styles'
import { GovernedModelSelector, type GovernedModelAvailability } from './components/GovernedModelSelector'
import { createMCPScanSubmission, deleteMCPAttachment, uploadMCPAttachment, type MCPAttachment, type MCPScanInput, type MCPScanResult } from './mcpScansApi'

export function MCPScanCreatePage() {
  const { state } = useSession()
  if (state.status !== 'authenticated' || state.subject.role === 'auditor') return <StatePanel state="forbidden" title="无权创建 MCP 扫描" />
  return <MCPScanForm />
}
function MCPScanForm() {
  const viewStyles = useAIInfraWorkbenchStyles()
  const navigate = useNavigate()
  const [source, setSource] = useState<'repository' | 'service'>('repository')
  const [repositoryMode, setRepositoryMode] = useState<'git' | 'upload'>('git')
  const [repositoryURL, setRepositoryURL] = useState('')
  const [selectedConnection, setSelectedConnection] = useState('')
  const [authorization, setAuthorization] = useState(false)
  const [files, setFiles] = useState<File[]>([])
  const [attachments, setAttachments] = useState<(MCPAttachment & { localFilename: string })[]>([])
  const [modelID, setModelID] = useState<string>()
  const [availability, setAvailability] = useState<GovernedModelAvailability>('available')
  const [thread, setThread] = useState('4')
  const [busy, setBusy] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [unknown, setUnknown] = useState(false)
  const [error, setError] = useState('')
  const [conflict, setConflict] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const submission = useRef<MCPMutation<MCPScanResult> | null>(null)
  const controller = useRef<AbortController | null>(null)
  const uploadController = useRef<AbortController | null>(null)
  const mounted = useRef(true)
  const submitting = useRef(false)
  const attachmentIDs = useRef<string[]>([])
  const submitted = useRef(false)
  const options = useQuery({ queryKey: ['mcp-connection-options'], queryFn: ({ signal }) => fetchConnectionOptions(signal), enabled: source === 'service', retry: false })
  const selected = options.data?.find((item) => `${item.connection_id}:${item.connection_version}` === selectedConnection)
  const changed = () => { submission.current?.clear(); submission.current = null; setUnknown(false); setError(''); setConflict(false) }
  const clearUploads = () => {
    uploadController.current?.abort(); uploadController.current = null
    const ids = attachmentIDs.current; attachmentIDs.current = []
    ids.forEach((id) => { void deleteMCPAttachment(id).catch(() => undefined) })
    setFiles([]); setAttachments([]); setUploading(false)
    if (fileInput.current) fileInput.current.value = ''
  }
  useEffect(() => { mounted.current = true; return () => {
    mounted.current = false; controller.current?.abort(); uploadController.current?.abort(); submission.current?.clear(); submission.current = null
    if (!submitted.current) attachmentIDs.current.forEach((id) => { void deleteMCPAttachment(id).catch(() => undefined) })
    attachmentIDs.current = []
  } }, [])
  const switchSource = (value: 'repository' | 'service') => { changed(); clearUploads(); setRepositoryURL(''); setSelectedConnection(''); setAuthorization(false); setSource(value) }
  const upload = async () => {
    if (uploadController.current || files.length === 0 || busy) return
    if (files.length + attachments.length > 10) { setError('最多上传 10 个代码附件。'); return }
    const request = new AbortController(); uploadController.current = request; setUploading(true); setError('')
    try {
      for (const file of files) {
        const result = await uploadMCPAttachment(file, request.signal)
        if (!mounted.current || request.signal.aborted) { void deleteMCPAttachment(result.id).catch(() => undefined); return }
        if (result.state !== 'ready' || result.size !== file.size) { void deleteMCPAttachment(result.id).catch(() => undefined); throw new ApiError('unexpected-response', 200) }
        attachmentIDs.current.push(result.id)
        setAttachments((current) => [...current, { ...result, localFilename: file.name }]); setFiles((current) => current.filter((item) => item !== file))
      }
      if (fileInput.current) fileInput.current.value = ''
      changed()
    } catch { if (mounted.current && !request.signal.aborted) setError('代码附件上传失败，请核对文件后显式重试。') }
    finally { if (uploadController.current === request) { uploadController.current = null; if (mounted.current) setUploading(false) } }
  }
  const validThread = Number.isInteger(Number(thread)) && Number(thread) >= 1 && Number(thread) <= 32
  const sourceReady = source === 'service' ? Boolean(selected && authorization && !options.isFetching && !options.isError) : repositoryMode === 'git' ? Boolean(repositoryURL.trim()) : attachments.length > 0 && files.length === 0
  const canSubmit = sourceReady && validThread && availability === 'available' && !busy && !uploading && !conflict
  const submit = async (event?: FormEvent, retry = false) => {
    event?.preventDefault()
    if (submitting.current || (!retry && !canSubmit)) return
    submitting.current = true; setBusy(true); setError('')
    const request = new AbortController(); controller.current = request
    try {
      if (!retry) {
        const common = { source_kind: source, thread: Number(thread), ...(modelID ? { model_id: modelID } : {}) }
        const input: MCPScanInput = source === 'service' && selected ? { ...common, connection_config_id: selected.connection_id, connection_config_version: selected.connection_version, authorization_confirmed: authorization } : { ...common, ...(repositoryMode === 'git' ? { repository_url: repositoryURL.trim() } : { attachment_ids: attachments.map((item) => item.id) }) }
        submission.current = createMCPScanSubmission(input)
      }
      submitted.current = true
      const result = await submission.current!.submit(request.signal)
      if (!mounted.current || request.signal.aborted) return
      submission.current?.clear(); submission.current = null
      navigate(`/tasks/mcp/${encodeURIComponent(result.task_id)}`, { replace: true })
    } catch (caught) {
      if (!mounted.current || request.signal.aborted) return
      setUnknown(isMCPUnknownOutcome(caught)); setError(mcpErrorMessage(caught))
      if (caught instanceof ApiError && caught.status === 409) { setConflict(true); setAuthorization(false) }
      if (!isMCPUnknownOutcome(caught)) { submitted.current = false; submission.current?.clear(); submission.current = null }
    } finally { submitting.current = false; if (mounted.current) setBusy(false) }
  }
  return <section className={viewStyles.page}>
    <AIInfraWorkbenchHeader title="新建 MCP 扫描" description="选择代码或已授权的 MCP 服务，创建中文安全扫描。" backLink={{ to: '/tasks/mcp', label: '返回 MCP 工作台' }} />
    {error ? <MessageBar intent="error"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
    {unknown ? <Button disabled={busy} onClick={() => void submit(undefined, true)}>重试同一提交</Button> : null}
    <form className={viewStyles.creationForm} onSubmit={(event) => void submit(event)}>
      <section className={viewStyles.surface}><h2 className={viewStyles.sectionHeading}>扫描对象</h2>
        <RadioGroup aria-label="MCP 扫描来源" value={source} className={viewStyles.sourceGrid} onChange={(_, data) => switchSource(data.value as 'repository' | 'service')}>
          <div className={viewStyles.sourceCard}><Radio value="repository" label="代码 / 仓库" disabled={busy} /><Text>审计 Git 仓库或已上传的代码包。</Text></div>
          <div className={viewStyles.sourceCard}><Radio value="service" label="MCP 服务" disabled={busy} /><Text>使用已测试并启用的连接执行受控验证。</Text></div>
        </RadioGroup>
        {source === 'repository' ? <div className={viewStyles.creationForm}>
          <Field label="代码来源"><Select value={repositoryMode} disabled={busy} onChange={(_, data) => { changed(); clearUploads(); setRepositoryURL(''); setRepositoryMode(data.value as 'git' | 'upload') }}><option value="git">Git 仓库</option><option value="upload">上传代码包</option></Select></Field>
          {repositoryMode === 'git' ? <Field label="Git 仓库地址" required hint="使用 HTTPS 地址；仓库地址与代码附件只能选择一种。"><Input type="url" value={repositoryURL} disabled={busy} placeholder="https://github.com/organization/repository" onChange={(_, data) => { changed(); setRepositoryURL(data.value) }} /></Field> : <div className={viewStyles.sourceCard}>
            <label htmlFor="mcp-code-files">代码附件</label><input id="mcp-code-files" ref={fileInput} type="file" multiple disabled={busy || uploading} onChange={(event) => { changed(); setFiles(Array.from(event.currentTarget.files ?? [])) }} />
            <Text size={200}>最多 10 个文件，每个文件不超过 50 MiB。</Text>
            <Button type="button" disabled={busy || uploading || files.length === 0} onClick={() => void upload()}>{uploading ? '正在上传代码附件' : '上传代码附件'}</Button>
            {attachments.map((item) => <div className={viewStyles.attachmentRow} key={item.id}><Text>{item.localFilename} · 已上传</Text><Button disabled={busy || uploading} onClick={() => { changed(); attachmentIDs.current = attachmentIDs.current.filter((id) => id !== item.id); setAttachments((current) => current.filter((entry) => entry.id !== item.id)); void deleteMCPAttachment(item.id).catch(() => setError('附件移除未确认，请刷新后核对。')) }}>移除 {item.localFilename}</Button></div>)}
          </div>}
        </div> : <div className={viewStyles.creationForm}>
          {options.isPending ? <StatePanel state="loading" title="正在加载可用 MCP 连接" /> : null}
          {options.isError ? <StatePanel state={options.error instanceof ApiError && options.error.status === 403 ? 'forbidden' : 'error'} title="无法读取可用 MCP 连接" actionLabel="重新加载连接" onAction={() => void options.refetch()} /> : null}
          {options.isSuccess && options.data.length === 0 ? <StatePanel state="empty" title="暂无已启用的 MCP 连接" description="请先保存连接、测试并启用。" /> : null}
          <Field label="MCP 连接配置" required><Select value={selectedConnection} disabled={busy || options.isPending} onChange={(_, data) => { changed(); setSelectedConnection(data.value); setAuthorization(false) }}><option value="">请选择已测试的连接</option>{options.data?.map((item) => <option key={`${item.connection_id}:${item.connection_version}`} value={`${item.connection_id}:${item.connection_version}`}>{item.name} · 版本 {item.connection_version}</option>)}</Select></Field>
          <div className={viewStyles.attachmentControls}><Link to="/credentials/mcp-connections">前往 MCP 连接配置</Link><Button type="button" disabled={busy || options.isFetching} onClick={() => { changed(); setSelectedConnection(''); setAuthorization(false); void options.refetch() }}>刷新可用连接</Button></div>
          <Checkbox label="我已获得此 MCP 服务的安全测试授权" checked={authorization} disabled={busy || !selected} onChange={(_, data) => { changed(); setAuthorization(data.checked === true) }} />
        </div>}
      </section>
      <section className={viewStyles.surface}><h2 className={viewStyles.sectionHeading}>扫描配置</h2><div className={viewStyles.configurationGrid}>
        <GovernedModelSelector value={modelID} disabled={busy} onChange={(value) => { changed(); setModelID(value) }} onAvailabilityChange={setAvailability} />
        <Field label="并发数" validationState={validThread ? 'none' : 'error'} validationMessage={validThread ? undefined : '并发数须为 1–32 的整数。'}><Input type="number" min={1} max={32} value={thread} disabled={busy} onChange={(_, data) => { changed(); setThread(data.value) }} /></Field>
      </div><div className={viewStyles.noteField}><Text>模型可选：不选择时仅执行基础检查；选择后增加模型辅助分析，不会自动使用默认模型。</Text></div><div className={viewStyles.noteMeta}><Text>报告语言：中文</Text></div></section>
      <section className={viewStyles.surface}><div className={viewStyles.confirmation}><Text>提交后可在 MCP 工作台跟踪扫描进度。</Text><div className={viewStyles.submitActions}><Button type="button" disabled={busy} onClick={() => navigate('/tasks/mcp')}>取消</Button><Button type="submit" appearance="primary" disabled={!canSubmit || unknown}>{busy ? '正在提交' : '创建 MCP 扫描'}</Button></div></div></section>
    </form>
  </section>
}
