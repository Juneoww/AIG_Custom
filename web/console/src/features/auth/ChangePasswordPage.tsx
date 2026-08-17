/**
 * 功能：完成首次登录或管理员重置后的强制密码变更。
 * 实现：本地校验确认密码后调用真实改密接口，成功即清理客户端 Subject。
 * 输入：当前密码、新密码和确认值。
 * 输出：改密请求、固定错误提示与重新登录要求。
 * 依赖：React、Fluent UI 与身份会话上下文。
 */
import { useRef, useState, type FormEvent } from 'react'
import {
  Button,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
  MessageBarTitle,
  Text,
  makeStyles,
  shorthands,
  tokens,
} from '@fluentui/react-components'

import { ApiError, NetworkError } from '../../shared/api/errors'
import { useSession } from './session'

const useStyles = makeStyles({
  page: {
    minHeight: '100dvh',
    display: 'grid',
    placeItems: 'center',
    padding: '32px 20px',
    backgroundColor: tokens.colorNeutralBackground2,
    color: tokens.colorNeutralForeground1,
  },
  panel: {
    width: '100%',
    maxWidth: '480px',
    padding: '36px',
    ...shorthands.border('1px', 'solid', tokens.colorNeutralStroke2),
    borderRadius: tokens.borderRadiusLarge,
    backgroundColor: tokens.colorNeutralBackground1,
    boxShadow: tokens.shadow4,
    '@media (max-width: 520px)': {
      padding: '28px 20px',
    },
  },
  heading: {
    display: 'block',
    margin: 0,
    fontSize: tokens.fontSizeBase600,
    lineHeight: tokens.lineHeightBase600,
    fontWeight: tokens.fontWeightSemibold,
  },
  description: {
    display: 'block',
    marginTop: tokens.spacingVerticalS,
    color: tokens.colorNeutralForeground2,
    lineHeight: tokens.lineHeightBase400,
  },
  form: {
    display: 'grid',
    rowGap: tokens.spacingVerticalL,
    marginTop: tokens.spacingVerticalXXL,
  },
  submit: {
    minHeight: '40px',
  },
})

function changePasswordErrorMessage(error: unknown): string {
  if (error instanceof ApiError && error.kind === 'unauthenticated') {
    return '当前密码不正确或登录已失效，请重新登录。'
  }
  if (error instanceof ApiError && error.kind === 'forbidden') return '安全校验失败，请刷新页面后重试。'
  if (error instanceof ApiError && error.kind === 'insecure-transport') return error.message
  if (error instanceof NetworkError) return error.message
  return '暂时无法更新密码，请稍后重试。'
}

export function ChangePasswordPage() {
  const styles = useStyles()
  const { changePassword } = useSession()
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [errorMessage, setErrorMessage] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const submittingRef = useRef(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (submittingRef.current) return
    setErrorMessage('')
    if (newPassword !== confirmation) {
      setOldPassword('')
      setNewPassword('')
      setConfirmation('')
      setErrorMessage('两次输入的新密码不一致。')
      return
    }

    submittingRef.current = true
    setSubmitting(true)
    try {
      await changePassword(oldPassword, newPassword)
    } catch (error) {
      setOldPassword('')
      setNewPassword('')
      setConfirmation('')
      setErrorMessage(changePasswordErrorMessage(error))
    } finally {
      submittingRef.current = false
      setSubmitting(false)
    }
  }

  return (
    <main className={styles.page}>
      <section className={styles.panel} aria-labelledby="change-password-heading">
        <Text as="h1" className={styles.heading} id="change-password-heading">
          更新初始密码
        </Text>
        <Text className={styles.description}>完成密码更新后，系统将清除当前会话并要求重新登录。</Text>

        <form className={styles.form} onSubmit={handleSubmit}>
          {errorMessage ? (
            <MessageBar intent="error">
              <MessageBarBody>
                <MessageBarTitle>无法更新密码</MessageBarTitle>
                {errorMessage}
              </MessageBarBody>
            </MessageBar>
          ) : null}

          <Field label="当前密码" required>
            <Input
              autoComplete="current-password"
              disabled={submitting}
              name="old-password"
              type="password"
              value={oldPassword}
              onChange={(_, data) => setOldPassword(data.value)}
            />
          </Field>
          <Field label="新密码" required>
            <Input
              autoComplete="new-password"
              disabled={submitting}
              name="new-password"
              type="password"
              value={newPassword}
              onChange={(_, data) => setNewPassword(data.value)}
            />
          </Field>
          <Field label="确认新密码" required>
            <Input
              autoComplete="new-password"
              disabled={submitting}
              name="new-password-confirmation"
              type="password"
              value={confirmation}
              onChange={(_, data) => setConfirmation(data.value)}
            />
          </Field>
          <Button
            appearance="primary"
            className={styles.submit}
            disabled={submitting || !oldPassword || !newPassword || !confirmation}
            type="submit"
          >
            {submitting ? '正在更新' : '更新密码'}
          </Button>
        </form>
      </section>
    </main>
  )
}
