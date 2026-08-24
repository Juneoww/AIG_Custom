/**
 * 功能：为管理员提供全局模型存量 Token 的主密钥重加密确认。
 * 实现：以无凭据输入的 Fluent 对话框发起单次 POST，卸载或关闭时取消。
 * 输入：可写全局 platform 模型、开关状态与完成/关闭回调。
 * 输出：精确 204 确认、固定错误或已取消请求。
 * 依赖：Fluent UI、React 生命周期与模型 API 适配器。
 */
import {
  Button,
  Dialog,
  DialogActions,
  DialogBody,
  DialogContent,
  DialogSurface,
  DialogTitle,
  MessageBar,
  MessageBarBody,
} from '@fluentui/react-components'
import { useEffect, useRef, useState } from 'react'

import { rotateModelEncryption, type ModelCatalogItem } from './api'

interface RotateCredentialDialogProps {
  open: boolean
  model: ModelCatalogItem
  onClose: () => void
  onCompleted: () => void
}

export function RotateCredentialDialog({ open, model, onClose, onCompleted }: RotateCredentialDialogProps) {
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const mountedRef = useRef(true)
  const mutexRef = useRef(false)
  const controllerRef = useRef<AbortController | null>(null)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      controllerRef.current?.abort()
    }
  }, [])

  useEffect(() => {
    if (!open) {
      controllerRef.current?.abort()
      controllerRef.current = null
      mutexRef.current = false
      setSubmitting(false)
      setError('')
    }
  }, [open])

  if (model.source !== 'platform' || model.read_only || model.scope !== 'global') return null

  const close = () => {
    controllerRef.current?.abort()
    onClose()
  }

  const rotate = async () => {
    if (mutexRef.current) return
    mutexRef.current = true
    setSubmitting(true)
    setError('')
    const controller = new AbortController()
    controllerRef.current?.abort()
    controllerRef.current = controller
    try {
      await rotateModelEncryption(model.id, controller.signal)
      if (!mountedRef.current || controller.signal.aborted) return
      onCompleted()
      onClose()
    } catch {
      if (mountedRef.current && !controller.signal.aborted) setError('轮换加密失败，请显式重试。')
    } finally {
      if (controllerRef.current === controller) controllerRef.current = null
      mutexRef.current = false
      if (mountedRef.current) setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(_, data) => { if (!data.open) close() }}>
      <DialogSurface aria-label="轮换凭据加密">
        <DialogBody>
          <DialogTitle>轮换凭据加密</DialogTitle>
          <DialogContent>
            <p>将使用当前环境主密钥重新加密“{model.name}”已存的 Token。此操作不更换供应商 Token，且不会把明文返回浏览器。</p>
            {error ? <MessageBar intent="error" role="alert"><MessageBarBody>{error}</MessageBarBody></MessageBar> : null}
          </DialogContent>
          <DialogActions>
            <Button appearance="secondary" disabled={submitting} onClick={close}>取消</Button>
            <Button appearance="primary" disabled={submitting} onClick={() => void rotate()}>
              {submitting ? '正在轮换' : error ? '重试轮换加密' : '确认轮换加密'}
            </Button>
          </DialogActions>
        </DialogBody>
      </DialogSurface>
    </Dialog>
  )
}
