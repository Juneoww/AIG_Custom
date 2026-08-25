# 登录页「高级雾灰」重设计 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不改变认证、路由或密码安全边界的前提下，将企业控制台 `/login` 重做为用户已批准的暖白「高级雾灰」登录页。

**Architecture:** 只在 `LoginPage.tsx` 中重组布局并建立页面局部的 Fluent `makeStyles` 样式。现有 `useSession().login()` 调用、错误转换函数、受控输入状态与公共品牌 Provider 原样保留；视觉装饰是静态且 `aria-hidden` 的，不接入任何真实治理数据。

**Tech Stack:** React 19、TypeScript、Fluent UI v9、Vitest、React Testing Library、Vite、pnpm。

---

## 文件结构与边界

| 文件 | 角色 | 改动 |
| --- | --- | --- |
| `web/console/src/features/auth/LoginPage.tsx` | 登录页的表单行为、运行时品牌渲染、局部视觉布局 | 重构 JSX 与 `useStyles`；不改 `loginErrorMessage` 或提交状态机。 |
| `web/console/src/features/auth/pages.test.tsx` | 登录页的可访问性与敏感输入回归测试 | 新增批准后的静态治理外壳断言；保留现有认证失败和双击防抖断言。 |
| `docs/superpowers/specs/2026-08-25-login-warm-gray-redesign-design.md` | 已批准的设计约束 | 只读参考；实现期间不得修改范围。 |

不改动：`routes.tsx`、`session.ts`、`PublicBrandProvider.tsx`、`shared/theme/tokens.ts`、`shared/styles/global.css`、后端或 Docker 编排。

### Task 1: 用语义测试锁定已批准的页面外壳

**Files:**
- Modify: `web/console/src/features/auth/pages.test.tsx:57-106`
- Test: `web/console/src/features/auth/pages.test.tsx`

- [ ] **Step 1: 新增失败的登录页视觉语义测试**

  在既有 `describe('LoginPage', ...)` 的第一个测试之后新增如下测试。测试只锁定对用户和辅助技术有意义的文案；不要对 Fluent 生成的哈希类名或 CSS 像素值断言。

  ```tsx
  it('呈现已批准的治理叙事与受控访问提示，同时保留认证表单', () => {
    renderPage(<LoginPage />)

    expect(screen.getByText('受控本地访问')).toBeInTheDocument()
    expect(screen.getByText('TRUSTWORTHY AI OPERATIONS')).toBeInTheDocument()
    expect(screen.getByText('AUTHORIZED ACCESS')).toBeInTheDocument()
    expect(screen.getByText('资产可见')).toBeInTheDocument()
    expect(screen.getByText('风险可控')).toBeInTheDocument()
    expect(screen.getByText('治理可证')).toBeInTheDocument()
    expect(screen.getByText('登录活动将被记录，用于安全审计')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '登录平台' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '使用重置凭据' })).toHaveAttribute('href', '/reset-password')
  })
  ```

- [ ] **Step 2: 运行测试，确认它在旧页面上失败**

  Run:

  ```powershell
  Set-Location web/console
  pnpm run prepare:fonts
  pnpm exec vitest run src/features/auth/pages.test.tsx
  ```

  Expected: `LoginPage` 的新测试失败，缺少 `受控本地访问` 或 `TRUSTWORTHY AI OPERATIONS`；其余既有认证测试仍可运行。

- [ ] **Step 3: 提交仅含失败测试的检查点**

  ```powershell
  Set-Location ../..
  git add web/console/src/features/auth/pages.test.tsx
  git commit -m "test: define warm gray login shell"
  ```

### Task 2: 在 LoginPage 内实现暖白双栏外壳

**Files:**
- Modify: `web/console/src/features/auth/LoginPage.tsx:1-207`
- Test: `web/console/src/features/auth/pages.test.tsx:57-120`

- [ ] **Step 1: 将样式重构为页面局部的语义单元**

  保留 Fluent `makeStyles`，将当前 `page` / `panel` 为主的单卡样式替换为下列职责明确的键：

  ```ts
  const useStyles = makeStyles({
    page: { minHeight: '100dvh', position: 'relative', overflow: 'hidden' },
    backdrop: { minHeight: '100dvh', backgroundColor: '#f5f5f2' },
    topbar: { display: 'flex', justifyContent: 'space-between' },
    story: { display: 'grid', alignContent: 'center' },
    trustPillars: { display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' },
    authPanel: { width: '100%', maxWidth: '440px', backgroundColor: '#fbfaf7' },
    authBrand: { display: 'flex', alignItems: 'center', minWidth: 0 },
    brandText: { minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
    fieldInput: { width: '100%' },
    submit: { minHeight: '48px', backgroundColor: '#2c415d' },
    footerNotice: { display: 'flex', alignItems: 'center' },
  })
  ```

  完整实现应使用以下规则：暖白画布、墨蓝文本与按钮、细灰边框、低饱和鼠尾草绿/棕金点缀；左右栏约为 55/45；在 `@media (max-width: 950px)` 改为单列并隐藏轨道装饰。顶部和登录卡内的 `productName` 都要使用 `minWidth: 0`、单行省略与合理的最大宽度，避免长品牌名挤压状态或表单。所有颜色均限制在这个文件，不能修改全局主题令牌。

