/**
 * 功能：提供分步任务创建、附件上传与同逻辑提交幂等重试。
 * 实现：表单只收安全参数和模型ID，附件先换取opaque ID；失败重试复用原Submission。
 * 输入：任务类型、目标/说明、安全参数和本地 File。
 * 输出：202 后导航到任务详情；错误时保留可核对表单但不自动重放写请求。
 * 依赖：Fluent UI、React Router、Session、任务及附件 API。
 */
import {
  Button,
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
import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'

import { useSession } from '../auth/session'
import { ApiError } from '../../shared/api/errors'
import type { AttachmentView, InfrastructurePortScanMode, TaskCreateRequest } from '../../shared/api/types'
import { PageHeader } from '../../shared/components/PageHeader'
import { downloadAttachment, preflightAttachments, uploadAttachment } from './attachments'
import { createTaskSubmission, type TaskSubmission } from './api'
import { AIInfraWorkbenchHeader } from './components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from './components/AIInfraWorkbench.styles'
import { GovernedModelSelector, type GovernedModelAvailability } from './components/GovernedModelSelector'
import { TaskTypeSelector } from './components/TaskTypeSelector'
import { SkillsTaskCreatePage } from './SkillsTaskCreatePage'
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

const MAX_AI_TARGET_LIST_BYTES = 1_024 * 1_024
const MAX_TASK_REMARK_CODE_POINTS = 2_000

function codePointLength(value: string): number {
  return Array.from(value).length
}

function isWellFormedUnicode(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const unit = value.charCodeAt(index)
    if (unit >= 0xD800 && unit <= 0xDBFF) {
      if (index + 1 >= value.length) return false
      const next = value.charCodeAt(index + 1)
      if (next < 0xDC00 || next > 0xDFFF) return false
      index += 1
      continue
    }
    if (unit >= 0xDC00 && unit <= 0xDFFF) return false
  }
  return true
}

export interface TaskCreatePageProps {
  fixedTaskType?: 'ai_infra_scan' | 'skills_scan'
  returnTo?: string
}

export function TaskCreatePage({ fixedTaskType, returnTo }: TaskCreatePageProps) {
  const [taskType, setTaskType] = useState<TaskCreateRequest['task_type']>('mcp_scan')
  if ((fixedTaskType ?? taskType) === 'skills_scan') {
    return <SkillsTaskCreatePage returnTo={returnTo ?? (fixedTaskType ? '/tasks/skills' : '/tasks')} onTaskTypeChange={fixedTaskType ? undefined : setTaskType} />
  }
  return <StandardTaskCreatePage fixedTaskType={fixedTaskType === 'ai_infra_scan' ? fixedTaskType : undefined} returnTo={returnTo} taskType={taskType} setTaskType={setTaskType} />
}

