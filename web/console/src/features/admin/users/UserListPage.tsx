/**
 * 功能：提供管理员专用的用户台账与受审计账户治理动作。
 * 实现：使用服务端分页读取安全用户 DTO；创建、改角色、启停和重置均为单次同源写请求。
 * 输入：管理员会话、分页 URL、用户表单与当前 CSRF Cookie。
 * 输出：原生用户表格、固定错误反馈及不回显临时密码的创建表单。
 * 依赖：Fluent UI、TanStack Query、共享会话、用户 API 适配层。
 */
import { Button, Input, MessageBar, MessageBarBody, Select, makeStyles, tokens } from '@fluentui/react-components'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'

import { ApiError } from '../../../shared/api/errors'
import { DataTable, type DataTableColumn } from '../../../shared/components/DataTable'
import { PageHeader } from '../../../shared/components/PageHeader'
import { StatePanel } from '../../../shared/components/StatePanel'
import {
  createUser,
  fetchUsers,
  requestPasswordReset,
  setUserActive,
  setUserRole,
  type UserView,
} from '../api'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  actions: { display: 'flex', flexWrap: 'wrap', gap: tokens.spacingHorizontalXS },
  form: { display: 'grid', gap: tokens.spacingVerticalS, maxWidth: '420px' },
  pagination: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: tokens.spacingHorizontalM },
})

function pageFrom(value: string | null): number {
  if (!value || !/^\d+$/.test(value)) return 1
  const page = Number(value)
  return Number.isSafeInteger(page) && page >= 1 && page <= 1_000 ? page : 1
}

export function UserListPage() {
  const styles = useStyles()
  const client = useQueryClient()
  const [searchParams, setSearchParams] = useSearchParams()
  const page = pageFrom(searchParams.get('page'))
  const [creating, setCreating] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<UserView['role']>('user')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const actionLockRef = useRef(false)

  const users = useQuery({
    queryKey: ['admin-users', { page, pageSize: 20 }],
    queryFn: ({ signal }) => fetchUsers({ page, pageSize: 20 }, signal),
    retry: false,
  })

  const refresh = () => client.invalidateQueries({ queryKey: ['admin-users'] })
  const runAction = async (key: string, action: () => Promise<void>) => {
    if (actionLockRef.current) return
    actionLockRef.current = true
    setBusy(key)
    setError('')
    try {
      await action()
      void refresh()
    } catch {
      setError('用户治理操作失败，请显式重试。')
    } finally {
      actionLockRef.current = false
      setBusy('')
    }
  }

  const submitCreate = async () => {
    if (actionLockRef.current || !username.trim() || !password) return
    actionLockRef.current = true
    setBusy('create')
    setError('')
    try {
      await createUser({ username: username.trim(), password, role })
      setUsername('')
      setPassword('')
      setRole('user')
      setCreating(false)
      void refresh()
    } catch {
      // 任意失败都清除敏感输入，不回显服务端原因。
      setPassword('')
      setError('用户创建失败，请检查输入后重试。')
    } finally {
      actionLockRef.current = false
      setBusy('')
    }
  }

  const columns: readonly DataTableColumn<UserView>[] = [
    { id: 'username', header: '用户名', render: (user) => user.username },
    { id: 'role', header: '角色', render: (user) => user.role === 'admin' ? '管理员' : user.role === 'auditor' ? '安全审计员' : '普通用户' },
    { id: 'active', header: '状态', render: (user) => user.active ? '启用' : '已禁用' },
    { id: 'password', header: '改密状态', render: (user) => user.must_change_password ? '待首次改密' : '正常' },
    {
      id: 'actions', header: '操作', render: (user) => (
        <div className={styles.actions}>
          <Button appearance="subtle" disabled={busy !== ''} aria-label={`${user.active ? '禁用' : '启用'} ${user.username}`} onClick={() => void runAction(`active:${user.id}`, () => setUserActive(user.id, !user.active))}>
            {user.active ? '禁用' : '启用'}
          </Button>
          <Select aria-label={`变更 ${user.username} 角色`} disabled={busy !== ''} value={user.role} onChange={(_, data) => void runAction(`role:${user.id}`, () => setUserRole(user.id, data.value as UserView['role']))}>
            <option value="user">普通用户</option><option value="auditor">安全审计员</option><option value="admin">管理员</option>
          </Select>
          <Button appearance="subtle" disabled={busy !== ''} aria-label={`发起 ${user.username} 密码重置`} onClick={() => void runAction(`reset:${user.id}`, () => requestPasswordReset(user.id))}>发起重置</Button>
        </div>
      ),
    },
  ]

  return (
    <section className={styles.page}>
      <PageHeader title="用户管理" description="仅管理员可创建、启停、改角色或发起站外密码重置。重置 Token 永不返回浏览器。">
        <Button appearance="primary" onClick={() => { setCreating(true); setError('') }}>新增用户</Button>
      </PageHeader>
      {error ? <MessageBar intent="error" role="alert"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
      {creating ? (
        <form className={styles.form} onSubmit={(event) => { event.preventDefault(); void submitCreate() }}>
          <label>用户名<Input value={username} onChange={(_, data) => setUsername(data.value)} /></label>
          <label>临时密码<Input type="password" value={password} onChange={(_, data) => setPassword(data.value)} /></label>
          <label>角色<Select value={role} onChange={(_, data) => setRole(data.value as UserView['role'])}><option value="user">普通用户</option><option value="auditor">安全审计员</option><option value="admin">管理员</option></Select></label>
          <div className={styles.actions}><Button appearance="secondary" type="button" disabled={busy !== ''} onClick={() => { setPassword(''); setCreating(false) }}>取消</Button><Button appearance="primary" type="submit" disabled={busy !== '' || !username.trim() || !password}>{busy === 'create' ? '正在创建' : '确认创建'}</Button></div>
        </form>
      ) : null}
      {users.isPending ? <StatePanel state="loading" title="正在加载用户台账" /> : null}
      {users.isError && users.error instanceof ApiError && users.error.kind === 'forbidden' ? <StatePanel state="forbidden" title="无权查看用户管理" /> : null}
      {users.isError && !(users.error instanceof ApiError && users.error.kind === 'forbidden') ? <StatePanel state="error" title="暂时无法加载用户台账" actionLabel="重试" onAction={() => void users.refetch()} /> : null}
      {users.data?.items.length === 0 ? <StatePanel state="empty" title="暂无用户" /> : null}
      {users.data?.items.length ? <DataTable caption="用户台账" columns={columns} rows={users.data.items} getRowKey={(user) => user.id} /> : null}
      {users.data ? <nav className={styles.pagination} aria-label="用户分页"><span>共 {users.data.total} 条，第 {users.data.page} 页</span><div><Button appearance="secondary" disabled={page <= 1} onClick={() => setSearchParams(page > 2 ? { page: String(page - 1) } : {})}>上一页</Button>{' '}<Button appearance="secondary" disabled={page * users.data.page_size >= users.data.total} onClick={() => setSearchParams({ page: String(page + 1) })}>下一页</Button></div></nav> : null}
    </section>
  )
}
