/**
 * 功能：提供分步任务创建、附件上传与同逻辑提交幂等重试。
 * 实现：表单只收安全参数和模型ID，MCP 来源由白名单预设控制；附件先换取opaque ID；失败重试复用原Submission。
 * 输入：任务类型、目标/说明、安全参数和本地 File。
 * 输出：202 后导航到任务详情；错误时保留可核对表单但不自动重放写请求。
 * 依赖：Fluent UI、React Router、Session、任务及附件 API。
 */
import {
  Button,
  Checkbox,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
  Select,
  Text,
  Textarea,
  makeStyles,
  tokens,
} from '@fluentui/react-components'
import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

import { useSession } from '../auth/session'
import { ApiError } from '../../shared/api/errors'
import type { AttachmentView, InfrastructurePortScanMode, MCPSourceKind, TaskCreateRequest } from '../../shared/api/types'
import { PageHeader } from '../../shared/components/PageHeader'
import { downloadAttachment, preflightAttachments, uploadAttachment } from './attachments'
import { createTaskSubmission, type TaskSubmission } from './api'
import { previewTargetExpressions } from './targetExpressionPreview'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  form: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  step: { margin: 0, padding: tokens.spacingVerticalL, border: `1px solid ${tokens.colorNeutralStroke1}`, borderRadius: tokens.borderRadiusMedium, backgroundColor: tokens.colorNeutralBackground1, display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalM },
  fields: { display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: tokens.spacingHorizontalL },
  actions: { display: 'flex', justifyContent: 'flex-end', gap: tokens.spacingHorizontalM },
  attachment: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: tokens.spacingHorizontalM },
  targetGuidance: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalXS,
    padding: tokens.spacingVerticalM,
    borderLeft: `3px solid ${tokens.colorBrandStroke1}`,
    borderRadius: tokens.borderRadiusSmall,
    backgroundColor: tokens.colorNeutralBackground2,
  },
  targetExamples: { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: tokens.spacingHorizontalS },
  portScanMode: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalXS, gridColumn: '1 / -1' },
})

type MCPCreateSourceKind = Extract<MCPSourceKind, 'repository' | 'service'>

function mcpCreatePreset(search: string): { taskType: TaskCreateRequest['task_type']; sourceKind: MCPCreateSourceKind } {
  const query = new URLSearchParams(search)
  if (query.get('task_type') !== 'mcp_scan') return { taskType: 'mcp_scan', sourceKind: 'repository' }
  return {
    taskType: 'mcp_scan',
    sourceKind: query.get('source_kind') === 'service' ? 'service' : 'repository',
  }
}

function isMCPServiceTarget(taskType: TaskCreateRequest['task_type'], sourceKind: MCPCreateSourceKind): boolean {
  return taskType === 'mcp_scan' && sourceKind === 'service'
}

