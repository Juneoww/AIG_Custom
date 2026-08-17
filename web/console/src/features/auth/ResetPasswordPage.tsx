/**
 * 功能：让用户手工提交带外交付的单次重置凭据与临时密码。
 * 实现：不读取 URL、存储或剪贴板，仅在表单提交时初始化 CSRF 并发送请求体。
 * 输入：用户手工粘贴的重置凭据、临时密码和确认值。
 * 输出：重置确认请求与固定中文状态提示。
 * 依赖：React、Fluent UI、共享错误类型与身份重置函数。
 */
import { useState, type FormEvent } from 'react'
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
import { confirmPasswordReset } from './session'

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
    maxWidth: '500px',
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
  loginLink: {
    display: 'block',
    marginTop: tokens.spacingVerticalL,
    color: tokens.colorBrandForegroundLink,
    textAlign: 'center',
    textDecorationLine: 'none',
  },
})

function resetErrorMessage(error: unknown): string {
  if (error instanceof ApiError && error.kind === 'unauthenticated') return '重置凭据无效或已失效。'
  if (error instanceof ApiError && error.kind === 'forbidden') return '安全校验失败，请刷新页面后重试。'
  if (error instanceof ApiError && error.kind === 'insecure-transport') return error.message
  if (error instanceof NetworkError) return error.message
  return '暂时无法重置密码，请稍后重试。'
}

export function ResetPasswordPage() {
  const styles = useStyles()
  const [token, setToken] = useState('')
  const [temporaryPassword, setTemporaryPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [errorMessage, setErrorMessage] = useState('')
  const [successMessage, setSuccessMessage] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setErrorMessage('')
    setSuccessMessage('')
    if (temporaryPassword !== confirmation) {
      setErrorMessage('两次输入的临时密码不一致。')
      return
    }

    setSubmitting(true)
    try {
      await confirmPasswordReset({ token, temporary_password: temporaryPassword })
      setToken('')
      setTemporaryPassword('')
      setConfirmation('')
      setSuccessMessage('密码已重置，请使用临时密码重新登录。')
    } catch (error) {
      setErrorMessage(resetErrorMessage(error))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className={styles.page}>
      <section className={styles.panel} aria-labelledby="reset-password-heading">
        <Text as="h1" className={styles.heading} id="reset-password-heading">
          重置密码
        </Text>
        <Text className={styles.description}>请手工粘贴管理员通过受控渠道交付的一次性重置凭据。</Text>

        <form className={styles.form} onSubmit={handleSubmit}>
          {errorMessage ? (
            <MessageBar intent="error">
              <MessageBarBody>
                <MessageBarTitle>无法重置密码</MessageBarTitle>
                {errorMessage}
              </MessageBarBody>
            </MessageBar>
          ) : null}
          {successMessage ? (
            <MessageBar intent="success">
              <MessageBarBody>
                <MessageBarTitle>重置完成</MessageBarTitle>
                {successMessage}
              </MessageBarBody>
            </MessageBar>
          ) : null}

          <Field label="重置凭据" hint="只使用受控渠道收到的完整凭据。" required>
            <Input
              autoComplete="off"
              disabled={submitting}
              name="reset-credential"
              type="password"
              value={token}
              onChange={(_, data) => setToken(data.value)}
            />
          </Field>
          <Field label="临时密码" required>
            <Input
              autoComplete="new-password"
              disabled={submitting}
              name="temporary-password"
              type="password"
              value={temporaryPassword}
              onChange={(_, data) => setTemporaryPassword(data.value)}
            />
          </Field>
          <Field label="确认临时密码" required>
            <Input
              autoComplete="new-password"
              disabled={submitting}
              name="temporary-password-confirmation"
              type="password"
              value={confirmation}
              onChange={(_, data) => setConfirmation(data.value)}
            />
          </Field>
          <Button
            appearance="primary"
            className={styles.submit}
            disabled={submitting || !token || !temporaryPassword || !confirmation}
            type="submit"
          >
            {submitting ? '正在重置' : '重置密码'}
          </Button>
        </form>
        <a className={styles.loginLink} href="/login">
          返回登录
        </a>
      </section>
    </main>
  )
}