function StandardTaskCreatePage({ fixedTaskType, returnTo, taskType, setTaskType }: {
  fixedTaskType?: 'ai_infra_scan'
  returnTo?: string
  taskType: TaskCreateRequest['task_type']
  setTaskType: (taskType: TaskCreateRequest['task_type']) => void
}) {
  const styles = useStyles()
  const workbenchStyles = useAIInfraWorkbenchStyles()
  const navigate = useNavigate()
  const { state } = useSession()
  const role = state.status === 'authenticated' ? state.subject.role : 'auditor'
  const [content, setContent] = useState('')
  const [remark, setRemark] = useState('')
  const [language, setLanguage] = useState<'zh_CN' | 'en'>('zh_CN')
  const [modelID, setModelID] = useState('')
  const [modelAvailability, setModelAvailability] = useState<GovernedModelAvailability>('available')
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
  const controllerRef = useRef<AbortController | null>(null)
  const effectiveTaskType = fixedTaskType ?? taskType
  const isDedicatedAI = fixedTaskType === 'ai_infra_scan'
  const targetPreview = useMemo(
    () => (effectiveTaskType === 'ai_infra_scan' ? previewTargetExpressions(content) : null),
    [content, effectiveTaskType],
  )
  const remarkCodePointCount = codePointLength(remark)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      controllerRef.current?.abort()
    }
  }, [])

  const invalidateSubmission = () => {
    submissionRef.current = null
  }

  const handleUpload = async () => {
    if (uploading || files.length === 0) return
    setError('')
    if (isDedicatedAI && files.some((file) => file.size > MAX_AI_TARGET_LIST_BYTES)) {
      setError('目标清单文件不能超过 1 MiB，请缩小后重试。')
      return
    }
    try {
      preflightAttachments([...attachments.map((item) => new File(['x'], item.filename)), ...files])
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : '附件校验失败。')
      return
    }
    const controller = new AbortController()
    controllerRef.current?.abort()
    controllerRef.current = controller
    setUploading(true)
    try {
      for (const file of files) {
        const uploaded = await uploadAttachment(file, controller.signal)
        if (!mountedRef.current || controller.signal.aborted) return
        setAttachments((current) => [...current, uploaded])
        setFiles((current) => current.filter((candidate) => candidate !== file))
        invalidateSubmission()
      }
    } catch {
      if (mountedRef.current && !controller.signal.aborted) setError('附件上传失败，请核对后显式重试。')
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
      if (mountedRef.current) setUploading(false)
    }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (uploading || files.length > 0) {
      setError('请先完成已选择附件的上传。')
      return
    }
    if (effectiveTaskType === 'ai_infra_scan' && targetPreview && !targetPreview.ok) {
      setError(targetPreview.error)
      return
    }
    if (isDedicatedAI && modelID && modelAvailability !== 'available') {
      setError('扫描模型尚未确认可用，请等待目录验证完成或选择不使用模型。')
      return
    }
    if (mutexRef.current) return
    mutexRef.current = true
    setSubmitting(true)
    setError('')
    const controller = new AbortController()
    controllerRef.current?.abort()
    controllerRef.current = controller
    try {
      const hasManualTarget = content.trim().length > 0
      const hasImportedTargetList = attachments.length > 0
      if (isDedicatedAI && !hasManualTarget && !hasImportedTargetList) throw new Error('请填写扫描目标或导入目标清单。')
      if (!isDedicatedAI && !hasManualTarget) throw new Error('请填写扫描目标或任务说明。')
      const normalizedRemark = isDedicatedAI ? remark.trim() : ''
      if (normalizedRemark && !isWellFormedUnicode(normalizedRemark)) throw new Error('任务说明包含无效字符。')
      if (normalizedRemark && codePointLength(normalizedRemark) > MAX_TASK_REMARK_CODE_POINTS) {
        throw new Error('任务说明不能超过 2,000 个字符。')
      }
      const params: TaskCreateRequest['params'] = {}
      if (effectiveTaskType === 'mcp_scan') {
        if (modelID.trim()) params.model_id = modelID.trim()
        const parsed = Number(thread)
        if (!Number.isInteger(parsed) || parsed < 1 || parsed > 1_024) throw new Error('请填写有效的并发数。')
        params.thread = parsed
      }
      if (effectiveTaskType === 'ai_infra_scan') {
        if (modelID.trim()) params.model_id = modelID.trim()
        const parsed = Number(timeout)
        if (!Number.isInteger(parsed) || parsed < 1 || parsed > 86_400) throw new Error('请填写有效的超时秒数。')
        params.timeout = parsed
        params.port_scan_mode = portScanMode
      }
      if (effectiveTaskType === 'model_redteam_report') {
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
      if (effectiveTaskType === 'agent_scan') {
        if (!agentID.trim()) throw new Error('请填写 Agent 配置 ID。')
        if (!evalModelID.trim()) throw new Error('请填写裁判模型 ID。')
        params.agent_id = agentID.trim()
        params.eval_model_id = evalModelID.trim()
      }
      const input: TaskCreateRequest = {
        task_type: effectiveTaskType,
        content,
        params,
        attachment_ids: attachments.map((item) => item.id),
        country_iso_code: isDedicatedAI ? 'zh_CN' : language,
        ...(normalizedRemark ? { remark: normalizedRemark } : {}),
      }
      submissionRef.current ??= createTaskSubmission(input)
      const created = await submissionRef.current.submit(controller.signal)
      navigate(`${returnTo ?? '/tasks'}/${encodeURIComponent(created.id)}`, { replace: true })
    } catch (caught) {
      if (mountedRef.current && !controller.signal.aborted) {
        if (caught instanceof ApiError && caught.kind === 'bad-request') {
          setError(caught.message)
        } else {
          const localError = caught instanceof Error ? caught.message : ''
          setError(
            localError.startsWith('请填写') || localError === '任务说明包含无效字符。' || localError === '任务说明不能超过 2,000 个字符。'
              ? localError
              : '任务创建未确认，显式重试将复用同一幂等键。',
          )
        }
      }
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
      mutexRef.current = false
      if (mountedRef.current) setSubmitting(false)
    }
  }

  const handleDownload = async (attachmentID: string) => {
    const controller = new AbortController()
    controllerRef.current?.abort()
    controllerRef.current = controller
    setError('')
    try {
      await downloadAttachment(attachmentID, role, controller.signal)
    } catch {
      if (mountedRef.current && !controller.signal.aborted) setError('附件下载失败，请稍后重试。')
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
    }
  }

  const aiConfigFields = (
    <div className={workbenchStyles.configurationGrid}>
      <GovernedModelSelector
        value={modelID || undefined}
        onChange={(modelID) => { setModelID(modelID ?? ''); invalidateSubmission() }}
        onAvailabilityChange={setModelAvailability}
        disabled={submitting || uploading}
      />
      <Field label="超时秒数"><Input type="number" min={1} max={86400} value={timeout} onChange={(_, data) => { setTimeoutValue(data.value); invalidateSubmission() }} /></Field>
      <div className={`${styles.portScanMode} ${workbenchStyles.fullWidth}`}>
        <Field label="端口扫描模式">
          <Select value={portScanMode} onChange={(_, data) => { setPortScanMode(data.value as InfrastructurePortScanMode); invalidateSubmission() }}>
            <option value="fixed_ai">固定 AI 端口及范围</option><option value="full_tcp">全量 TCP（1–65535）</option>
          </Select>
        </Field>
        {portScanMode === 'fixed_ai' ? <Text size={200}>固定 AI 端口：11434、1337、7000–9000、18789（共 2,004 个端口）。</Text> : <MessageBar intent="warning"><MessageBarBody><Text weight="semibold">全量 TCP 1–65535</Text><Text size={200}>仅对裸 IPv4 执行 TCP 1–65535；会显著增加扫描耗时和网络压力，请仅扫描已获授权的目标。URL 和域名继续沿用现有 Web 扫描路径。</Text></MessageBarBody></MessageBar>}
      </div>
    </div>
  )

  if (isDedicatedAI) {
    return (
      <section className={workbenchStyles.page}>
        <AIInfraWorkbenchHeader
          title="新建 AI 基础设施扫描任务"
          description="配置目标来源和扫描参数后提交；浏览器不接收模型密钥。"
          backLink={{ to: returnTo ?? '/tasks/ai-infra', label: '返回 AI 基础设施扫描任务台' }}
        />
        {error ? <MessageBar intent="error"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
        <form className={workbenchStyles.creationForm} onSubmit={submit}>
          <section className={workbenchStyles.surface} aria-labelledby="ai-infra-source-heading">
            <h2 id="ai-infra-source-heading" className={`${workbenchStyles.sectionHeader} ${workbenchStyles.sectionHeading}`}>
              <span className={workbenchStyles.stepNumber}>1</span>
              <span>扫描对象</span>
            </h2>
            <div className={workbenchStyles.sourceGrid}>
              <div className={workbenchStyles.sourceCard}>
                <Text className={workbenchStyles.sourceTitle}>手工填写扫描目标</Text>
                <Text className={workbenchStyles.sourceDescription}>手工填写和导入的目标清单会在服务端合并、校验并去重，最终数量以服务端判定为准。</Text>
                <Field label="手工填写扫描目标（可选）">
                  <Textarea
                    className={workbenchStyles.largeTextarea}
                    value={content}
                    onChange={(_, data) => { setContent(data.value); invalidateSubmission() }}
                    resize="vertical"
                    aria-invalid={targetPreview && !targetPreview.ok ? true : undefined}
                    aria-describedby="ai-infra-target-guidance ai-infra-target-preview"
                  />
                </Field>
                {targetPreview ? (
                  <aside id="ai-infra-target-guidance" className={workbenchStyles.targetGuidance} aria-label="AI 基础设施扫描目标格式">
                    <Text weight="semibold">AI 基础设施扫描目标格式</Text>
                    <Text size={200}>每行一条目标，可直接一次粘贴多行。支持单个 URL、域名、IPv4、IPv4 CIDR、闭区间范围和末尾通配符。</Text>
                    <div className={workbenchStyles.targetExamples} aria-label="目标格式示例"><code>192.168.10.2-192.168.10.10</code><code>104.147.75.1-104.147.75.10</code><code>22.2.10.*</code></div>
                    <Text size={200}>无效示例：<code>104.147.75.1\~104.147.75.10</code>；范围必须使用标准连字符 <code>-</code>。</Text>
                    <Text size={200}>最多 65,536 个展开后的唯一目标。大网段或通配符会显著增加扫描耗时和网络压力，请仅扫描已获授权的范围。</Text>
                    <Text size={200}>这里只预览手工填写内容；导入清单会由服务端按 UTF-8 校验，两个来源合并后的最终数量以服务端判定为准。</Text>
                    {targetPreview.ok ? <Text id="ai-infra-target-preview" role="status" aria-live="polite" weight="semibold">已识别 {targetPreview.count.toLocaleString('zh-CN')} 个目标</Text> : <Text id="ai-infra-target-preview" role="alert" aria-live="assertive" weight="semibold">{targetPreview.error}</Text>}
                  </aside>
                ) : null}
              </div>
              <div className={workbenchStyles.sourceCard}>
                <Text className={workbenchStyles.sourceTitle}>导入目标清单</Text>
                <Text className={workbenchStyles.sourceDescription}>目标清单会与手工填写内容在服务端合并、校验并去重，最终数量以服务端判定为准。</Text>
                <label className={workbenchStyles.sourceTitle} htmlFor="ai-infra-target-list">导入目标清单（可选）</label>
                <input
                  id="ai-infra-target-list"
                  type="file"
                  multiple
                  disabled={uploading || submitting}
                  onChange={(event) => { setFiles(Array.from(event.currentTarget.files ?? [])); invalidateSubmission() }}
                />
                <Text className={workbenchStyles.sourceDescription} size={200}>UTF-8 文本清单；每个文件不超过 1 MiB，服务端会再次校验。</Text>
                <div className={workbenchStyles.attachmentControls}>
                  <Button type="button" appearance="secondary" disabled={uploading || submitting || files.length === 0} onClick={() => void handleUpload()}>
                    {uploading ? '正在上传目标清单' : '上传目标清单'}
                  </Button>
                  {files.length > 0 ? <Text size={200}>已选择 {files.length} 个待上传清单</Text> : null}
                </div>
                {attachments.length > 0 ? (
                  <div className={workbenchStyles.attachmentList} aria-label="已上传目标清单">
                    {attachments.map((attachment) => (
                      <div className={workbenchStyles.attachmentRow} key={attachment.id}>
                        <Text>{attachment.filename}（{attachment.size} 字节）</Text>
                        {role !== 'auditor' ? <Button type="button" appearance="subtle" disabled={uploading || submitting} onClick={() => void handleDownload(attachment.id)}>下载附件 {attachment.filename}</Button> : null}
                      </div>
                    ))}
                  </div>
                ) : null}
              </div>
            </div>
            <Field className={workbenchStyles.noteField} label="任务说明 / 备注（可选）">
              <Textarea
                className={workbenchStyles.largeTextarea}
                value={remark}
                onChange={(_, data) => { setRemark(data.value); invalidateSubmission() }}
                resize="vertical"
                aria-invalid={remark.trim() && (!isWellFormedUnicode(remark.trim()) || codePointLength(remark.trim()) > MAX_TASK_REMARK_CODE_POINTS) ? true : undefined}
                aria-describedby="ai-infra-remark-meta"
              />
            </Field>
            <div id="ai-infra-remark-meta" className={workbenchStyles.noteMeta}>
              <Text size={200}>任务说明会作为本次扫描任务的保留说明，不会写入扫描配置。</Text>
              <Text size={200} role="status" aria-live="polite">{remarkCodePointCount.toLocaleString('zh-CN')} / {MAX_TASK_REMARK_CODE_POINTS.toLocaleString('zh-CN')}</Text>
            </div>
          </section>

          <section className={workbenchStyles.surface} aria-labelledby="ai-infra-config-heading">
            <h2 id="ai-infra-config-heading" className={`${workbenchStyles.sectionHeader} ${workbenchStyles.sectionHeading}`}>
              <span className={workbenchStyles.stepNumber}>2</span>
              <span>扫描配置</span>
            </h2>
            {aiConfigFields}
          </section>

          <section className={workbenchStyles.surface} aria-labelledby="ai-infra-submit-heading">
            <h2 id="ai-infra-submit-heading" className={`${workbenchStyles.sectionHeader} ${workbenchStyles.sectionHeading}`}>
              <span className={workbenchStyles.stepNumber}>3</span>
              <span>确认并提交</span>
            </h2>
            <div className={workbenchStyles.confirmation}>
              <Text className={workbenchStyles.confirmationCopy}>仅在已获授权的目标范围内创建真实扫描任务。提交会使用幂等键；网络中断后需要由您显式重试，系统不会自动创建重复任务。</Text>
              <div className={workbenchStyles.submitActions}>
                <Button type="button" appearance="secondary" disabled={submitting || uploading} onClick={() => navigate(returnTo ?? '/tasks/ai-infra')}>取消</Button>
                <Button className={workbenchStyles.submitPrimary} type="submit" appearance="primary" disabled={submitting || uploading || files.length > 0}>{submitting ? '正在提交' : '创建 AI 基础设施扫描任务'}</Button>
              </div>
            </div>
          </section>
        </form>
      </section>
    )
  }

  return (
    <section className={styles.page}>
      <PageHeader title="创建扫描任务" description="按类型、参数、附件和确认顺序提交；浏览器不接收模型密钥。" />
      {error ? <MessageBar intent="error"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
      <form className={styles.form} onSubmit={submit}>
        <fieldset className={styles.step} aria-label="第一步：任务类型">
          <Text weight="semibold">第一步：任务类型</Text>
          <TaskTypeSelector value={taskType} onChange={(type) => { setTaskType(type); invalidateSubmission() }} disabled={uploading || submitting} />
        </fieldset>
        <fieldset className={styles.step} aria-label="第二步：参数">
          <Text weight="semibold">第二步：参数</Text>
          <Field label="扫描目标或任务说明" required>
            <Textarea
              value={content}
              onChange={(_, data) => { setContent(data.value); invalidateSubmission() }}
              resize="vertical"
              aria-invalid={effectiveTaskType === 'ai_infra_scan' && targetPreview && !targetPreview.ok ? true : undefined}
              aria-describedby={effectiveTaskType === 'ai_infra_scan' ? 'ai-infra-target-guidance ai-infra-target-preview' : undefined}
            />
          </Field>
          {effectiveTaskType === 'ai_infra_scan' && targetPreview ? (
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
            {effectiveTaskType === 'mcp_scan' || effectiveTaskType === 'ai_infra_scan' ? (
              <Field label="模型 ID" hint="仅填写平台模型 ID，不填写密钥。">
                <Input value={modelID} onChange={(_, data) => { setModelID(data.value); invalidateSubmission() }} autoComplete="off" />
              </Field>
            ) : null}
            {effectiveTaskType === 'model_redteam_report' ? (
              <Field label="评测模型 ID（逗号分隔）" hint="仅填写平台模型 ID，不填写密钥。">
                <Input value={modelID} onChange={(_, data) => { setModelID(data.value); invalidateSubmission() }} autoComplete="off" />
              </Field>
            ) : null}
            {effectiveTaskType === 'agent_scan' ? (
              <Field label="Agent 配置 ID">
                <Input value={agentID} onChange={(_, data) => { setAgentID(data.value); invalidateSubmission() }} autoComplete="off" />
              </Field>
            ) : null}
            {effectiveTaskType === 'model_redteam_report' || effectiveTaskType === 'agent_scan' ? (
              <Field label="裁判模型 ID" hint="仅填写平台模型 ID，不填写密钥。">
                <Input value={evalModelID} onChange={(_, data) => { setEvalModelID(data.value); invalidateSubmission() }} autoComplete="off" />
              </Field>
            ) : null}
            {effectiveTaskType === 'mcp_scan' ? <Field label="并发数"><Input type="number" min={1} max={1024} value={thread} onChange={(_, data) => { setThread(data.value); invalidateSubmission() }} /></Field> : null}
            {effectiveTaskType === 'ai_infra_scan' ? <Field label="超时秒数"><Input type="number" min={1} max={86400} value={timeout} onChange={(_, data) => { setTimeoutValue(data.value); invalidateSubmission() }} /></Field> : null}
            {effectiveTaskType === 'ai_infra_scan' ? (
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
            {effectiveTaskType === 'model_redteam_report' ? <Field label="提示词数量"><Input type="number" min={1} max={1000000} value={numPrompts} onChange={(_, data) => { setNumPrompts(data.value); invalidateSubmission() }} /></Field> : null}
          </div>
        </fieldset>
        <fieldset className={styles.step} aria-label="第三步：附件">
          <Text weight="semibold">第三步：附件</Text>
          <Field label="选择附件" hint="单文件最大 50 MiB，超过 5 MiB 时自动分片。">
            <input type="file" multiple disabled={uploading || submitting} onChange={(event) => setFiles(Array.from(event.currentTarget.files ?? []))} />
          </Field>
          <Button type="button" appearance="secondary" disabled={uploading || files.length === 0} onClick={() => void handleUpload()}>{uploading ? '正在上传' : '上传附件'}</Button>
          {attachments.map((attachment) => (
            <div className={styles.attachment} key={attachment.id}>
              <Text>{attachment.filename}（{attachment.size} 字节）</Text>
              {role !== 'auditor' ? <Button type="button" appearance="subtle" disabled={uploading || submitting} onClick={() => void handleDownload(attachment.id)}>下载附件 {attachment.filename}</Button> : null}
            </div>
          ))}
        </fieldset>
        <fieldset className={styles.step} aria-label="第四步：确认">
          <Text weight="semibold">第四步：确认</Text>
          <Text>提交后将创建真实扫描任务；网络失败不会自动创建第二个任务。</Text>
          <div className={styles.actions}><Button type="button" appearance="secondary" onClick={() => navigate(returnTo ?? '/tasks')}>取消</Button><Button type="submit" appearance="primary" disabled={submitting || uploading || files.length > 0}>{submitting ? '正在提交' : '创建任务'}</Button></div>
        </fieldset>
      </form>
    </section>
  )
}