export function TaskCreatePage() {
  const styles = useStyles()
  const navigate = useNavigate()
  const location = useLocation()
  const { state } = useSession()
  const role = state.status === 'authenticated' ? state.subject.role : 'auditor'
  const preset = useMemo(() => mcpCreatePreset(location.search), [location.search])
  const [taskType, setTaskType] = useState<TaskCreateRequest['task_type']>(() => preset.taskType)
  const [mcpSourceKind, setMCPSourceKind] = useState<MCPCreateSourceKind>(() => preset.sourceKind)
  const [authorizationConfirmed, setAuthorizationConfirmed] = useState(false)
  const [content, setContent] = useState('')
  const [language, setLanguage] = useState<'zh_CN' | 'en'>('zh_CN')
  const [modelID, setModelID] = useState('')
  const [evalModelID, setEvalModelID] = useState('')
  const [agentID, setAgentID] = useState('')
  const [thread, setThread] = useState('4')
  const [timeout, setTimeoutValue] = useState('300')
  const [portScanMode, setPortScanMode] = useState<InfrastructurePortScanMode>('fixed_ai')
  const [numPrompts, setNumPrompts] = useState('100')
  const [files, setFiles] = useState<File[]>([])
  const [attachments, setAttachments] = useState<AttachmentView[]>([])
  const [error, setError] = useState('')
  const [uploading, setUploading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const submissionRef = useRef<TaskSubmission | null>(null)
  const mutexRef = useRef(false)
  const mountedRef = useRef(true)
  const uploadControllerRef = useRef<AbortController | null>(null)
  const submitControllerRef = useRef<AbortController | null>(null)
  const downloadControllerRef = useRef<AbortController | null>(null)
  const hasMCPRepositoryAttachment = taskType === 'mcp_scan' && mcpSourceKind === 'repository' && attachments.length > 0
  const contentRequired = !hasMCPRepositoryAttachment
  const targetPreview = useMemo(
    () => (taskType === 'ai_infra_scan' ? previewTargetExpressions(content) : null),
    [content, taskType],
  )

  const invalidateSubmission = () => {
    if (!mutexRef.current) submissionRef.current = null
  }

  const clearMCPServiceAttachments = useCallback(() => {
    uploadControllerRef.current?.abort()
    uploadControllerRef.current = null
    setFiles([])
    setAttachments([])
    setUploading(false)
  }, [])

  const transitionMCPConfiguration = (
    nextTaskType: TaskCreateRequest['task_type'],
    nextSourceKind: MCPCreateSourceKind,
  ) => {
    if (submitting) return
    const configurationChanged = nextTaskType !== taskType || nextSourceKind !== mcpSourceKind
    if (isMCPServiceTarget(nextTaskType, nextSourceKind)) clearMCPServiceAttachments()
    if (!configurationChanged) return
    setTaskType(nextTaskType)
    setMCPSourceKind(nextSourceKind)
    setAuthorizationConfirmed(false)
    invalidateSubmission()
  }

  const handleContentChange = (value: string) => {
    if (isMCPServiceTarget(taskType, mcpSourceKind) && value !== content) setAuthorizationConfirmed(false)
    setContent(value)
    invalidateSubmission()
  }

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      uploadControllerRef.current?.abort()
      submitControllerRef.current?.abort()
      downloadControllerRef.current?.abort()
    }
  }, [])

  useEffect(() => {
    if (mutexRef.current) return
    setTaskType(preset.taskType)
    setMCPSourceKind(preset.sourceKind)
    setAuthorizationConfirmed(false)
    submissionRef.current = null
    if (isMCPServiceTarget(preset.taskType, preset.sourceKind)) clearMCPServiceAttachments()
  }, [clearMCPServiceAttachments, preset.sourceKind, preset.taskType])

  const chooseMCPSource = (value: string) => {
    const sourceKind: MCPCreateSourceKind = value === 'service' ? 'service' : 'repository'
    transitionMCPConfiguration(taskType, sourceKind)
  }

  const handleUpload = async () => {
    if (taskType === 'mcp_scan' && mcpSourceKind === 'service') {
      setError('服务扫描不能携带代码附件。')
      return
    }
    if (uploading || submitting || files.length === 0) return
    setError('')
    try {
      preflightAttachments([...attachments.map((item) => new File(['x'], item.filename)), ...files])
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '附件校验失败。')
      return
    }
    const controller = new AbortController()
    uploadControllerRef.current?.abort()
    uploadControllerRef.current = controller
    setUploading(true)
    try {
      for (const file of files) {
        const uploaded = await uploadAttachment(file, controller.signal)
        if (!mountedRef.current || controller.signal.aborted) return
        setAttachments((current) => [...current, uploaded])
        if (taskType === 'mcp_scan' && mcpSourceKind === 'repository') setContent('')
        setFiles((current) => current.filter((candidate) => candidate !== file))
        invalidateSubmission()
      }
    } catch {
      if (mountedRef.current && !controller.signal.aborted) setError('附件上传失败，请核对后显式重试。')
    } finally {
      if (uploadControllerRef.current === controller) {
        uploadControllerRef.current = null
        if (mountedRef.current) setUploading(false)
      }
    }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const serviceScan = taskType === 'mcp_scan' && mcpSourceKind === 'service'
    const repositoryAttachmentScan = taskType === 'mcp_scan' && mcpSourceKind === 'repository' && attachments.length > 0
    if (serviceScan && (files.length > 0 || attachments.length > 0)) {
      setError('服务扫描不能携带代码附件。')
      return
    }
    if (serviceScan && !authorizationConfirmed) {
      setError('请确认已获得该目标的安全测试授权。')
      return
    }
    if (repositoryAttachmentScan && content !== '') {
      setError('代码附件扫描不能同时填写扫描目标或任务说明。')
      return
    }
    if (uploading || files.length > 0) {
      setError('请先完成已选择附件的上传。')
      return
    }
    if (taskType === 'ai_infra_scan' && targetPreview && !targetPreview.ok) {
      setError(targetPreview.error)
      return
    }
    if (!content.trim() && !repositoryAttachmentScan) {
      setError('请填写扫描目标或任务说明。')
      return
    }
    if (mutexRef.current) return
    mutexRef.current = true
    setSubmitting(true)
    setError('')
    const controller = new AbortController()
    submitControllerRef.current = controller
    try {
      const params: TaskCreateRequest['params'] = {}
      if (taskType === 'mcp_scan') {
        params.source_kind = mcpSourceKind
        if (mcpSourceKind === 'service') params.authorization_confirmed = true
        if (modelID.trim()) params.model_id = modelID.trim()
        const parsed = Number(thread)
        if (!Number.isInteger(parsed) || parsed < 1 || parsed > 1_024) throw new Error('请填写有效的并发数。')
        params.thread = parsed
      }
      if (taskType === 'ai_infra_scan') {
        if (modelID.trim()) params.model_id = modelID.trim()
        const parsed = Number(timeout)
        if (!Number.isInteger(parsed) || parsed < 1 || parsed > 86_400) throw new Error('请填写有效的超时秒数。')
        params.timeout = parsed
        params.port_scan_mode = portScanMode
      }
      if (taskType === 'model_redteam_report') {
        const modelIDs = modelID.split(',').map((value) => value.trim()).filter(Boolean)
        if (modelIDs.length === 0 || modelIDs.length > 10 || new Set(modelIDs).size !== modelIDs.length) {
          throw new Error('请填写有效且不重复的评测模型 ID。')
        }
        if (!evalModelID.trim()) throw new Error('请填写裁判模型 ID。')
        params.model_id = modelIDs
        params.eval_model_id = evalModelID.trim()
        const parsed = Number(numPrompts)
        if (!Number.isInteger(parsed) || parsed < 1 || parsed > 1_000_000) throw new Error('请填写有效的提示词数量。')
        params.dataset = { numPrompts: parsed }
      }
      if (taskType === 'agent_scan') {
        if (!agentID.trim()) throw new Error('请填写 Agent 配置 ID。')
        if (!evalModelID.trim()) throw new Error('请填写裁判模型 ID。')
        params.agent_id = agentID.trim()
        params.eval_model_id = evalModelID.trim()
      }
      const input: TaskCreateRequest = {
        task_type: taskType,
        content,
        params,
        attachment_ids: serviceScan ? [] : attachments.map((item) => item.id),
        country_iso_code: language,
      }
      submissionRef.current ??= createTaskSubmission(input)
      const created = await submissionRef.current.submit(controller.signal)
      navigate(`/tasks/${encodeURIComponent(created.id)}`, { replace: true })
    } catch (caught) {
      if (mountedRef.current && !controller.signal.aborted) {
        if (caught instanceof ApiError && caught.kind === 'bad-request') {
          setError(caught.message)
        } else {
          setError(caught instanceof Error && caught.message.startsWith('请填写') ? caught.message : '任务创建未确认，显式重试将复用同一幂等键。')
        }
      }
    } finally {
      if (submitControllerRef.current === controller) submitControllerRef.current = null
      mutexRef.current = false
      if (mountedRef.current) setSubmitting(false)
    }
  }

  const handleDownload = async (attachmentID: string) => {
    const controller = new AbortController()
    downloadControllerRef.current?.abort()
    downloadControllerRef.current = controller
    setError('')
    try {
      await downloadAttachment(attachmentID, role, controller.signal)
    } catch {
      if (mountedRef.current && !controller.signal.aborted) setError('附件下载失败，请稍后重试。')
    } finally {
      if (downloadControllerRef.current === controller) downloadControllerRef.current = null
    }
  }

  return (
    <section className={styles.page}>
      <PageHeader title="创建扫描任务" description="按类型、参数、附件和确认顺序提交；浏览器不接收模型密钥。" />
      {error ? <MessageBar intent="error"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
      <form className={styles.form} onSubmit={submit}>
        <fieldset className={styles.step} aria-label="第一步：任务类型" disabled={submitting}>
          <Text weight="semibold">第一步：任务类型</Text>
          <Field label="扫描类型">
            <Select value={taskType} disabled={submitting} onChange={(_, data) => transitionMCPConfiguration(data.value as TaskCreateRequest['task_type'], mcpSourceKind)}>
              <option value="mcp_scan">MCP 扫描</option>
              <option value="ai_infra_scan">AI 基础设施扫描</option>
              <option value="model_redteam_report">模型红队评测</option>
              <option value="agent_scan">Agent 扫描</option>
            </Select>
          </Field>
        </fieldset>
        <fieldset className={styles.step} aria-label="第二步：参数" disabled={submitting}>
          <Text weight="semibold">第二步：参数</Text>
          <Field label="扫描目标或任务说明" required={contentRequired}>
            <Textarea
              value={content}
              onChange={(_, data) => handleContentChange(data.value)}
              resize="vertical"
              aria-invalid={taskType === 'ai_infra_scan' && targetPreview && !targetPreview.ok ? true : undefined}
              aria-describedby={taskType === 'ai_infra_scan' ? 'ai-infra-target-guidance ai-infra-target-preview' : undefined}
            />
          </Field>
          {taskType === 'ai_infra_scan' && targetPreview ? (
            <aside id="ai-infra-target-guidance" className={styles.targetGuidance} aria-label="AI 基础设施扫描目标格式">
              <Text weight="semibold">AI 基础设施扫描目标格式</Text>
              <Text size={200}>每行一条目标，可直接一次粘贴多行。支持单个 URL、域名、IPv4、IPv4 CIDR、闭区间范围和末尾通配符。</Text>
              <div className={styles.targetExamples} aria-label="目标格式示例">
                <code>192.168.10.2-192.168.10.10</code>
                <code>104.147.75.1-104.147.75.10</code>
                <code>22.2.10.*</code>
              </div>
              <Text size={200}>无效示例：<code>104.147.75.1\~104.147.75.10</code>；范围必须使用标准连字符 <code>-</code>。</Text>
              <Text size={200}>最多 65,536 个展开后的唯一目标。大网段或通配符会显著增加扫描耗时和网络压力，请仅扫描已获授权的范围。</Text>
              <Text size={200}>附件会在服务端按 UTF-8 目标清单校验（每个不超过 1 MiB）；正文与附件合并后的最终数量以服务端判定为准。</Text>
              {targetPreview.ok ? (
                <Text id="ai-infra-target-preview" role="status" aria-live="polite" weight="semibold">已识别 {targetPreview.count.toLocaleString('zh-CN')} 个目标</Text>
              ) : (
                <Text id="ai-infra-target-preview" role="alert" aria-live="assertive" weight="semibold">{targetPreview.error}</Text>
              )}
            </aside>
          ) : null}
          <div className={styles.fields}>
            <Field label="语言">
              <Select value={language} onChange={(_, data) => { setLanguage(data.value as 'zh_CN' | 'en'); invalidateSubmission() }}>
                <option value="zh_CN">中文</option><option value="en">英文</option>
              </Select>
            </Field>
            {taskType === 'mcp_scan' ? (
              <Field label="MCP 扫描对象">
                <Select value={mcpSourceKind} disabled={submitting} onChange={(_, data) => chooseMCPSource(data.value)}>
                  <option value="repository">代码仓库或代码压缩包扫描</option>
                  <option value="service">受控运行服务扫描</option>
                </Select>
              </Field>
            ) : null}
            {taskType === 'mcp_scan' || taskType === 'ai_infra_scan' ? (
              <Field label="模型 ID" hint="仅填写平台模型 ID，不填写密钥。">
                <Input value={modelID} onChange={(_, data) => { setModelID(data.value); invalidateSubmission() }} autoComplete="off" />
              </Field>
            ) : null}
            {taskType === 'model_redteam_report' ? (
              <Field label="评测模型 ID（逗号分隔）" hint="仅填写平台模型 ID，不填写密钥。">
                <Input value={modelID} onChange={(_, data) => { setModelID(data.value); invalidateSubmission() }} autoComplete="off" />
              </Field>
            ) : null}
            {taskType === 'agent_scan' ? (
              <Field label="Agent 配置 ID">
                <Input value={agentID} onChange={(_, data) => { setAgentID(data.value); invalidateSubmission() }} autoComplete="off" />
              </Field>
            ) : null}
            {taskType === 'model_redteam_report' || taskType === 'agent_scan' ? (
              <Field label="裁判模型 ID" hint="仅填写平台模型 ID，不填写密钥。">
                <Input value={evalModelID} onChange={(_, data) => { setEvalModelID(data.value); invalidateSubmission() }} autoComplete="off" />
              </Field>
            ) : null}
            {taskType === 'mcp_scan' ? <Field label="并发数"><Input type="number" min={1} max={1024} value={thread} onChange={(_, data) => { setThread(data.value); invalidateSubmission() }} /></Field> : null}
            {taskType === 'mcp_scan' && mcpSourceKind === 'service' ? (
              <div className={styles.portScanMode}>
                <Text weight="semibold">服务扫描授权声明</Text>
                <Checkbox
                  label="我确认已获得该目标的安全测试授权"
                  aria-required="true"
                  checked={authorizationConfirmed}
                  onChange={(_, data) => { setAuthorizationConfirmed(data.checked === true); invalidateSubmission() }}
                />
              </div>
            ) : null}
            {taskType === 'ai_infra_scan' ? <Field label="超时秒数"><Input type="number" min={1} max={86400} value={timeout} onChange={(_, data) => { setTimeoutValue(data.value); invalidateSubmission() }} /></Field> : null}
            {taskType === 'ai_infra_scan' ? (
              <div className={styles.portScanMode}>
                <Field label="端口扫描模式">
                  <Select value={portScanMode} onChange={(_, data) => { setPortScanMode(data.value as InfrastructurePortScanMode); invalidateSubmission() }}>
                    <option value="fixed_ai">固定 AI 端口及范围</option>
                    <option value="full_tcp">全量 TCP（1–65535）</option>
                  </Select>
                </Field>
                {portScanMode === 'fixed_ai' ? (
                  <Text size={200}>固定 AI 端口：11434、1337、7000–9000、18789（共 2,004 个端口）。</Text>
                ) : (
                  <MessageBar intent="warning">
                    <MessageBarBody>
                      <Text weight="semibold">全量 TCP 1–65535</Text>
                      <Text size={200}>仅对裸 IPv4 执行 TCP 1–65535；会显著增加扫描耗时和网络压力，请仅扫描已获授权的目标。URL 和域名继续沿用现有 Web 扫描路径。</Text>
                    </MessageBarBody>
                  </MessageBar>
                )}
              </div>
            ) : null}
            {taskType === 'model_redteam_report' ? <Field label="提示词数量"><Input type="number" min={1} max={1000000} value={numPrompts} onChange={(_, data) => { setNumPrompts(data.value); invalidateSubmission() }} /></Field> : null}
          </div>
        </fieldset>
        {!(taskType === 'mcp_scan' && mcpSourceKind === 'service') ? (
          <fieldset className={styles.step} aria-label="第三步：附件">
            <Text weight="semibold">第三步：附件</Text>
            <Field label="选择附件" hint="单文件最大 50 MiB，超过 5 MiB 时自动分片。">
              <input type="file" multiple disabled={uploading || submitting} onChange={(event) => { setFiles(Array.from(event.currentTarget.files ?? [])); invalidateSubmission() }} />
            </Field>
            <Button type="button" appearance="secondary" disabled={uploading || submitting || files.length === 0} onClick={() => void handleUpload()}>{uploading ? '正在上传' : '上传附件'}</Button>
            {attachments.map((attachment) => (
              <div className={styles.attachment} key={attachment.id}>
                <Text>{attachment.filename}（{attachment.size} 字节）</Text>
                {role !== 'auditor' ? <Button type="button" appearance="subtle" disabled={uploading || submitting} onClick={() => void handleDownload(attachment.id)}>下载附件 {attachment.filename}</Button> : null}
              </div>
            ))}
          </fieldset>
        ) : null}
        <fieldset className={styles.step} aria-label="第四步：确认">
          <Text weight="semibold">第四步：确认</Text>
          <Text>提交后将创建真实扫描任务；网络失败不会自动创建第二个任务。</Text>
          <div className={styles.actions}><Button type="button" appearance="secondary" onClick={() => navigate('/tasks')}>取消</Button><Button type="submit" appearance="primary" disabled={submitting || uploading || files.length > 0}>{submitting ? '正在提交' : '创建任务'}</Button></div>
        </fieldset>
      </form>
    </section>
  )
}
