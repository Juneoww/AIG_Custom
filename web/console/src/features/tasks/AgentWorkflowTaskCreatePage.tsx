/**
 * 功能：配置并创建 Agent 工作流动态扫描任务。
 * 实现：三段表单选择治理引用、分离执行说明与备注，并复用逻辑提交的幂等键。
 * 输入：Agent、扫描模型和说明；输出：平台任务与专属详情导航。
 * 依赖：Fluent UI、会话、任务 API；取消或卸载时中止请求并忽略迟到响应。
 */
import { Button, Field, MessageBar, MessageBarBody, Select, Text, Textarea } from '@fluentui/react-components'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'

import { useSession } from '../auth/session'
import { ApiError } from '../../shared/api/errors'
import { createTaskSubmission, type TaskSubmission } from './api'
import { safeAgentReference, safeEvaluationModelReference } from './agentWorkflow'
import { AIInfraWorkbenchHeader } from './components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from './components/AIInfraWorkbench.styles'
import { GovernedAgentSelector } from './components/GovernedAgentSelector'
import { GovernedModelSelector, type GovernedModelAvailability } from './components/GovernedModelSelector'

const BASE_PATH = '/tasks/agent-workflow'

function wellFormed(value: string): boolean {
  for (let i = 0; i < value.length; i += 1) {
    const code = value.charCodeAt(i)
    if (code >= 0xD800 && code <= 0xDBFF) {
      const next = value.charCodeAt(++i)
      if (!(next >= 0xDC00 && next <= 0xDFFF)) return false
    } else if (code >= 0xDC00 && code <= 0xDFFF) return false
  }
  return true
}

