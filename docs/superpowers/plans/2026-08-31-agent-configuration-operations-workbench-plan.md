# Agent Configuration Workbench Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** 把 Agent 配置页改造成“安全目录 → 受控本地工作区”的浅色维护工作台，同时保留既有 API、权限和敏感内容边界。

**Architecture:** 新增一个纯展示的 AgentConfigurationBrief，只接收目录计数和前端治理能力；AgentConfigPage 继续独占原文、模板、Prompt 和异步操作状态。目录 UI 只由当前成功的名称查询派生；已选工作区有独立的原文/模板就绪门槛，未就绪时不能写入或测试。

**Tech Stack:** React 19、TypeScript、Fluent UI v9、TanStack Query v5、Vitest、Testing Library、Vite。

---

## Scope guardrails

- 不修改 Go/Python、docs/api、Swagger、Agent API、服务端权限、CSRF 或配置 YAML/JSON 格式。
- 不将原文、密码字段、Prompt、测试输出放入 query cache、目录摘要或目录行。
- 不增加持续健康、风险评分、测试历史、审批、版本或回滚能力。
- 前端只对管理员启用模板查询与创建工作流；不改变既有模板路由的兼容授权语义。
- 每个 TypeScript/TSX 新文件或实质性重写都遵守 @chinese-script-comments 的中文文件头要求；每个实现任务先遵守 @test-driven-development。

## Command convention

前端 package manifest 固定要求 Node `22.18.0` 与 pnpm `10.15.0`。当前宿主运行时不满足该约束；所有前端验证均使用下面的一次性 Node `22.18.0` 容器。它只读挂载源码，在容器内建立临时副本和依赖目录，容器退出后自动删除，不会改写 `web/console/node_modules`。

~~~powershell
$consolePath = (Resolve-Path web/console).Path
docker run --rm -v "${consolePath}:/source:ro" node:22.18.0-alpine sh -lc "set -e; mkdir /workspace; tar -C /source --exclude='./node_modules' --exclude='./.pnpm-store' --exclude='./dist' -cf - . | tar -C /workspace -xf -; cd /workspace; corepack pnpm install --frozen-lockfile --ignore-scripts; corepack pnpm <COMMAND>"
~~~

将 `<COMMAND>` 替换为各步骤给出的 `exec vitest …`、`run lint`、`run typecheck` 或 `run build`。不要在宿主直接执行 `pnpm --dir web/console …`；后端仓库根没有前端 package manifest，且宿主的 Node/pnpm 版本不符合本项目约束。

## File structure

- Create: web/console/src/features/knowledge/components/AgentConfigurationBrief.tsx — 纯展示的完整目录概览，不读取会话、路由或查询缓存。
- Create: web/console/src/features/knowledge/components/AgentConfigurationBrief.test.tsx — 概览的计数、角色语义与敏感内容排除合同。
- Modify: web/console/src/features/knowledge/AgentConfigPage.tsx — 目录成功门控、受控工作区层级、模板/原文就绪门槛、局部结果与响应式样式。
- Modify: web/console/src/features/knowledge/KnowledgePages.test.tsx — Agent 目录缓存失败、角色边界、工作区就绪、关闭清理和交互语义合同。
- Create: docs/superpowers/plans/2026-08-31-agent-configuration-operations-workbench-plan.md — 本实施计划（已创建）。

## Shared test fixtures

在 KnowledgePages.test.tsx 的 knowledgeFetch 附近新增最小安全 fixture，避免测试依赖真实 Agent 或 Provider：

~~~ts
function agentTemplateFixture(): Response {
  return new Response(JSON.stringify({
    openai: {
      name: 'OpenAI',
      description: '受控测试模板',
      fields: [
        { field: 'base_url', label: '服务地址', type: 'text', required: true },
        { field: 'api_key', label: '访问凭据', type: 'password', required: true },
        { field: 'timeout', label: '请求超时', type: 'number', required: false, min: 1 },
      ],
    },
  }), { status: 200, headers: { 'Content-Type': 'application/json' } })
}
~~~

将 fixture 仅用于 /api/v1/knowledge/agent/template?language=zh；不要在任何断言、截图或错误文本中加入真实密钥。需要延迟响应时，用受控 Promise<Response> 与 resolver，不用真实计时或网络。

### Task 1: 建立安全的 Agent 目录概览组件

