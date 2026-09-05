/**
 * 功能：创建固定静态模式的 Skills 任务，以单个 ZIP 和必选治理模型构成安全请求。
 * 实现：本地预检、附件先上传、目录可用性校验与同逻辑幂等重试；卸载时中止请求。
 * 输入：一个不超过 20 MiB 的 ZIP、模型 ID 与可选备注；输出：任务详情导航或安全错误提示。
 */
import { Button, Field, MessageBar, MessageBarBody, Text, Textarea } from '@fluentui/react-components'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'

import { ApiError } from '../../shared/api/errors'
import type { AttachmentView, TaskCreateRequest } from '../../shared/api/types'
import { StatePanel } from '../../shared/components/StatePanel'
import { useSession } from '../auth/session'
import { createTaskSubmission, type TaskSubmission } from './api'
import { preflightAttachments, uploadAttachment } from './attachments'
import { AIInfraWorkbenchHeader } from './components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from './components/AIInfraWorkbench.styles'
import { GovernedModelSelector, type GovernedModelAvailability } from './components/GovernedModelSelector'
import { TaskTypeSelector } from './components/TaskTypeSelector'

const MAX_SKILLS_ZIP_BYTES = 20 * 1024 * 1024
const MAX_REMARK_CODE_POINTS = 2_000

function validateSkillsFiles(files: readonly File[]): string | undefined {
  if (files.length !== 1) return '请选择恰好一个 ZIP 包。'
  if (!/\.zip$/i.test(files[0].name)) return 'Skills 扫描仅支持 ZIP 包。'
  if (files[0].size > MAX_SKILLS_ZIP_BYTES) return 'Skills ZIP 包不能超过 20 MiB。'
  try { preflightAttachments(files) } catch (error) { return error instanceof Error ? error.message : '附件校验失败。' }
  return undefined
}

