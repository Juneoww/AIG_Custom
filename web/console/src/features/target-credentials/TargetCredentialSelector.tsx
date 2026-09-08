/** 功能：为基础设施任务选择目标凭据；实现：仅查询启用的安全元数据，版本变化时要求重新选择；输入/输出：凭据引用，不含认证密钥。 */
import { Button, Field, Select, Text } from '@fluentui/react-components'
import { useQuery } from '@tanstack/react-query'
import { useEffect } from 'react'
import { Link } from 'react-router-dom'
import { fetchCredentials, type TargetCredential } from './api'

export function TargetCredentialSelector({ value, onChange, onAvailabilityChange, disabled }: { value?: TargetCredential; onChange: (value?: TargetCredential) => void; onAvailabilityChange?: (available: boolean) => void; disabled?: boolean }) {
  const query = useQuery({ queryKey: ['target-credentials'], queryFn: ({ signal }) => fetchCredentials(signal), retry: false })
  const available = query.data?.filter((item) => !item.disabled) ?? []
  const selectedMatches = Boolean(value && available.some((item) => item.id === value.id && item.revision === value.revision))
  const selectedAvailable = !value || query.isSuccess && !query.isFetching && selectedMatches
  const invalidSelection = Boolean(value && query.isSuccess && !query.isFetching && !selectedMatches)
  const retainedValue = value && !selectedMatches ? `unavailable:${value.id}:${value.revision}` : undefined
  useEffect(() => { onAvailabilityChange?.(selectedAvailable) }, [selectedAvailable, onAvailabilityChange])
  return <Field label="目标访问凭据（可选）" validationState={invalidSelection ? 'error' : undefined} validationMessage={invalidSelection ? '已选凭据已变更、停用或删除，请重新选择；如需匿名扫描，请明确选择“不使用目标凭据”。' : undefined} hint={value ? `允许访问：${value.origin}。请手动填写此地址下的 ${value.allow_insecure_http ? 'HTTP' : 'HTTPS'} URL；协议、主机和端口必须一致，不支持导入目标清单或端口发现。认证扫描使用 HTTP 证据，不生成网页截图。` : '需要登录或 API Key 的目标可选择访问凭据。'}>
    <Select value={retainedValue ?? value?.id ?? ''} disabled={disabled || query.isPending} onChange={(_, data) => {
      if (data.value === '') onChange(undefined)
      else {
        const selected = available.find((item) => item.id === data.value)
        if (selected) onChange(selected)
      }
    }}>
      <option value="">不使用目标凭据</option>
      {retainedValue && value ? <option value={retainedValue} disabled>{value.name} · 已选版本待确认</option> : null}
      {available.map((item) => <option key={item.id} value={item.id}>{item.name} · {item.origin}</option>)}
    </Select>
    {query.isPending ? <Text size={200}>正在加载凭据</Text> : null}
    {value && query.isFetching ? <Text size={200}>正在确认已选凭据，请稍候。</Text> : null}
    {query.isError ? <Text size={200}>凭据目录加载失败。<Button size="small" onClick={() => void query.refetch()}>重新加载凭据</Button></Text> : null}
    <Link to="/credentials/target-credentials">管理基础设施凭据</Link>
  </Field>
}