**Files:**
- Create: web/console/src/features/knowledge/components/AgentConfigurationBrief.test.tsx
- Create: web/console/src/features/knowledge/components/AgentConfigurationBrief.tsx

- [ ] **Step 1: 写出概览组件的失败测试**

在测试文件中建立三个独立断言：完整目录显示“当前目录 3 项”；管理员显示“可维护与验证”；只读角色显示“可查看原文，不可修改”。用 agent-token-sentinel、prompt-sentinel 和 test-output-sentinel 对整个 region 做否定断言。

~~~tsx
render(<AgentConfigurationBrief total={3} canManage />)
const brief = screen.getByRole('region', { name: 'Agent 配置目录概览' })
expect(within(brief).getByRole('group', { name: '当前目录范围' })).toHaveTextContent('当前目录 3 项')
expect(brief).not.toHaveTextContent(/agent-token-sentinel|prompt-sentinel|test-output-sentinel/)
~~~

- [ ] **Step 2: 运行新测试，确认因组件不存在而失败**

Run: `exec vitest run src/features/knowledge/components/AgentConfigurationBrief.test.tsx`（按 Command convention 执行）
Expected: FAIL，原因是 AgentConfigurationBrief 尚未存在或未导出。

- [ ] **Step 3: 实现最小纯展示组件**

在 AgentConfigurationBrief.tsx 添加中文文件头和严格 props：

~~~ts
export interface AgentConfigurationBriefProps {
  total: number
  canManage: boolean
}
~~~

使用 Fluent Card、Text、Badge 与 makeStyles 实现具名 region，显示总数、当前权限和“选择一项后在本地工作区查看或维护原文”。不接受名称数组、原文、模板、Prompt、测试输出或 query client；根元素 minWidth: 0，信号区在 960px 下单列。

- [ ] **Step 4: 运行组件测试，确认通过**

Run: `exec vitest run src/features/knowledge/components/AgentConfigurationBrief.test.tsx`（按 Command convention 执行）
Expected: PASS，三个安全/角色合同均通过。

- [ ] **Step 5: 提交测试合同**

~~~bash
git add web/console/src/features/knowledge/components/AgentConfigurationBrief.test.tsx
git commit -m "test: define agent configuration brief"
~~~

- [ ] **Step 6: 提交组件实现**

~~~bash
git add web/console/src/features/knowledge/components/AgentConfigurationBrief.tsx
git commit -m "feat: add agent configuration brief"
~~~

### Task 2: 接入成功门控的安全目录与工作台入口

**Files:**
- Modify: web/console/src/features/knowledge/KnowledgePages.test.tsx
- Modify: web/console/src/features/knowledge/AgentConfigPage.tsx

- [ ] **Step 1: 为 Agent 目录添加失败重取集成测试**

参考指纹页现有的 installFingerprintRefetchFailure，添加 Agent 专属 helper：首次 /agent/names 返回 ['agent-one']，第二次返回 403 或 500；/agent/agent-one 返回 agent-token-sentinel。对两个状态都断言：

