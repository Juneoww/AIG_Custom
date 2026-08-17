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
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'

import { useSession } from '../auth/session'
import type { AttachmentView, TaskCreateRequest } from '../../shared/api/types'
import { PageHeader } from '../../shared/components/PageHeader'
import { downloadAttachment, preflightAttachments, uploadAttachment } from './attachments'
import { createTaskSubmission, type TaskSubmission } from './api'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  form: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  step: { margin: 0, padding: tokens.spacingVerticalL, border: `1px solid ${tokens.colorNeutralStroke1}`, borderRadius: tokens.borderRadiusMedium, backgroundColor: tokens.colorNeutralBackground1, display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalM },
  fields: { display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: tokens.spacingHorizontalL },
  actions: { display: 'flex', justifyContent: 'flex-end', gap: tokens.spacingHorizontalM },
  attachment: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: tokens.spacingHorizontalM },
})

export function TaskCreatePage() {
  const styles = useStyles()
  const navigate = useNavigate()
  const { state } = useSession()
  const role = state.status === 'authenticated' ? state.subject.role : 'auditor'
  const [taskType, setTaskType] = useState<TaskCreateRequest['task_type']>('mcp_scan')
  const [content, setContent] = useState('')
  const [language, setLanguage] = useState<'zh_CN' | 'en'>('zh_CN')
  const [modelID, setModelID] = useState('')
  const [thread, setThread] = useState('4')
  const [timeout, setTimeoutValue] = useState('300')
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
    if (mutexRef.current) return
    mutexRef.current = true
    setSubmitting(true)
    setError('')
    const controller = new AbortController()
    controllerRef.current?.abort()
    controllerRef.current = controller
    try {
      if (!content.trim()) throw new Error('请填写扫描目标或任务说明。')
      const params: TaskCreateRequest['params'] = {}
      if (modelID.trim()) params.model_id = modelID.trim()
      if (taskType === 'mcp_scan') {
        const parsed = Number(thread)
        if (!Number.isInteger(parsed) || parsed < 1 || parsed > 1_024) throw new Error('请填写有效的并发数。')
        params.thread = parsed
      }
      if (taskType === 'ai_infra_scan') {
        const parsed = Number(timeout)
        if (!Number.isInteger(parsed) || parsed < 1 || parsed > 86_400) throw new Error('请填写有效的超时秒数。')
        params.timeout = parsed
      }
      if (taskType === 'model_redteam_report') {
        const parsed = Number(numPrompts)
        if (!Number.isInteger(parsed) || parsed < 1 || parsed > 1_000_000) throw new Error('请填写有效的提示词数量。')
        params.dataset = { numPrompts: parsed }
      }
      const input: TaskCreateRequest = {
        task_type: taskType,
        content: content.trim(),
        params,
        attachment_ids: attachments.map((item) => item.id),
        country_iso_code: language,
      }
      submissionRef.current ??= createTaskSubmission(input)
      const created = await submissionRef.current.submit(controller.signal)
      navigate(`/tasks/${encodeURIComponent(created.id)}`, { replace: true })
    } catch (caught) {
      if (mountedRef.current && !controller.signal.aborted) {
        setError(caught instanceof Error && caught.message.startsWith('请填写') ? caught.message : '任务创建未确认，显式重试将复用同一幂等键。')
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

  return (
    <section className={styles.page}>
      <PageHeader title="创建扫描任务" description="按类型、参数、附件和确认顺序提交；浏览器不接收模型密钥。" />
      {error ? <MessageBar intent="error"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
      <form className={styles.form} onSubmit={submit}>
        <fieldset className={styles.step} aria-label="第一步：任务类型">
          <Text weight="semibold">第一步：任务类型</Text>
          <Field label="扫描类型">
            <Select value={taskType} onChange={(_, data) => { setTaskType(data.value as TaskCreateRequest['task_type']); invalidateSubmission() }}>
              <option value="mcp_scan">MCP 扫描</option>
              <option value="ai_infra_scan">AI 基础设施扫描</option>
              <option value="model_redteam_report">模型红队评测</option>
              <option value="agent_scan">Agent 扫描</option>
            </Select>
          </Field>
        </fieldset>
        <fieldset className={styles.step} aria-label="第二步：参数">
          <Text weight="semibold">第二步：参数</Text>
          <Field label="扫描目标或任务说明" required>
            <Textarea value={content} onChange={(_, data) => { setContent(data.value); invalidateSubmission() }} resize="vertical" />
          </Field>
          <div className={styles.fields}>
            <Field label="语言">
              <Select value={language} onChange={(_, data) => { setLanguage(data.value as 'zh_CN' | 'en'); invalidateSubmission() }}>
                <option value="zh_CN">中文</option><option value="en">英文</option>
              </Select>
            </Field>
            <Field label="模型 ID" hint="仅填写平台模型 ID，不填写密钥。">
              <Input value={modelID} onChange={(_, data) => { setModelID(data.value); invalidateSubmission() }} autoComplete="off" />
            </Field>
            {taskType === 'mcp_scan' ? <Field label="并发数"><Input type="number" min={1} max={1024} value={thread} onChange={(_, data) => { setThread(data.value); invalidateSubmission() }} /></Field> : null}
            {taskType === 'ai_infra_scan' ? <Field label="超时秒数"><Input type="number" min={1} max={86400} value={timeout} onChange={(_, data) => { setTimeoutValue(data.value); invalidateSubmission() }} /></Field> : null}
            {taskType === 'model_redteam_report' ? <Field label="提示词数量"><Input type="number" min={1} max={1000000} value={numPrompts} onChange={(_, data) => { setNumPrompts(data.value); invalidateSubmission() }} /></Field> : null}
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
          <div className={styles.actions}><Button type="button" appearance="secondary" onClick={() => navigate('/tasks')}>取消</Button><Button type="submit" appearance="primary" disabled={submitting || uploading || files.length > 0}>{submitting ? '正在提交' : '创建任务'}</Button></div>
        </fieldset>
      </form>
    </section>
  )
}
