/**
 * 功能：提供本地账号登录表单并展示固定中文认证反馈。
 * 实现：使用 Fluent 表单组件提交 Session 登录动作，保留失败时的输入上下文。
 * 输入：用户手工输入的用户名和密码。
 * 输出：登录请求、提交状态与脱敏错误提示。
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
    maxWidth: '440px',
    padding: '36px',
    ...shorthands.border('1px', 'solid', tokens.colorNeutralStroke2),
    borderRadius: tokens.borderRadiusLarge,
    backgroundColor: tokens.colorNeutralBackground1,
    boxShadow: tokens.shadow4,
    '@media (max-width: 520px)': {
      padding: '28px 20px',
    },
  },
  brand: {
    display: 'block',
    marginBottom: tokens.spacingVerticalM,
    color: tokens.colorBrandForeground1,
    fontWeight: tokens.fontWeightSemibold,
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
  actions: {
    display: 'grid',
    rowGap: tokens.spacingVerticalS,
  },
  submit: {
    minHeight: '40px',
  },
  resetLink: {
    color: tokens.colorBrandForegroundLink,
    textAlign: 'center',
    textDecorationLine: 'none',
  },
})

function loginErrorMessage(error: unknown): string {
  if (error instanceof ApiError && error.kind === 'unauthenticated') return '用户名或密码不正确。'
  if (error instanceof ApiError && error.kind === 'forbidden') return '安全校验失败，请刷新页面后重试。'
  if (error instanceof ApiError && error.kind === 'insecure-transport') return error.message
  if (error instanceof NetworkError) return error.message
  return '暂时无法登录，请稍后重试。'
}

export function LoginPage() {
  const styles = useStyles()
  const { login } = useSession()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [errorMessage, setErrorMessage] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const submittingRef = useRef(false)

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (submittingRef.current) return
    submittingRef.current = true
    setErrorMessage('')
    setSubmitting(true)
    try {
      await login(username, password)
    } catch (error) {
      setPassword('')
      setErrorMessage(loginErrorMessage(error))
    } finally {
      submittingRef.current = false
      setSubmitting(false)
    }
  }

  return (
    <main className={styles.page}>
      <section className={styles.panel} aria-labelledby="login-heading">
        <Text className={styles.brand} size={200}>
          AI 安全治理平台
        </Text>
        <Text as="h1" className={styles.heading} id="login-heading">
          登录平台
        </Text>
        <Text className={styles.description}>使用由平台管理员分配的本地账号继续。</Text>

        <form className={styles.form} onSubmit={handleSubmit}>
          {errorMessage ? (
            <MessageBar intent="error">
              <MessageBarBody>
                <MessageBarTitle>登录失败</MessageBarTitle>
                {errorMessage}
              </MessageBarBody>
            </MessageBar>
          ) : null}

          <Field label="用户名" required>
            <Input
              autoComplete="username"
              disabled={submitting}
              name="username"
              value={username}
              onChange={(_, data) => setUsername(data.value)}
            />
          </Field>
          <Field label="密码" required>
            <Input
              autoComplete="current-password"
              disabled={submitting}
              name="password"
              type="password"
              value={password}
              onChange={(_, data) => setPassword(data.value)}
            />
          </Field>
          <div className={styles.actions}>
            <Button
              appearance="primary"
              className={styles.submit}
              disabled={submitting || !username.trim() || !password}
              type="submit"
            >
              {submitting ? '正在登录' : '登录'}
            </Button>
            <a className={styles.resetLink} href="/reset-password">
              使用重置凭据
            </a>
          </div>
        </form>
      </section>
    </main>
  )
}
