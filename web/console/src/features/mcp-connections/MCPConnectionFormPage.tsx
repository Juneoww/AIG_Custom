/**
 * 功能：创建、编辑、测试并明确启用 MCP 连接配置。
 * 实现：管理详情与秘密输入隔离；省略值表示保留；保存后清空秘密并重新获取安全详情。
 * 输入：已保存安全详情与临时表单；输出：携带 If-Match、CSRF 和幂等键的专属写请求。
 */
import { Button, Field, Input, MessageBar, MessageBarBody, Select, Text, Textarea } from '@fluentui/react-components'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { ApiError } from '../../shared/api/errors'
import { StatePanel } from '../../shared/components/StatePanel'
import { useSession } from '../auth/session'
import { AIInfraWorkbenchHeader } from '../tasks/components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from '../tasks/components/AIInfraWorkbench.styles'
import { createConnectionMutation, fetchConnection, isMCPUnknownOutcome, mcpErrorMessage, type AuthenticationKind, type ConnectionDetail, type ConnectionResult, type ConnectionWrite, type MCPMutation, type Transport } from './api'

export function MCPConnectionFormPage() {
  const { connectionConfigId } = useParams()
  const { state } = useSession()
  const canManage = state.status === 'authenticated' && state.subject.role !== 'auditor'
  const query = useQuery({ queryKey: ['mcp-connection', connectionConfigId], queryFn: ({ signal }) => fetchConnection(connectionConfigId!, signal), enabled: canManage && Boolean(connectionConfigId), retry: false, refetchOnWindowFocus: false })
  if (!canManage) return <StatePanel state="forbidden" title="无权管理 MCP 连接配置" />
  if (connectionConfigId && query.isPending) return <StatePanel state="loading" title="正在加载 MCP 连接详情" />
  if (connectionConfigId && query.isError) return <StatePanel state={query.error instanceof ApiError && query.error.status === 403 ? 'forbidden' : 'error'} title="无法读取 MCP 连接详情" actionLabel="重新加载" onAction={() => void query.refetch()} />
  return <ConnectionEditor key={`${connectionConfigId ?? 'new'}:${query.data?.resource_revision ?? ''}`} detail={query.data} reload={() => query.refetch()} />
}