export function AgentWorkflowTaskCreatePage() {
  const styles = useAIInfraWorkbenchStyles()
  const navigate = useNavigate()
  const { state } = useSession()
  const canCreate = state.status === 'authenticated' && state.subject.role !== 'auditor'
  const [agentID, setAgentID] = useState<string>()
  const [modelID, setModelID] = useState<string>()
  const [agentAvailability, setAgentAvailability] = useState<GovernedModelAvailability>('unavailable')
  const [modelAvailability, setModelAvailability] = useState<GovernedModelAvailability>('unavailable')
  const [content, setContent] = useState('')
  const [remark, setRemark] = useState('')
  const [language, setLanguage] = useState<'zh_CN' | 'en'>('zh_CN')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const submission = useRef<TaskSubmission | null>(null)
  const controller = useRef<AbortController | null>(null)
  const mounted = useRef(true)
  const busy = useRef(false)

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; controller.current?.abort() }
  }, [])

  const invalidate = () => { submission.current = null; setError('') }
  const cancel = () => { controller.current?.abort(); navigate(BASE_PATH) }
  const ready = canCreate && Boolean(safeAgentReference(agentID)) && Boolean(safeEvaluationModelReference(modelID))
    && agentAvailability === 'available' && modelAvailability === 'available' && content.trim().length > 0

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (busy.current || !ready) return
    const instructions = content.trim()
    const note = remark.trim()
    if (!wellFormed(instructions) || new TextEncoder().encode(instructions).length > 32_768) {
      setError('执行说明必须为有效文本，且不能超过 32 KiB。'); return
    }
    if (!wellFormed(note) || [...note].length > 2_000) {
      setError('任务备注必须为有效文本，且不能超过 2,000 个字符。'); return
    }
    busy.current = true
    setSubmitting(true)
    setError('')
    const current = new AbortController()
    controller.current = current
    try {
      submission.current ??= createTaskSubmission({
        task_type: 'agent_scan', content: instructions, ...(note ? { remark: note } : {}),
        country_iso_code: language, params: { agent_id: agentID, eval_model_id: modelID }, attachment_ids: [],
      })
      const created = await submission.current.submit(current.signal)
      if (!mounted.current || current.signal.aborted || controller.current !== current) return
      if (created.task_type !== 'agent_scan') throw new ApiError('unexpected-response', 200)
      navigate(`${BASE_PATH}/${encodeURIComponent(created.id)}`, { replace: true })
    } catch (caught) {
      if (mounted.current && !current.signal.aborted && controller.current === current) {
        setError(caught instanceof ApiError && caught.kind === 'bad-request'
          ? '配置或输入不符合扫描要求，请核对 Agent 配置、模型和执行说明。'
          : '创建结果尚未确认。重试会复用本次提交，避免重复创建。')
      }
    } finally {
      if (controller.current === current) { controller.current = null; busy.current = false; if (mounted.current) setSubmitting(false) }
    }
  }

  return <section className={styles.page}>
    <AIInfraWorkbenchHeader title="新建 Agent 工作流扫描" description="选择 Agent，说明本次检查范围，提交动态安全扫描。" backLink={{ to: BASE_PATH, label: '返回 Agent 工作流扫描' }} />
    {!canCreate ? <MessageBar><MessageBarBody>当前角色可查看扫描任务，无法创建任务。</MessageBarBody></MessageBar> : <>
      {error ? <MessageBar intent="error"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
      <form className={styles.creationForm} onSubmit={submit} aria-label="Agent 扫描配置">
        <section className={styles.surface} aria-labelledby="agent-source-heading">
          <h2 id="agent-source-heading" className={`${styles.sectionHeader} ${styles.sectionHeading}`}><span className={styles.stepNumber}>1</span>扫描对象与范围</h2>
          <div className={styles.sourceGrid}>
            <div className={styles.sourceCard}>
              <GovernedAgentSelector value={agentID} disabled={submitting} onChange={(id) => { setAgentID(id); invalidate() }} onAvailabilityChange={setAgentAvailability} />
              <Text className={styles.sourceDescription}>从已配置的 Agent 中选择。本次支持 HTTP、WebSocket 和 Dify 接口。</Text>
            </div>
            <div className={styles.sourceCard}>
              <Field label="执行说明" required hint="说明业务场景、检查重点与允许测试的范围。">
                <Textarea className={styles.largeTextarea} value={content} disabled={submitting} onChange={(_, data) => { setContent(data.value); invalidate() }} resize="vertical" required />
              </Field>
              <Text size={200}>执行说明会用于扫描，最多 32 KiB。</Text>
            </div>
          </div>
          <Field className={styles.noteField} label="任务备注（可选）" hint="用于任务记录，不作为扫描指令。">
            <Textarea value={remark} disabled={submitting} onChange={(_, data) => { setRemark(data.value); invalidate() }} resize="vertical" />
          </Field>
          <Text className={styles.noteMeta}>{[...remark].length.toLocaleString('zh-CN')} / 2,000 字符</Text>
        </section>
        <section className={styles.surface} aria-labelledby="agent-model-heading">
          <h2 id="agent-model-heading" className={`${styles.sectionHeader} ${styles.sectionHeading}`}><span className={styles.stepNumber}>2</span>扫描配置</h2>
          <div className={styles.configurationGrid}>
            <GovernedModelSelector value={modelID} onChange={(id) => { setModelID(id); invalidate() }} onAvailabilityChange={setModelAvailability} disabled={submitting} required label="扫描 / 裁判模型" />
            <Field label="报告语言"><Select value={language} disabled={submitting} onChange={(_, data) => { setLanguage(data.value as 'zh_CN' | 'en'); invalidate() }}><option value="zh_CN">中文</option><option value="en">英文</option></Select></Field>
          </div>
          <Text className={styles.noteMeta}>选中的模型用于信息搜集、漏洞挖掘与复核三个阶段。</Text>
        </section>
        <section className={styles.surface} aria-labelledby="agent-confirm-heading">
          <h2 id="agent-confirm-heading" className={`${styles.sectionHeader} ${styles.sectionHeading}`}><span className={styles.stepNumber}>3</span>确认扫描</h2>
          <div className={styles.confirmation}><Text>Agent：{agentID ?? '尚未选择'}</Text><Text className={styles.confirmationCopy}>扫描会向选中的 Agent 发送测试输入。请确认执行说明覆盖本次授权范围；完成后可在任务详情查看报告。</Text></div>
          <div className={styles.submitActions}><Button type="button" onClick={cancel}>取消</Button><Button className={styles.submitPrimary} appearance="primary" type="submit" disabled={!ready || submitting}>{submitting ? '正在提交…' : '创建扫描任务'}</Button></div>
        </section>
      </form>
    </>}
  </section>
}