~~~tsx
const view = renderRoute('admin', '/knowledge/agents')
fireEvent.click(await screen.findByRole('button', { name: '进入工作区 agent-one' }))
expect(await screen.findByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('agent-token-sentinel')
await view.queryClient.invalidateQueries({ queryKey: ['knowledge', 'agents'] })
await expectAgentFailureState(status)
expect(screen.queryByRole('region', { name: 'Agent 配置目录概览' })).not.toBeInTheDocument()
expect(screen.queryByRole('table', { name: 'Agent 配置台账' })).not.toBeInTheDocument()
expect(screen.getByRole('textbox', { name: 'Agent 配置原文' })).toHaveValue('agent-token-sentinel')
~~~

同时断言 query cache、目录概览和目录 DOM 都不含该哨兵。添加 agentTemplateFixture()，使管理员模板请求可确定完成但不影响目录测试。

- [ ] **Step 2: 运行失败测试，确认当前页面错误地保留缓存目录**

Run: `exec vitest run src/features/knowledge/KnowledgePages.test.tsx`（按 Command convention 执行）
Expected: FAIL，当前页面会在目录重取失败后继续由 namesQuery.data 渲染旧目录，且尚无 Agent 概览和“进入工作区”语义。

- [ ] **Step 3: 实现目录成功门控与工作台入口**

在 AgentConfigPage.tsx 派生：

~~~ts
const catalog = namesQuery.isSuccess ? namesQuery.data : null
~~~

只用 catalog 渲染 AgentConfigurationBrief、空目录状态和 DataTable；保留加载/无权/错误 StatePanel。将目录动作改为可访问名称“进入工作区 ”加配置名，给当前选中配置附加可读的“当前工作区”状态。引入 AgentConfigurationBrief 并以 total={catalog.length}、canManage={admin} 调用。为表格加 tableViewport，同时把页面根、目录和工作区容器限制为 minWidth: 0 与 maxWidth: '100%'。

不得把 content、prompt、templateValues 或 actionResult 传入概览、表格行或 query cache。目录重取失败时不清除一个已成功加载的本地工作区，也不从旧缓存重新构建目录。

- [ ] **Step 4: 运行目录测试，确认通过**

Run: `exec vitest run src/features/knowledge/KnowledgePages.test.tsx`（按 Command convention 执行）
Expected: PASS，403/500 时只显示安全状态；目录概览/表格隐藏；本地已加载原文保持可见且缓存无敏感哨兵。

- [ ] **Step 5: 提交目录合同与实现**

~~~bash
git add web/console/src/features/knowledge/KnowledgePages.test.tsx
git commit -m "test: define agent workbench catalog"
git add web/console/src/features/knowledge/AgentConfigPage.tsx
git commit -m "feat: arrange agent configuration catalog"
~~~

### Task 3: 强化工作区就绪门槛与敏感生命周期

**Files:**
- Modify: web/console/src/features/knowledge/KnowledgePages.test.tsx
- Modify: web/console/src/features/knowledge/AgentConfigPage.tsx

- [ ] **Step 1: 写出原文/模板非就绪状态的失败测试**

增加以下基于受控 promise 的场景：

1. 已有配置原文仍在加载或加载失败时，只显示加载/重试和关闭；不渲染保存、删除、连通性、Prompt 输入或 Prompt 测试。
2. 已准备好的配置切换到另一个尚未返回原文的配置时，旧 valid 不能使任何操作出现或可用。
3. 管理员点击“新增 Agent 配置”后，模板加载、模板错误、模板空态都只能重试/关闭；模板成功且有已选模板后才显示原文编辑器和配置/测试区。
4. 关闭工作区后，agent-token-sentinel、模板值、prompt-sentinel 和 test-output-sentinel 不在 DOM 或 query cache。
5. 模板 ready 后保留 Provider 类型、两个必填字段、可展开的“高级设置”和“下载配置模板”入口。
6. 保存、连接或 Prompt 测试返回失败时，清空原文并使保存、连接、Prompt 测试不可用；旧 valid 不得保留为 true。

~~~tsx
expect(screen.queryByRole('button', { name: '保存 Agent 配置' })).not.toBeInTheDocument()
expect(screen.queryByRole('button', { name: '删除 Agent 配置' })).not.toBeInTheDocument()
expect(screen.queryByRole('button', { name: '测试连通性' })).not.toBeInTheDocument()
expect(screen.queryByRole('textbox', { name: '测试 Prompt' })).not.toBeInTheDocument()
~~~

- [ ] **Step 2: 运行新增测试，确认当前页面在非就绪态仍渲染操作区**

Run: `exec vitest run src/features/knowledge/KnowledgePages.test.tsx`（按 Command convention 执行）
Expected: FAIL，当前实现会在原文加载/错误阶段保留管理员操作区，并可能沿用旧结构校验状态。

- [ ] **Step 3: 实现显式的工作区就绪状态机**

在页面内派生而不是缓存敏感状态：

~~~ts
const templates = templatesQuery.isSuccess ? templatesQuery.data : null
const existingReady = selectedName !== null && configState === 'ready'
const createReady = creating && templates !== null && templates.length > 0 && selectedTemplate !== undefined
const workbenchReady = existingReady || createReady
~~~

在 openExisting、原文 effect 开始、原文 effect 失败、openCreate 以及 runAction 的失败分支中立即 setValid(false)。新增模板的 loading/error/empty StatePanel 与显式重试；除关闭（及 retry）外，所有编辑器、写入、删除、连接、Prompt 控件都必须受 workbenchReady 门控。runAction 也必须早退检查 workbenchReady，而不能只依赖已隐藏的按钮。

把操作结果消息移入具名“Agent 配置工作区”内，以便关闭和切换时连同原文/Prompt 一起消失；保留现有 AbortController、epoch、mutex、确认弹窗、下载 URL 回收和固定安全错误。已有配置在未 ready 时也不允许删除，避免基于未确认原文进行破坏性操作。

- [ ] **Step 4: 运行生命周期与现有 Agent 测试，确认通过**

Run: `exec vitest run src/features/knowledge/KnowledgePages.test.tsx src/features/knowledge/api.test.ts`（按 Command convention 执行）
Expected: PASS，所有新门槛、关闭清理、角色边界和原有 API 解析合同均通过。

- [ ] **Step 5: 提交工作区测试和实现**

~~~bash
git add web/console/src/features/knowledge/KnowledgePages.test.tsx
git commit -m "test: guard agent workbench readiness"
git add web/console/src/features/knowledge/AgentConfigPage.tsx
git commit -m "feat: guard agent configuration lifecycle"
~~~

### Task 4: 完成浅色工作台层级、可访问性与回归验收

**Files:**
- Modify: web/console/src/features/knowledge/KnowledgePages.test.tsx
- Modify: web/console/src/features/knowledge/AgentConfigPage.tsx

- [ ] **Step 1: 写出工作台语义与角色体验的失败测试**

补充面向最终结构的测试：

~~~tsx
const workspace = screen.getByRole('region', { name: 'Agent 配置工作区' })
expect(within(workspace).getByText('原文仅在当前本地工作区显示')).toBeInTheDocument()
expect(within(workspace).getByText('单次测试不代表持续健康')).toBeInTheDocument()
expect(screen.getByRole('button', { name: '进入工作区 openai' })).toBeInTheDocument()
~~~

普通用户与审计员打开 ready 的工作区时验证原文为只读，且看不到配置操作和受控验证区。管理员验证两个区域标题存在，保存/删除仍要确认，测试的成功/失败反馈在 workspace 内且不含服务端/Provider 原始错误。

- [ ] **Step 2: 运行结构测试，确认当前语义或区域尚不存在**

Run: `exec vitest run src/features/knowledge/KnowledgePages.test.tsx`（按 Command convention 执行）
Expected: FAIL，直到具名工作区、操作分区和提示语义实现完成。

- [ ] **Step 3: 完成雾灰工作台布局与语义**

用现有 Fluent token 完成以下结构，避免硬编码深色主题或虚构状态色：

~~~tsx
<section className={styles.workbench} aria-label="Agent 配置工作区">
  <header className={styles.workspaceHeader}>{/* 名称、局部说明、关闭 */}</header>
  <section aria-label="配置来源与原文">...</section>
  {admin && workbenchReady ? (
    <div className={styles.operationZones}>
      <section aria-label="配置操作">...</section>
      <section aria-label="受控验证">...</section>
    </div>
  ) : null}
</section>
~~~

在 operationZones 中使用两张浅雾灰卡片或两块带边界的区域：保存/删除与连通性/Prompt 测试分开；不使用红绿健康仪表。960px 以下单列；390px 以下按钮换行、输入控件全宽。表格只有局部横向滚动。保留 StructuredEditor 的 disabled={!admin || submitting}，让只读用户可查看但不能更改。

- [ ] **Step 4: 运行定向测试、lint、类型检查和生产构建**

Run:

~~~bash
exec vitest run src/features/knowledge/KnowledgePages.test.tsx src/features/knowledge/components/AgentConfigurationBrief.test.tsx src/features/knowledge/api.test.ts
run lint
run typecheck
run build
~~~

以上每一行均按 Command convention 分别执行。

Expected: 每条命令 exit 0；没有 TypeScript、ESLint 或 Vite 构建错误。

- [ ] **Step 5: 在登录后的本地预览做浏览器验收**

在用户自行登录后，以管理员和只读角色检查：目录概览、进入工作区、窄屏 960px/390px、空/无权/失败状态、确认弹窗、局部测试反馈、关闭后不再显示敏感内容。绝不读取或输入用户凭据。

- [ ] **Step 6: 提交最终结构与验证结果**

~~~bash
git add web/console/src/features/knowledge/KnowledgePages.test.tsx web/console/src/features/knowledge/AgentConfigPage.tsx
git commit -m "feat: refine agent configuration workbench"
~~~

完成后以 git status --short 确认只剩用户既有的未跟踪 .superpowers/，并报告浏览器验收是否仍等待用户登录会话。