- [ ] **Step 2: 重组 JSX，但逐项保留认证契约**

  将当前 `<main>` 内容替换为以下层级；`handleSubmit`、四个 state、`submittingRef` 和 `loginErrorMessage` 保持字节级行为不变。

  ```tsx
  <main className={styles.page}>
    <div className={styles.backdrop}>
      <header className={styles.topbar}>
        <div className={styles.brandIdentity}>{/* 现有 logoDataURL / “安”回退和 productName；长名称单行省略 */}</div>
        <span className={styles.accessStatus}><span aria-hidden="true" />受控本地访问</span>
      </header>
      <div className={styles.layout}>
        <section className={styles.story} aria-label="平台治理能力">
          <Text className={styles.kicker}>TRUSTWORTHY AI OPERATIONS</Text>
          <Text as="h2" className={styles.storyHeading}>让每一次 AI 决策，都处于清晰的治理之中。</Text>
          <Text className={styles.storyCopy}>从资产识别、风险扫描到处置闭环……</Text>
          <div className={styles.trustPillars}>
            <div><Text>资产可见</Text><Text>模型、工具与数据连接</Text></div>
            <div><Text>风险可控</Text><Text>持续扫描与优先级处置</Text></div>
            <div><Text>治理可证</Text><Text>审计轨迹与可信报告</Text></div>
          </div>
          <div aria-hidden="true" className={styles.orbitalDecor} />
        </section>
        <section className={styles.authPanel} aria-labelledby="login-heading">
          <div className={styles.authBrand}>
            {/* 再次使用同一个 logoDataURL / “安”回退，不创建独立品牌源 */}
            <div><Text className={styles.authEyebrow}>AUTHORIZED ACCESS</Text><Text className={styles.brandText}>{productName}</Text></div>
          </div>
          {/* 原有 h1、说明、form、MessageBar、两个 Field 和 actions */}
          <Text className={styles.footerNotice}>登录活动将被记录，用于安全审计</Text>
        </section>
      </div>
    </div>
  </main>
  ```

  对表单部分执行以下不可变约束：

  ```tsx
  <Field label="用户名" required>
    <Input autoComplete="username" disabled={submitting} name="username" value={username} onChange={...} />
  </Field>
  <Field label="密码" required>
    <Input autoComplete="current-password" disabled={submitting} name="password" type="password" value={password} onChange={...} />
  </Field>
  <Button disabled={submitting || !username.trim() || !password} type="submit">
    {submitting ? '正在登录' : '登录'}
  </Button>
  <a href="/reset-password">使用重置凭据</a>
  ```

  `MessageBar` 必须留在 `<form>` 内、两个字段之前；认证错误不可只用颜色表达。加入图标、圆点、轨道或箭头时，全部设置 `aria-hidden="true"`，不改变登录按钮的可访问名称。

- [ ] **Step 3: 运行目标测试，确认认证与新外壳共同通过**

  Run:

  ```powershell
  Set-Location web/console
  pnpm exec vitest run src/features/auth/pages.test.tsx
  ```

  Expected: `LoginPage` 的四个测试通过；失败登录仍保留用户名、清空密码，同步双提交仍只触发一次登录流程。

- [ ] **Step 4: 提交实现与通过的测试**

  ```powershell
  Set-Location ../..
  git add web/console/src/features/auth/LoginPage.tsx web/console/src/features/auth/pages.test.tsx
  git commit -m "feat: redesign login with warm gray workspace"
  ```

### Task 3: 运行前端质量门禁并复核响应式视觉

**Files:**
- Verify: `web/console/src/features/auth/LoginPage.tsx`
- Verify: `web/console/src/features/auth/pages.test.tsx`
- Verify: `web/console/src/shared/styles/global.css`

- [ ] **Step 1: 运行静态检查、完整单元测试与构建**

  Run:

  ```powershell
  Set-Location web/console
  pnpm run prepare:fonts
  pnpm run lint
  pnpm run typecheck
  pnpm run test:run
  pnpm run build
  ```

  Expected: 每个命令退出码为 0；构建生成 `web/console/dist`，且不会在源代码中新增远程字体或图片 URL。

- [ ] **Step 2: 运行容器化前端门禁**

  Run from repository root:

  ```powershell
  docker compose -f deploy/compose/docker-compose.frontend-test.yml run --rm console-test
  ```

  Expected: 容器依次完成 frozen install、lint、typecheck、Vitest 与 Vite build，退出码为 0。

- [ ] **Step 3: 在隔离预览中进行视觉验收**

  使用与 E2E 预览相同的 `deploy/compose/docker-compose.console-e2e.yml`，但不要覆盖当前用户的 8089 预览或复用其临时 override。确认：

  - 1440px 下呈现暖白双栏，卡片位于右侧视觉重心，页面不再是左侧小卡片加大片空白；
  - 950px 及以下为单列，字段与按钮可完整点击；
  - 键盘 Tab 顺序依次经过用户名、密码、登录、重置凭据，焦点环清晰；
  - 使用长品牌名时，顶部和卡片品牌都省略而不溢出、不遮挡“受控本地访问”或字段；
  - 用错误凭据登录后，错误可读、用户名保留、密码清空；
  - 仅登录页改变，已登录控制台页面不发生主题回归。

- [ ] **Step 4: 记录验证结果并提交（若测试或页面代码在该阶段有修正）**

  ```powershell
  git status --short
  git add web/console/src/features/auth/LoginPage.tsx web/console/src/features/auth/pages.test.tsx
  git commit -m "test: verify warm gray login redesign"
  ```

  如果验证没有引入源码修正，不创建空提交；只在交付说明中记录实际运行的命令和结果。
