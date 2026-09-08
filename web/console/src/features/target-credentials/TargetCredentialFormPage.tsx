/**
 * 功能：创建、编辑、停用和删除私有目标凭据。
 * 实现：密钥仅存在于当前表单和显式写请求，空值保留；版本冲突要求刷新。
 * 输入：安全元数据与临时认证值；输出：受 CSRF 和 If-Match 保护的请求。
 * 依赖：Fluent UI、查询缓存与站内路由；缓存只接收安全 DTO。
 */
import { Button, Checkbox, Field, Input, MessageBar, MessageBarBody, Select, Text } from '@fluentui/react-components'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { StatePanel } from '../../shared/components/StatePanel'
import { AIInfraWorkbenchHeader } from '../tasks/components/AIInfraWorkbenchHeader'
import { useAIInfraWorkbenchStyles } from '../tasks/components/AIInfraWorkbench.styles'
import { authLabels, credentialError, deleteCredential, fetchCredential, normalizeCredentialOrigin, saveCredential, type TargetAuthType, type TargetCredential } from './api'

export function TargetCredentialFormPage() {
  const { credentialId } = useParams()
  const query = useQuery({ queryKey: ['target-credential', credentialId], queryFn: ({ signal }) => fetchCredential(credentialId!, signal), enabled: Boolean(credentialId), retry: false, refetchOnWindowFocus: false })
  if (credentialId && query.isPending) return <StatePanel state="loading" title="正在加载基础设施凭据" />
  if (credentialId && query.isError) return <StatePanel state="error" title="无法读取基础设施凭据" actionLabel="重新加载" onAction={() => void query.refetch()} />
  return <CredentialEditor key={`${credentialId ?? 'new'}:${query.data?.revision ?? ''}`} detail={query.data} reload={() => void query.refetch()} />
}

