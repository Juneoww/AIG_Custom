/**
 * 功能：提供高级雾灰治理工作区中的本地账号登录表单与固定中文认证反馈。
 * 实现：消费公共品牌，以响应式双栏叙事承载 Fluent 表单并提交 Session 登录动作，保留失败时的输入上下文。
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
  mergeClasses,
} from '@fluentui/react-components'

import { ApiError, NetworkError } from '../../shared/api/errors'
import { usePublicBrand } from '../../shared/brand/PublicBrandProvider'
import { useSession } from './session'

const useStyles = makeStyles({
  page: {
    minHeight: '100dvh',
    position: 'relative',
    overflowX: 'hidden',
    overflowY: 'auto',
    color: '#1C2839',
    backgroundColor: '#F5F5F2',
  },
  backdrop: {
    minHeight: '100dvh',
    display: 'grid',
    gridTemplateRows: 'auto minmax(0, 1fr)',
    backgroundColor: '#F5F5F2',
    backgroundImage:
      'radial-gradient(circle at 14% 24%, rgba(197, 210, 197, 0.28), transparent 32%), radial-gradient(circle at 88% 72%, rgba(219, 207, 184, 0.26), transparent 30%), linear-gradient(135deg, rgba(255, 255, 255, 0.48), rgba(245, 245, 242, 0.12))',
  },
  topbar: {
    width: '100%',
    maxWidth: '1440px',
    minHeight: '72px',
    boxSizing: 'border-box',
    marginRight: 'auto',
    marginLeft: 'auto',
    paddingRight: '40px',
    paddingLeft: '40px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: '28px',
    borderBottom: '1px solid rgba(78, 91, 101, 0.14)',
    '@media (max-width: 950px)': {
      minHeight: '64px',
      paddingRight: '24px',
      paddingLeft: '24px',
      gap: '16px',
    },
    '@media (max-width: 520px)': {
      paddingRight: '16px',
      paddingLeft: '16px',
    },
  },
  topBrand: {
    minWidth: 0,
    maxWidth: 'min(62vw, 520px)',
    display: 'flex',
    alignItems: 'center',
    gap: '11px',
  },
  brandMark: {
    width: '36px',
    height: '36px',
    flexShrink: 0,
    display: 'grid',
    placeItems: 'center',
    overflow: 'hidden',
    border: '1px solid rgba(44, 65, 93, 0.16)',
    borderRadius: '10px',
    backgroundColor: '#E6EAE6',
    color: '#2C415D',
    fontWeight: 700,
    boxShadow: '0 5px 14px rgba(40, 53, 62, 0.08)',
  },
  brandLogo: {
    width: '100%',
    height: '100%',
    objectFit: 'contain',
  },
  topBrandText: {
    minWidth: 0,
    overflow: 'hidden',
    color: '#26364A',
    fontSize: '14px',
    fontWeight: 650,
    letterSpacing: '0.01em',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  accessStatus: {
    minHeight: '44px',
    flexShrink: 0,
    display: 'flex',
    alignItems: 'center',
    gap: '9px',
    color: '#56645F',
    fontSize: '12px',
    fontWeight: 600,
    letterSpacing: '0.08em',
    whiteSpace: 'nowrap',
  },
  statusDot: {
    width: '7px',
    height: '7px',
    borderRadius: '50%',
    backgroundColor: '#7B927F',
    boxShadow: '0 0 0 4px rgba(123, 146, 127, 0.13)',
  },
  layout: {
    width: '100%',
    maxWidth: '1280px',
    minHeight: 0,
    boxSizing: 'border-box',
    marginRight: 'auto',
    marginLeft: 'auto',
    padding: '56px 64px 72px',
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 1.18fr) minmax(420px, 0.82fr)',
    alignItems: 'center',
    gap: '72px',
    '@media (max-width: 1100px)': {
      paddingRight: '40px',
      paddingLeft: '40px',
      gap: '44px',
    },
    '@media (max-width: 950px)': {
      padding: '36px 24px 48px',
      gridTemplateColumns: 'minmax(0, 1fr)',
    },
    '@media (max-width: 520px)': {
      padding: '24px 16px 32px',
    },
  },
  story: {
    minWidth: 0,
    position: 'relative',
    display: 'grid',
    alignContent: 'center',
    paddingRight: '12px',
    '@media (max-width: 950px)': {
      display: 'none',
    },
  },
  storyContent: {
    position: 'relative',
    zIndex: 1,
  },
  kicker: {
    display: 'block',
    marginBottom: '22px',
    color: '#526757',
    fontSize: '11px',
    lineHeight: '16px',
    fontWeight: 700,
    letterSpacing: '0.18em',
  },
  storyHeading: {
    maxWidth: '650px',
    margin: 0,
    color: '#1C2839',
    fontSize: 'clamp(38px, 4vw, 54px)',
    lineHeight: 1.13,
    fontWeight: 620,
    letterSpacing: '-0.035em',
  },
  storyCopy: {
    display: 'block',
    maxWidth: '570px',
    marginTop: '24px',
    color: '#5F686D',
    fontSize: '16px',
    lineHeight: 1.75,
  },
  trustPillars: {
    maxWidth: '620px',
    marginTop: '42px',
    display: 'grid',
    gridTemplateColumns: 'repeat(3, minmax(0, 1fr))',
    gap: '24px',
  },
  pillar: {
    minWidth: 0,
    paddingTop: '16px',
    borderTop: '1px solid rgba(44, 65, 93, 0.22)',
  },
  pillarTitleRow: {
    display: 'flex',
    alignItems: 'center',
    gap: '9px',
  },
  pillarDotSage: {
    width: '6px',
    height: '6px',
    flexShrink: 0,
    borderRadius: '50%',
    backgroundColor: '#7F9582',
  },
  pillarDotGold: {
    width: '6px',
    height: '6px',
    flexShrink: 0,
    borderRadius: '50%',
    backgroundColor: '#A38A62',
  },
  pillarTitle: {
    color: '#26364A',
    fontSize: '14px',
    fontWeight: 650,
  },
  pillarCopy: {
    display: 'block',
    marginTop: '8px',
    color: '#536061',
    fontSize: '12px',
    lineHeight: 1.55,
  },
  orbitalDecor: {
    width: '270px',
    height: '270px',
    position: 'absolute',
    right: '-96px',
    top: '-58px',
    pointerEvents: 'none',
    opacity: 0.54,
  },
  orbitOuter: {
    width: '100%',
    height: '100%',
    boxSizing: 'border-box',
    position: 'absolute',
    border: '1px solid rgba(93, 118, 99, 0.22)',
    borderRadius: '50%',
  },
  orbitInner: {
    width: '58%',
    height: '58%',
    boxSizing: 'border-box',
    position: 'absolute',
    right: '21%',
    bottom: '21%',
    border: '1px solid rgba(154, 127, 84, 0.2)',
    borderRadius: '50%',
  },
  orbitNode: {
    width: '9px',
    height: '9px',
    position: 'absolute',
    right: '31px',
    top: '58px',
    borderRadius: '50%',
    backgroundColor: '#8B9D8D',
    boxShadow: '0 0 0 7px rgba(139, 157, 141, 0.12)',
  },
  authPanel: {
    width: '100%',
    maxWidth: '436px',
    boxSizing: 'border-box',
    position: 'relative',
    justifySelf: 'end',
    overflow: 'hidden',
    padding: '40px',
    border: '1px solid rgba(69, 80, 82, 0.16)',
    borderRadius: '18px',
    backgroundColor: 'rgba(252, 251, 247, 0.96)',
    boxShadow: '0 24px 65px rgba(48, 57, 61, 0.13), 0 4px 16px rgba(48, 57, 61, 0.06)',
    '@media (max-width: 950px)': {
      justifySelf: 'center',
    },
    '@media (max-width: 520px)': {
      padding: '28px 20px',
      borderRadius: '14px',
    },
  },
  panelStrip: {
    height: '4px',
    position: 'absolute',
    top: 0,
    right: 0,
    left: 0,
    backgroundImage: 'linear-gradient(90deg, #778D7A 0%, #9B8A68 42%, #CBD1C8 100%)',
  },
  authBrand: {
    minWidth: 0,
    display: 'flex',
    alignItems: 'center',
    gap: '13px',
  },
  authBrandCopy: {
    minWidth: 0,
    display: 'grid',
    gap: '3px',
  },
  authEyebrow: {
    color: '#705A3D',
    fontSize: '10px',
    lineHeight: '14px',
    fontWeight: 700,
    letterSpacing: '0.16em',
  },
  brandText: {
    minWidth: 0,
    overflow: 'hidden',
    color: '#43505F',
    fontSize: '13px',
    fontWeight: 600,
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  },
  heading: {
    display: 'block',
    margin: '30px 0 0',
    color: '#1C2839',
    fontSize: '29px',
    lineHeight: 1.24,
    fontWeight: 650,
    letterSpacing: '-0.025em',
  },
  description: {
    display: 'block',
    marginTop: '10px',
    color: '#697174',
    fontSize: '14px',
    lineHeight: 1.65,
  },
  form: {
    display: 'grid',
    rowGap: '18px',
    marginTop: '26px',
  },
  message: {
    border: '1px solid rgba(166, 91, 77, 0.2)',
    borderRadius: '9px',
    backgroundColor: '#FBF2EE',
  },
  fieldInput: {
    width: '100%',
    minHeight: '46px',
    borderTopColor: '#C9CDC8',
    borderRightColor: '#C9CDC8',
    borderBottomColor: '#C9CDC8',
    borderLeftColor: '#C9CDC8',
    borderRadius: '8px',
    backgroundColor: '#FEFDFB',
  },
  fieldInputDisabled: {
    borderTopColor: '#C7CEC5',
    borderRightColor: '#C7CEC5',
    borderBottomColor: '#C7CEC5',
    borderLeftColor: '#C7CEC5',
    backgroundColor: '#ECEFEA',
    color: '#59655F',
  },
  actions: {
    display: 'grid',
    rowGap: '10px',
    marginTop: '3px',
  },
  submit: {
    minHeight: '48px',
    borderRadius: '8px',
    backgroundColor: '#2C415D',
    color: '#FFFFFF',
    fontWeight: 650,
    ':hover': {
      backgroundColor: '#22354E',
    },
    ':disabled': {
      backgroundColor: '#D8DDD5',
      color: '#4F5C54',
    },
    ':disabled:hover': {
      backgroundColor: '#D8DDD5',
      color: '#4F5C54',
    },
  },
  resetLink: {
    minHeight: '44px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    color: '#51647A',
    fontSize: '13px',
    fontWeight: 600,
    textAlign: 'center',
    textDecorationLine: 'none',
    ':hover': {
      color: '#2C415D',
      textDecorationLine: 'underline',
    },
  },
  footerNotice: {
    marginTop: '22px',
    paddingTop: '18px',
    display: 'flex',
    alignItems: 'center',
    gap: '9px',
    borderTop: '1px solid rgba(69, 80, 82, 0.12)',
    color: '#596663',
    fontSize: '11px',
    lineHeight: 1.5,
  },
  auditDot: {
    width: '6px',
    height: '6px',
    flexShrink: 0,
    borderRadius: '50%',
    backgroundColor: '#8B9A89',
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
  const { productName, logoDataURL } = usePublicBrand()
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
      <div className={styles.backdrop}>
        <header className={styles.topbar}>
          <div className={styles.topBrand}>
            <span className={styles.brandMark} aria-hidden="true">
              {logoDataURL ? <img alt="" className={styles.brandLogo} src={logoDataURL} /> : '安'}
            </span>
            <Text aria-label={productName} className={styles.topBrandText} title={productName}>
              {productName}
            </Text>
          </div>
          <span className={styles.accessStatus}>
            <span aria-hidden="true" className={styles.statusDot} />
            受控本地访问
          </span>
        </header>

        <div className={styles.layout}>
          <section aria-label="平台治理能力" className={styles.story}>
            <div className={styles.storyContent}>
              <Text className={styles.kicker}>TRUSTWORTHY AI OPERATIONS</Text>
              <Text as="p" className={styles.storyHeading}>
                让每一次 AI 决策，都处于清晰的治理之中。
              </Text>
              <Text className={styles.storyCopy}>
                从资产识别、风险扫描到处置闭环，以一致的安全视角连接模型、工具与数据。
              </Text>
              <div className={styles.trustPillars}>
                <div className={styles.pillar}>
                  <div className={styles.pillarTitleRow}>
                    <span aria-hidden="true" className={styles.pillarDotSage} />
                    <Text className={styles.pillarTitle}>资产可见</Text>
                  </div>
                  <Text className={styles.pillarCopy}>模型、工具与数据连接</Text>
                </div>
                <div className={styles.pillar}>
                  <div className={styles.pillarTitleRow}>
                    <span aria-hidden="true" className={styles.pillarDotGold} />
                    <Text className={styles.pillarTitle}>风险可控</Text>
                  </div>
                  <Text className={styles.pillarCopy}>持续扫描与优先级处置</Text>
                </div>
                <div className={styles.pillar}>
                  <div className={styles.pillarTitleRow}>
                    <span aria-hidden="true" className={styles.pillarDotSage} />
                    <Text className={styles.pillarTitle}>治理可证</Text>
                  </div>
                  <Text className={styles.pillarCopy}>审计轨迹与可信报告</Text>
                </div>
              </div>
            </div>
            <div aria-hidden="true" className={styles.orbitalDecor}>
              <span className={styles.orbitOuter} />
              <span className={styles.orbitInner} />
              <span className={styles.orbitNode} />
            </div>
          </section>

          <section aria-labelledby="login-heading" className={styles.authPanel}>
            <div aria-hidden="true" className={styles.panelStrip} />
            <div className={styles.authBrand}>
              <span className={styles.brandMark} aria-hidden="true">
                {logoDataURL ? <img alt="" className={styles.brandLogo} src={logoDataURL} /> : '安'}
              </span>
              <div className={styles.authBrandCopy}>
                <Text className={styles.authEyebrow}>AUTHORIZED ACCESS</Text>
                <Text aria-label={productName} className={styles.brandText} title={productName}>
                  {productName}
                </Text>
              </div>
            </div>

            <Text as="h1" className={styles.heading} id="login-heading">
              登录平台
            </Text>
            <Text className={styles.description}>使用由平台管理员分配的本地账号继续。</Text>

            <form className={styles.form} onSubmit={handleSubmit}>
              {errorMessage ? (
                <MessageBar className={styles.message} intent="error">
                  <MessageBarBody>
                    <MessageBarTitle>登录失败</MessageBarTitle>
                    {errorMessage}
                  </MessageBarBody>
                </MessageBar>
              ) : null}

              <Field label="用户名" required>
                <Input
                  autoComplete="username"
                  className={mergeClasses(styles.fieldInput, submitting && styles.fieldInputDisabled)}
                  disabled={submitting}
                  name="username"
                  value={username}
                  onChange={(_, data) => setUsername(data.value)}
                />
              </Field>
              <Field label="密码" required>
                <Input
                  autoComplete="current-password"
                  className={mergeClasses(styles.fieldInput, submitting && styles.fieldInputDisabled)}
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

            <Text className={styles.footerNotice}>
              <span aria-hidden="true" className={styles.auditDot} />
              登录活动将被记录，用于安全审计
            </Text>
          </section>
        </div>
      </div>
    </main>
  )
}
