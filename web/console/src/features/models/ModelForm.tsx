/**
 * 功能：创建或编辑当前角色允许的受治理模型配置。
 * 实现：以 Fluent 可访问表单收集白名单字段，Token 从不回填且成败都清空。
 * 输入：用户或管理员角色、可选可写模型与完成/取消回调。
 * 输出：单次创建或更新写请求、固定错误与已清理敏感输入。
 * 依赖：Fluent UI、React 取消边界与模型 API 适配器。
 */
import {
  Button,
  Dialog,
  DialogActions,
  DialogBody,
  DialogContent,
  DialogSurface,
  DialogTitle,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
  Switch,
  Text,
  Textarea,
  makeStyles,
  tokens,
} from '@fluentui/react-components'
import { useEffect, useRef, useState, type FormEvent } from 'react'

import { createModel, updateModel, type ModelCatalogItem } from './api'

interface ModelFormProps {
  role: 'user' | 'admin'
  model?: ModelCatalogItem
  onSaved: () => void
  onCancel: () => void
}

const useStyles = makeStyles({
  root: {
    display: 'flex',
    flexDirection: 'column',
    gap: tokens.spacingVerticalL,
    padding: tokens.spacingVerticalL,
    border: `1px solid ${tokens.colorNeutralStroke1}`,
    borderRadius: tokens.borderRadiusMedium,
    backgroundColor: tokens.colorNeutralBackground1,
  },
  grid: {
    display: 'grid',
    gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',
    gap: tokens.spacingHorizontalL,
    '@media (max-width: 960px)': {
      gridTemplateColumns: '1fr',
    },
  },
  wide: { gridColumn: '1 / -1' },
  actions: {
    display: 'flex',
    justifyContent: 'flex-end',
    flexWrap: 'wrap',
    gap: tokens.spacingHorizontalM,
    '@media (max-width: 960px)': {
      flexDirection: 'column',
      alignItems: 'stretch',
      justifyContent: 'flex-start',
    },
  },
})

export function ModelForm({ role, model, onSaved, onCancel }: ModelFormProps) {
  const styles = useStyles()
  const [name, setName] = useState(model?.name ?? '')
  const [providerModel, setProviderModel] = useState(model?.provider_model ?? '')
  const [baseURL, setBaseURL] = useState(model?.base_url ?? '')
  const [note, setNote] = useState(model?.note ?? '')
  const [limit, setLimit] = useState(String(model?.limit ?? 0))
  const [disabled, setDisabled] = useState(model?.disabled ?? false)
  const [token, setToken] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [confirmDisable, setConfirmDisable] = useState(false)
  const mountedRef = useRef(true)
  const mutexRef = useRef(false)
  const controllerRef = useRef<AbortController | null>(null)
  const scope = model?.scope ?? (role === 'admin' ? 'global' : 'private')

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      controllerRef.current?.abort()
    }
  }, [])

  const cancel = () => {
    controllerRef.current?.abort()
    setToken('')
    setConfirmDisable(false)
    onCancel()
  }

  const performSave = async () => {
    if (mutexRef.current) return
    const parsedLimit = Number(limit)
    if (!Number.isSafeInteger(parsedLimit)) {
      setToken('')
      setConfirmDisable(false)
      setError('请填写有效的调用限制。')
      return
    }

    mutexRef.current = true
    setSubmitting(true)
    setError('')
    const controller = new AbortController()
    controllerRef.current?.abort()
    controllerRef.current = controller
    try {
      if (model) {
        await updateModel(model.id, {
          name,
          provider_model: providerModel,
          base_url: baseURL,
          note,
          limit: parsedLimit,
          disabled,
          ...(token ? { token } : {}),
        }, controller.signal)
      } else {
        await createModel({ name, provider_model: providerModel, base_url: baseURL, token, scope, note, limit: parsedLimit }, controller.signal)
      }
      if (!mountedRef.current || controller.signal.aborted) return
      setToken('')
      setConfirmDisable(false)
      onSaved()
    } catch {
      if (!mountedRef.current || controller.signal.aborted) return
      setToken('')
      setError('模型保存失败，请重新输入 Token 后重试。')
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
      mutexRef.current = false
      if (mountedRef.current) setSubmitting(false)
    }
  }

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (mutexRef.current) return
    if (model && !model.disabled && disabled) {
      setConfirmDisable(true)
      return
    }
    void performSave()
  }

  return (
    <form className={styles.root} onSubmit={submit} aria-label={model ? '编辑模型' : '新增模型'}>
      <Text as="h2" size={500} weight="semibold">{model ? '编辑模型' : `新增${scope === 'global' ? '全局' : '私有'}模型`}</Text>
      <Text>作用范围：{scope === 'global' ? '全局' : '本人私有'}。作用范围由当前角色确定，不由浏览器自报。</Text>
      {error ? <MessageBar intent="error" role="alert"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
      <div className={styles.grid}>
        <Field label="模型名称" required>
          <Input required value={name} onChange={(_, data) => setName(data.value)} autoComplete="off" />
        </Field>
        <Field label="供应商模型" required>
          <Input required value={providerModel} onChange={(_, data) => setProviderModel(data.value)} autoComplete="off" />
        </Field>
        <Field className={styles.wide} label="基础 URL" required>
          <Input required type="url" value={baseURL} onChange={(_, data) => setBaseURL(data.value)} autoComplete="url" />
        </Field>
        <Field
          className={styles.wide}
          label={model ? '新 Token（留空保持不变）' : '访问 Token'}
          required={!model}
          hint="Token 只在本次提交中发送，不会回填或保存在浏览器。"
        >
          <Input
            required={!model}
            type="password"
            value={token}
            onChange={(_, data) => setToken(data.value)}
            autoComplete="new-password"
          />
        </Field>
        <Field label="调用限制">
          <Input type="number" value={limit} onChange={(_, data) => setLimit(data.value)} />
        </Field>
        {model ? <Switch checked={disabled} onChange={(_, data) => setDisabled(data.checked)} label="停用模型" /> : null}
        <Field className={styles.wide} label="备注">
          <Textarea value={note} onChange={(_, data) => setNote(data.value)} resize="vertical" />
        </Field>
      </div>
      <div className={styles.actions}>
        <Button type="button" appearance="secondary" disabled={submitting} onClick={cancel}>取消</Button>
        <Button type="submit" appearance="primary" disabled={submitting}>
          {submitting ? '正在保存' : model ? '保存模型' : `创建${scope === 'global' ? '全局' : '私有'}模型`}
        </Button>
      </div>
      <Dialog
        open={confirmDisable}
        onOpenChange={(_, data) => { if (!data.open && !submitting) setConfirmDisable(false) }}
      >
        <DialogSurface aria-label="确认停用模型">
          <DialogBody>
            <DialogTitle>确认停用模型</DialogTitle>
            <DialogContent>停用后，新任务将无法选择该模型。已存在任务不会因此自动重跑。</DialogContent>
            <DialogActions>
              <Button type="button" appearance="secondary" disabled={submitting} onClick={() => setConfirmDisable(false)}>取消停用</Button>
              <Button
                type="button"
                appearance="primary"
                disabled={submitting}
                onClick={() => { setConfirmDisable(false); void performSave() }}
              >
                {submitting ? '正在停用' : '确认停用'}
              </Button>
            </DialogActions>
          </DialogBody>
        </DialogSurface>
      </Dialog>
    </form>
  )
}