export function CredentialEditor({ detail, reload }: { detail?: TargetCredential; reload: () => void }) {
  const styles = useAIInfraWorkbenchStyles()
  const client = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState(detail?.name ?? '')
  const [origin, setOrigin] = useState(detail?.origin ?? '')
  const [authType, setAuthType] = useState<TargetAuthType>(detail?.auth_type ?? 'bearer')
  const [headerName, setHeaderName] = useState(detail?.header_name ?? '')
  const [secret, setSecret] = useState('')
  const [username, setUsername] = useState('')
  const [disabled, setDisabled] = useState(detail?.disabled ?? false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmDelete, setConfirmDelete] = useState(false)
  const operation = useRef<AbortController | null>(null)
  useEffect(() => () => operation.current?.abort(), [])
  const normalizedOrigin = normalizeCredentialOrigin(origin)
  const canRetain = Boolean(detail && normalizedOrigin && normalizedOrigin === normalizeCredentialOrigin(detail.origin) && authType === detail.auth_type && headerName.toLowerCase() === detail.header_name.toLowerCase())
  const clearSecrets = () => { setSecret(''); setUsername('') }
  const finish = async () => {
    clearSecrets()
    await client.invalidateQueries({ queryKey: ['target-credentials'] })
    client.removeQueries({ queryKey: ['target-credential', detail?.id], exact: true })
    navigate('/credentials/target-credentials')
  }
  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (operation.current) return
    if (!normalizedOrigin) { setError('请填写有效的 HTTP 或 HTTPS 目标地址，不包含路径、查询参数或账号信息。'); return }
    if (!canRetain && !secret || authType === 'basic' && secret && !username) { setError('请填写完整认证信息；更换目标或认证方式需要新密钥。'); return }
    const controller = new AbortController(); operation.current = controller; setBusy(true); setError('')
    try {
      await saveCredential({ name: name.trim(), origin, auth_type: authType, header_name: authType === 'api_key' ? headerName : '', username: authType === 'basic' && secret ? username : '', secret, disabled }, detail, controller.signal)
      if (!controller.signal.aborted) await finish()
    } catch (caught) { if (!controller.signal.aborted) { clearSecrets(); setError(credentialError(caught)) } }
    finally { if (!controller.signal.aborted) setBusy(false); operation.current = null }
  }
  const remove = async () => {
    if (!detail || operation.current) return
    const controller = new AbortController(); operation.current = controller; setBusy(true); setError(''); clearSecrets()
    try { await deleteCredential(detail, controller.signal); if (!controller.signal.aborted) await finish() }
    catch (caught) { if (!controller.signal.aborted) setError(credentialError(caught)) }
    finally { if (!controller.signal.aborted) setBusy(false); operation.current = null }
  }
  return <section className={styles.page}>
    <AIInfraWorkbenchHeader title={detail ? '编辑基础设施凭据' : '新建基础设施凭据'} description="凭据用于访问被扫描的基础设施服务，保存后密钥不会回显。" backLink={{ to: '/credentials/target-credentials', label: '返回基础设施凭据' }} />
    {error ? <MessageBar intent="error"><MessageBarBody>{error}{detail ? <Button onClick={() => { clearSecrets(); reload() }} disabled={busy}>刷新凭据</Button> : null}</MessageBarBody></MessageBar> : null}
    <form className={styles.creationForm} onSubmit={submit} autoComplete="off">
      <section className={styles.surface}><h2 className={styles.sectionHeading}>目标与认证</h2><div className={styles.configurationGrid}>
        <Field label="凭据名称" required><Input value={name} required maxLength={160} disabled={busy} onChange={(_, data) => setName(data.value)} placeholder="例如：测试环境推理服务" /></Field>
        <Field label="认证方式"><Select value={authType} disabled={busy} onChange={(_, data) => { setAuthType(data.value as TargetAuthType); setHeaderName(''); clearSecrets() }}>{Object.entries(authLabels).map(([value, label]) => <option value={value} key={value}>{label}</option>)}</Select></Field>
        <Field className={styles.fullWidth} label="允许访问的目标地址" required hint="支持 HTTP 和 HTTPS，填写协议、主机和端口，例如 http://inference.internal:8080。任务可以扫描该地址下的多个路径。"><Input type="url" value={origin} required disabled={busy} onChange={(_, data) => setOrigin(data.value)} placeholder="http://inference.internal:8080" /></Field>
        {authType === 'api_key' ? <Field label="API Key 请求头名称" required><Input value={headerName} required disabled={busy} onChange={(_, data) => setHeaderName(data.value)} placeholder="X-API-Key" /></Field> : null}
        {authType === 'basic' ? <Field label="认证用户名" hint={canRetain ? '替换密码时请同时填写用户名；保留原认证时留空。' : undefined}><Input value={username} disabled={busy} autoComplete="off" onChange={(_, data) => setUsername(data.value)} /></Field> : null}
        <Field label={authType === 'cookie' ? 'Cookie 内容' : authType === 'basic' ? '认证密码' : '认证密钥'} required={!canRetain} hint={canRetain ? '已配置。留空保留原值，输入新值替换。' : authType === 'bearer' ? '填写 Token 本身，系统会添加 Bearer 前缀。' : authType === 'cookie' ? '格式示例：session=你的会话值; tenant=你的租户值' : '认证信息加密保存。'}><Input type="password" autoComplete="new-password" value={secret} required={!canRetain} maxLength={8192} disabled={busy} onChange={(_, data) => setSecret(data.value)} placeholder={canRetain ? '已配置 / 留空保留' : '输入认证信息'} /></Field>
        <div className={styles.fullWidth}><Checkbox checked={disabled} disabled={busy} label="停用此凭据" onChange={(_, data) => setDisabled(data.checked === true)} /><Text block size={200}>认证请求仅发送到相同协议、主机和端口的目标。停用或修改后，尚未下发的旧版本任务将停止使用此凭据。</Text></div>
      </div></section>
      <div className={styles.submitActions}><Button type="button" disabled={busy} onClick={() => { clearSecrets(); navigate('/credentials/target-credentials') }}>取消</Button><Button type="submit" appearance="primary" disabled={busy}>{busy ? '正在保存' : '保存凭据'}</Button></div>
    </form>
    {detail ? <div>{confirmDelete ? <MessageBar intent="warning"><MessageBarBody>删除“{detail.name}”后，新任务和待下发任务将无法使用它。<Button disabled={busy} onClick={() => void remove()}>确认删除凭据</Button><Button disabled={busy} onClick={() => setConfirmDelete(false)}>取消删除</Button></MessageBarBody></MessageBar> : <Button disabled={busy} onClick={() => setConfirmDelete(true)}>删除凭据</Button>}</div> : null}
  </section>
}