function ConnectionEditor({ detail, reload }: { detail?: ConnectionDetail; reload: () => Promise<unknown> }) {
  const viewStyles = useAIInfraWorkbenchStyles()
  const navigate = useNavigate()
  const client = useQueryClient()
  const [editing, setEditing] = useState(!detail)
  const [name, setName] = useState(detail?.name ?? '')
  const [description, setDescription] = useState(detail?.description ?? '')
  const [serverURL, setServerURL] = useState(detail?.server_url ?? '')
  const [transport, setTransport] = useState<Transport>(detail?.transport ?? 'auto')
  const [kind, setKind] = useState<AuthenticationKind>(detail?.authentication_kind === 'unknown' ? 'none' : detail?.authentication_kind ?? 'none')
  const [headerName, setHeaderName] = useState(detail?.authentication_header_name ?? '')
  const [secret, setSecret] = useState('')
  const [headers, setHeaders] = useState(() => detail?.headers.map((item) => ({ ...item, value: '' })) ?? [])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [unknown, setUnknown] = useState(false)
  const operation = useRef<MCPMutation<ConnectionResult> | null>(null)
  const controller = useRef<AbortController | null>(null)
  const mounted = useRef(true)
  const locked = useRef(false)
  const action = useRef<'save' | 'test' | 'enable' | 'disable'>('save')
  const canRetainSecret = Boolean(detail?.authentication_configured && detail.authentication_kind === kind && (kind !== 'api_key_header' || detail.authentication_header_name === headerName))
  const headersValid = headers.length <= 10 && headers.every((item) => /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(item.name) && (item.configured || Boolean(item.value))) && new Set(headers.map((item) => item.name.toLowerCase())).size === headers.length
  const authenticationValid = headersValid && (kind === 'none' ? headers.length === 0 : kind === 'custom_headers' ? headers.length > 0 : (Boolean(secret.trim()) || canRetainSecret) && (kind !== 'api_key_header' || /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(headerName)))
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; controller.current?.abort(); operation.current?.clear(); operation.current = null } }, [])
  const changed = () => { operation.current?.clear(); operation.current = null; setUnknown(false); setError(''); setNotice('') }
  const clearSecrets = () => { setSecret(''); setHeaders((current) => current.map((item) => ({ ...item, value: '' }))) }
  const toggleEditing = () => {
    changed(); clearSecrets()
    if (editing && detail) {
      setName(detail.name); setDescription(detail.description); setServerURL(detail.server_url); setTransport(detail.transport)
      setKind(detail.authentication_kind === 'unknown' ? 'none' : detail.authentication_kind); setHeaderName(detail.authentication_header_name ?? '')
      setHeaders(detail.headers.map((item) => ({ ...item, value: '' })))
    }
    setEditing(!editing)
  }
  const saveInput = (): ConnectionWrite => {
    let parsed: URL
    try { parsed = new URL(serverURL.trim()) } catch { throw new ApiError('bad-request', 0) }
    if (!name.trim() || Array.from(name.trim()).length > 80 || Array.from(description).length > 500 || !['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password || parsed.hash) throw new ApiError('bad-request', 0)
    if (!authenticationValid) throw new ApiError('bad-request', 0)
    const input: ConnectionWrite = { name: name.trim(), description }
    if (!detail || detail.server_url !== serverURL.trim()) input.server_url = serverURL.trim()
    if (!detail || detail.transport !== transport) input.transport = transport
    if (!detail || detail.authentication_kind !== kind || detail.authentication_header_name !== (headerName || undefined) || secret) input.authentication = { kind, ...(kind === 'api_key_header' ? { header_name: headerName } : {}), ...(['bearer', 'api_key_header'].includes(kind) && secret ? { secret } : {}) }
    if (!detail || JSON.stringify(headers.map(({ name, configured }) => ({ name, configured }))) !== JSON.stringify(detail.headers) || headers.some((item) => item.value)) input.headers = headers.map((item) => ({ name: item.name, ...(item.value ? { value: item.value } : {}) }))
    return input
  }
  const execute = async (nextAction: typeof action.current, retry = false) => {
    if (locked.current) return
    locked.current = true; setBusy(true); setError(''); setNotice('')
    const request = new AbortController(); controller.current = request
    try {
      if (!retry) {
        operation.current?.clear(); action.current = nextAction
        const input = nextAction === 'save' ? saveInput() : nextAction === 'test' ? {} : { enabled: nextAction === 'enable' }
        operation.current = createConnectionMutation(nextAction === 'test' ? 'TEST' : detail ? 'PATCH' : 'POST', detail?.id, input, detail?.resource_revision)
      }
      const result = await operation.current!.submit(request.signal)
      if (!mounted.current || request.signal.aborted) return
      operation.current?.clear(); operation.current = null; clearSecrets(); setUnknown(false)
      setNotice(nextAction === 'test' ? result.status === 'passed' ? '连接测试通过，请明确启用后用于扫描。' : '连接测试失败，请检查配置后重新测试。' : nextAction === 'save' ? '连接配置已保存。连接材料修改后需重新测试并启用。' : nextAction === 'enable' ? '连接已启用。' : '连接已停用。')
      await client.invalidateQueries({ queryKey: ['mcp-connections'] })
      await client.invalidateQueries({ queryKey: ['mcp-connection-options'] })
      if (!detail) { navigate(`/credentials/mcp-connections/${encodeURIComponent(result.id)}`, { replace: true }); return }
      setEditing(false); await reload()
    } catch (caught) {
      if (!mounted.current || request.signal.aborted) return
      setUnknown(isMCPUnknownOutcome(caught)); setError(mcpErrorMessage(caught))
      if (!isMCPUnknownOutcome(caught)) { operation.current?.clear(); operation.current = null; clearSecrets() }
    } finally { locked.current = false; if (mounted.current) setBusy(false) }
  }
  const submit = (event: FormEvent) => { event.preventDefault(); void execute('save') }
  return <section className={viewStyles.page}>
    <AIInfraWorkbenchHeader title={detail ? 'MCP 连接详情' : '新建 MCP 连接'} description="连接材料仅用于受控服务扫描，密钥不会回显。" backLink={{ to: '/credentials/mcp-connections', label: '返回 MCP 连接配置' }} />
    {error ? <MessageBar intent="error"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
    {notice ? <MessageBar intent="success"><MessageBarBody>{notice}</MessageBarBody></MessageBar> : null}
    {detail?.authentication_kind === 'unknown' ? <MessageBar intent="warning"><MessageBarBody>认证状态不可用，请检查配置后重新保存并测试。</MessageBarBody></MessageBar> : null}
    {unknown ? <Button disabled={busy} onClick={() => void execute(action.current, true)}>重试同一操作</Button> : null}
    {detail ? <section className={viewStyles.surface}><div className={viewStyles.attachmentControls}><Text>版本 {detail.current_version}</Text><Text>{detail.enabled ? '已启用' : '未启用'}</Text><Text>{detail.probe_status === 'passed' ? '测试通过' : detail.probe_status === 'failed' ? '测试失败' : '待测试'}</Text></div><div className={viewStyles.attachmentControls}>
      <Button disabled={busy || unknown} onClick={toggleEditing}>{editing ? '取消编辑' : '编辑配置'}</Button>
      <Button disabled={busy || editing || unknown} onClick={() => void execute('test')}>测试连接</Button>
      <Button disabled={busy || editing || unknown || (!detail.enabled && detail.probe_status !== 'passed')} onClick={() => void execute(detail.enabled ? 'disable' : 'enable')}>{detail.enabled ? '停用连接' : '启用连接'}</Button>
      <Button disabled={busy} onClick={() => { changed(); clearSecrets(); void reload() }}>刷新配置</Button>
    </div></section> : null}
    <form onSubmit={submit} className={viewStyles.creationForm} autoComplete="off">
      <section className={viewStyles.surface}><h2 className={viewStyles.sectionHeading}>基本信息</h2><div className={viewStyles.configurationGrid}>
        <Field label="配置名称" required><Input value={name} maxLength={80} disabled={!editing || busy} onChange={(_, data) => { changed(); setName(data.value) }} /></Field>
        <Field label="传输协议"><Select value={transport} disabled={!editing || busy} onChange={(_, data) => { changed(); setTransport(data.value as Transport) }}><option value="auto">自动识别</option><option value="http">Streamable HTTP</option><option value="sse">SSE</option></Select></Field>
        <Field className={viewStyles.fullWidth} label="配置说明"><Textarea value={description} maxLength={500} disabled={!editing || busy} onChange={(_, data) => { changed(); setDescription(data.value) }} /></Field>
        <Field className={viewStyles.fullWidth} label="服务地址" required><Input type="url" value={serverURL} disabled={!editing || busy} placeholder="https://mcp.example.com/mcp" onChange={(_, data) => { changed(); setServerURL(data.value) }} /></Field>
      </div></section>
      <section className={viewStyles.surface}><h2 className={viewStyles.sectionHeading}>认证与请求头</h2><div className={viewStyles.configurationGrid}>
        <Field label="认证方式"><Select value={kind} disabled={!editing || busy} onChange={(_, data) => { changed(); setKind(data.value as AuthenticationKind); setSecret(''); setHeaderName(''); if (data.value === 'none') setHeaders([]) }}><option value="none">无需认证</option><option value="bearer">Bearer Token</option><option value="api_key_header">API Key 请求头</option><option value="custom_headers">自定义请求头</option></Select></Field>
        {kind === 'api_key_header' ? <Field label="认证请求头名称" required><Input value={headerName} disabled={!editing || busy} onChange={(_, data) => { changed(); setHeaderName(data.value) }} /></Field> : null}
        {kind === 'bearer' || kind === 'api_key_header' ? <Field label="认证密钥" hint={canRetainSecret ? '已配置；留空保留，输入新值替换。' : '请输入新密钥；认证方式或请求头名称变化时不能保留原值。'}><Input type="password" autoComplete="new-password" value={secret} disabled={!editing || busy} placeholder={canRetainSecret ? '已配置 / 保留' : '输入认证密钥'} onChange={(_, data) => { changed(); setSecret(data.value) }} /></Field> : null}
      </div>
      {kind === 'custom_headers' && headers.length === 0 ? <Text>请至少添加一个包含名称和值的请求头。</Text> : null}
      {kind !== 'none' ? <div className={viewStyles.attachmentList}>{headers.map((item, index) => <div className={viewStyles.sourceCard} key={index}><Field label={`请求头 ${index + 1} 名称`}><Input value={item.name} disabled={!editing || busy} onChange={(_, data) => { changed(); setHeaders((current) => current.map((entry, i) => i === index ? { ...entry, name: data.value, configured: false } : entry)) }} /></Field><Field label={`请求头 ${index + 1} 值`} hint={item.configured ? '已配置；留空保留，输入新值替换。' : '输入后仅写入保存。'}><Input type="password" autoComplete="new-password" value={item.value} disabled={!editing || busy} onChange={(_, data) => { changed(); setHeaders((current) => current.map((entry, i) => i === index ? { ...entry, value: data.value } : entry)) }} /></Field><Button type="button" disabled={!editing || busy} onClick={() => { changed(); setHeaders((current) => current.filter((_, i) => i !== index)) }}>移除请求头 {index + 1}</Button></div>)}</div> : null}
      {editing && kind !== 'none' ? <Button type="button" disabled={busy || headers.length >= 10} onClick={() => { changed(); setHeaders((current) => [...current, { name: '', value: '', configured: false }]) }}>添加请求头（{headers.length}/10）</Button> : null}
      </section>
      {editing ? <div className={viewStyles.submitActions}><Button type="submit" appearance="primary" disabled={busy || unknown || !authenticationValid}>{busy ? '正在保存' : '保存连接配置'}</Button></div> : null}
    </form>
  </section>
}