export function SkillsTaskCreatePage({ returnTo = '/tasks/skills', onTaskTypeChange }: {
  returnTo?: string
  onTaskTypeChange?: (type: TaskCreateRequest['task_type']) => void
}) {
  const styles = useAIInfraWorkbenchStyles()
  const navigate = useNavigate()
  const { state } = useSession()
  const canWrite = state.status === 'authenticated' && (state.subject.role === 'user' || state.subject.role === 'admin')
  const [files, setFiles] = useState<File[]>([])
  const [attachment, setAttachment] = useState<AttachmentView>()
  const [modelID, setModelID] = useState<string>()
  const [availability, setAvailability] = useState<GovernedModelAvailability>('pending')
  const [remark, setRemark] = useState('')
  const [error, setError] = useState('')
  const [uploading, setUploading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)
  const submission = useRef<TaskSubmission | null>(null)
  const busy = useRef(false)
  const mounted = useRef(true)
  const controllerRef = useRef<AbortController | null>(null)
  const normalizedRemark = remark.trim()
  const remarkPoints = Array.from(normalizedRemark)
  const hasInvalidUnicode = remarkPoints.some((character) => character.length === 1 && /[\uD800-\uDFFF]/.test(character))
  const remarkError = hasInvalidUnicode ? '任务说明包含无效字符。' : remarkPoints.length > MAX_REMARK_CODE_POINTS ? '任务说明不能超过 2,000 个字符。' : undefined
  const canSubmit = canWrite && !uploading && !submitting && files.length === 0 && attachment?.state === 'ready' && Boolean(modelID) && availability === 'available' && !remarkError

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; controllerRef.current?.abort() }
  }, [])

  const upload = async () => {
    if (!canWrite || busy.current || files.length === 0) return
    const invalid = validateSkillsFiles(files)
    if (invalid) { setError(invalid); return }
    busy.current = true
    const controller = new AbortController()
    controllerRef.current = controller
    setError('')
    setUploading(true)
    try {
      const uploaded = await uploadAttachment(files[0], controller.signal)
      if (!mounted.current || controller.signal.aborted) return
      if (uploaded.state !== 'ready') { setError('附件尚未就绪，请重新上传。'); return }
      if (!/\.zip$/i.test(uploaded.filename) || uploaded.size <= 0 || uploaded.size > MAX_SKILLS_ZIP_BYTES) {
        setError('上传结果不符合 Skills ZIP 包要求，请重新上传。')
        return
      }
      setAttachment(uploaded)
      setFiles([])
      if (fileInput.current) fileInput.current.value = ''
      submission.current = null
    } catch {
      if (mounted.current && !controller.signal.aborted) setError('附件上传失败，请核对后显式重试。')
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
      busy.current = false
      if (mounted.current) setUploading(false)
    }
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!canSubmit || busy.current || !attachment || !modelID) return
    busy.current = true
    const controller = new AbortController()
    controllerRef.current = controller
    setError('')
    setSubmitting(true)
    const input: TaskCreateRequest = {
      task_type: 'skills_scan', content: '', params: { model_id: modelID },
      attachment_ids: [attachment.id], country_iso_code: 'zh_CN',
      ...(normalizedRemark ? { remark: normalizedRemark } : {}),
    }
    try {
      submission.current ??= createTaskSubmission(input)
      const created = await submission.current.submit(controller.signal)
      if (!mounted.current || controller.signal.aborted) return
      navigate(`/tasks/skills/${encodeURIComponent(created.id)}`, { replace: true })
    } catch (caught) {
      if (mounted.current && !controller.signal.aborted) {
        setError(caught instanceof ApiError && caught.kind === 'bad-request' ? caught.message : '任务创建未确认，显式重试将复用同一幂等键。')
      }
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
      busy.current = false
      if (mounted.current) setSubmitting(false)
    }
  }

  if (!canWrite) return <StatePanel state="forbidden" title="无权创建 Skills 扫描任务" />

  return (
    <section className={styles.page}>
      <AIInfraWorkbenchHeader title="新建 Skills 扫描任务" description="上传技能包并选择扫描模型，检查技能内容中的安全风险。" backLink={{ to: returnTo, label: '返回任务台账' }} />
      {onTaskTypeChange ? <TaskTypeSelector value="skills_scan" onChange={onTaskTypeChange} disabled={uploading || submitting} /> : null}
      {error ? <MessageBar intent="error"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
      <form className={styles.creationForm} onSubmit={submit}>
        <section className={styles.surface} aria-labelledby="skills-source-heading">
          <h2 id="skills-source-heading" className={`${styles.sectionHeader} ${styles.sectionHeading}`}><span className={styles.stepNumber}>1</span><span>扫描对象</span></h2>
          <div className={styles.sourceGrid}>
            <div className={styles.sourceCard}>
              <label className={styles.sourceTitle} htmlFor="skills-zip-file">Skills ZIP 包</label>
              <Text className={styles.sourceDescription}>选择一个 ZIP 文件，最大 20 MiB；先上传，再创建任务。</Text>
              <input id="skills-zip-file" ref={fileInput} type="file" accept=".zip,application/zip,application/x-zip-compressed" disabled={uploading || submitting} onChange={(event) => {
                setFiles(Array.from(event.currentTarget.files ?? [])); setAttachment(undefined); setError(''); submission.current = null
              }} />
              <div className={styles.attachmentControls}>
                <Button type="button" appearance="secondary" disabled={uploading || submitting || files.length === 0} onClick={() => void upload()}>{uploading ? '正在上传 Skills 包' : '上传 Skills 包'}</Button>
                {files.length > 0 ? <Text size={200}>已选择 {files.length} 个待上传文件</Text> : null}
              </div>
              {attachment ? <div className={styles.attachmentRow} aria-label="已上传 Skills 包">
                <Text>{attachment.filename}（{attachment.size} 字节）</Text>
                <Button type="button" appearance="subtle" disabled={uploading || submitting} onClick={() => { setAttachment(undefined); submission.current = null }}>移除 Skills 包</Button>
              </div> : null}
            </div>
            <aside className={styles.sourceCard} aria-label="Skills 包说明">
              <Text className={styles.sourceTitle}>技能包内容</Text>
              <Text className={styles.sourceDescription}>包内应包含 SKILL.md，可同时包含 references、scripts 等技能资源。</Text>
              <Text className={styles.sourceDescription}>仅含 SKILL.md 的技能包也可扫描，无需 scripts 目录。</Text>
              <Text className={styles.sourceDescription}>本次扫描静态检查文件内容，不运行技能包中的脚本。</Text>
            </aside>
          </div>
          <Field className={styles.noteField} label="任务说明 / 备注（可选）" validationState={remarkError ? 'error' : 'none'} validationMessage={remarkError}>
            <Textarea className={styles.largeTextarea} value={remark} disabled={uploading || submitting} resize="vertical" aria-describedby="skills-remark-meta" onChange={(_, data) => { setRemark(data.value); submission.current = null }} />
          </Field>
          <div id="skills-remark-meta" className={styles.noteMeta}><Text size={200}>任务说明会随任务保留。</Text><Text size={200} role="status" aria-live="polite">{Array.from(remark).length.toLocaleString('zh-CN')} / 2,000</Text></div>
        </section>
        <section className={styles.surface} aria-labelledby="skills-config-heading">
          <h2 id="skills-config-heading" className={`${styles.sectionHeader} ${styles.sectionHeading}`}><span className={styles.stepNumber}>2</span><span>扫描配置</span></h2>
          <div className={styles.configurationGrid}>
            <GovernedModelSelector required value={modelID} disabled={uploading || submitting} onChange={(id) => { setModelID(id); submission.current = null }} onAvailabilityChange={setAvailability} />
            <div className={styles.sourceCard}><Text className={styles.sourceTitle}>静态扫描</Text><Text className={styles.sourceDescription}>使用所选模型审计技能文件，扫描语言为中文。</Text></div>
          </div>
        </section>
        <section className={styles.surface} aria-labelledby="skills-submit-heading">
          <h2 id="skills-submit-heading" className={`${styles.sectionHeader} ${styles.sectionHeading}`}><span className={styles.stepNumber}>3</span><span>确认并提交</span></h2>
          <div className={styles.confirmation}>
            <Text className={styles.confirmationCopy}>提交后创建真实扫描任务。网络中断后可显式重试，同一份提交会复用幂等键。</Text>
            <div className={styles.submitActions}><Button type="button" appearance="secondary" disabled={uploading || submitting} onClick={() => navigate(returnTo)}>取消</Button><Button className={styles.submitPrimary} type="submit" appearance="primary" disabled={!canSubmit}>{submitting ? '正在提交' : '创建 Skills 扫描任务'}</Button></div>
          </div>
        </section>
      </form>
    </section>
  )
}
