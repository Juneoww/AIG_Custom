/**
 * 功能：从当前用户可见的 Agent 名称目录选择扫描目标。
 * 实现：名称白名单去重；刷新与失败期间暂停可用确认，成功刷新后清除失效选择。
 * 输入：选中名称及回调；输出：安全名称和可用状态；依赖：React Query、Fluent UI。
 */
import { Button, Field, MessageBar, MessageBarBody, Select, makeStyles, tokens } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'

import { fetchAgentNames } from '../../knowledge/api'
import { safeAgentReference } from '../agentWorkflow'
import type { GovernedModelAvailability } from './GovernedModelSelector'

const useStyles = makeStyles({ body: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalS, minWidth: 0 }, select: { minWidth: 0, width: '100%' } })

interface Props {
  value?: string
  onChange: (value: string | undefined) => void
  onAvailabilityChange?: (value: GovernedModelAvailability) => void
  disabled?: boolean
}

export function GovernedAgentSelector({ value, onChange, onAvailabilityChange, disabled }: Props) {
  const styles = useStyles()
  const catalog = useQuery({ queryKey: ['governed-agent-names'], queryFn: ({ signal }) => fetchAgentNames(signal), retry: false })
  const trusted = catalog.isSuccess && !catalog.isFetching
  const names = trusted ? [...new Set(catalog.data.filter((name) => safeAgentReference(name) !== undefined))] : []
  const selected = safeAgentReference(value)
  const available = selected !== undefined && names.includes(selected)
  const unavailable = trusted && value !== undefined && !available
  const availability: GovernedModelAvailability = available ? 'available' : !value || unavailable ? 'unavailable' : 'pending'
  const callbacks = useRef({ onChange, onAvailabilityChange })
  const cleared = useRef<string | undefined>(undefined)
  useEffect(() => { callbacks.current = { onChange, onAvailabilityChange } }, [onChange, onAvailabilityChange])
  useEffect(() => { callbacks.current.onAvailabilityChange?.(availability) }, [availability])
  useEffect(() => {
    if (unavailable && cleared.current !== value) { cleared.current = value; callbacks.current.onChange(undefined) }
    if (!unavailable) cleared.current = undefined
  }, [unavailable, value])

  return <Field label="Agent 配置" required>
    <div className={styles.body}>
      <Select className={styles.select} aria-label="Agent 配置" required value={selected ?? ''} disabled={disabled || catalog.isFetching} onChange={(_, data) => onChange(data.value || undefined)}>
        <option value="">请选择 Agent</option>
        {selected && !available && !unavailable ? <option value={selected} disabled>已选 Agent（待确认）</option> : null}
        {names.map((name) => <option key={name} value={name}>{name}</option>)}
      </Select>
      {catalog.isFetching ? <span role="status">正在加载 Agent 目录…</span> : null}
      {catalog.isError ? <MessageBar intent="error"><MessageBarBody>Agent 目录加载失败，请重试。</MessageBarBody></MessageBar> : null}
      {unavailable ? <MessageBar intent="warning"><MessageBarBody>已选 Agent 不可用，已清除选择。</MessageBarBody></MessageBar> : null}
      <Button type="button" appearance="subtle" disabled={disabled || catalog.isFetching} onClick={() => void catalog.refetch()}>{catalog.isError ? '重试 Agent 目录' : '刷新 Agent 目录'}</Button>
      {trusted && names.length === 0 ? <Link to="/knowledge/agents">前往 Agent 配置</Link> : null}
    </div>
  </Field>
}
