# 管理者仪表盘双轴态势简报 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** 将登录后的治理总览改造成面向管理者的浅色雾灰“双轴态势简报”，让真实风险水平与真实治理动态在首屏并列呈现。

**Architecture:** DashboardPage 保持查询和状态分支职责，把已校验的 DashboardView 传给新的局部展示组件。ExecutiveSummary 只呈现首层并列的风险态势与治理动态，ManagementSignals 在第二层与 RiskTrend 并列；二者只从 trend、recent_tasks、risk、security_score 与 mapping_versions 派生真实摘要。RiskTrend 保留可访问的 30 日列表语义并改为窄屏不产生页面横向溢出的柱状布局；现有两张可追溯台账继续保留在摘要之后。

**Tech Stack:** React 19、TypeScript、TanStack Query、Fluent UI v9、React Router、Vitest、Testing Library、Griffel makeStyles。

---

## File structure

- Create: web/console/src/features/dashboard/components/ExecutiveSummary.tsx
  - 首层管理者摘要的风险态势与治理动态，并导出唯一的治理活动派生函数。
- Create: web/console/src/features/dashboard/components/ManagementSignals.tsx
  - 第二层的管理者信号，只接收风险摘要、映射版本与已派生的治理活动。
- Modify: web/console/src/features/dashboard/DashboardPage.tsx
  - 保留查询、状态、两张台账和路由链接；以 ExecutiveSummary 和改造后的趋势／信号布局替换四张通用指标卡的首屏位置。
- Modify: web/console/src/features/dashboard/components/RiskTrend.tsx
  - 保持 figure、30 个 listitem 和每日报告标签，改成无需 720px 最小宽度的紧凑响应式趋势。
- Modify: web/console/src/features/dashboard/DashboardPage.test.tsx
  - 为双轴摘要、真实派生计数、状态回归和可访问区域补充集成测试。

### Task 1: 用测试锁定管理者摘要的真实数据口径

**Files:**
- Modify: web/console/src/features/dashboard/DashboardPage.test.tsx: DashboardPage 成功数据、空态、403 与重试用例

- [ ] **Step 1: 写出摘要的失败断言**

在成功响应用例中，把旧的“核心指标”四卡断言替换为管理者摘要区域，并使用默认 fixture 的真实数据验证：

~~~tsx
const summary = await screen.findByRole('region', { name: '管理者摘要' })
expect(summary).toHaveTextContent('当前总体安全分72')
expect(summary).toHaveTextContent('高风险3')
expect(summary).toHaveTextContent('中风险6')
expect(summary).toHaveTextContent('低风险12')
expect(summary).toHaveTextContent('近 30 日已完成扫描30')
expect(summary).toHaveTextContent('执行中任务1')
expect(summary).toHaveTextContent('待调度任务0')
~~~

保留现有多映射版本提示、原始响应敏感哨兵不进 DOM、高风险待办／最近任务链接和单次请求断言。

在空数据用例中新增：

~~~tsx
expect(screen.queryByRole('region', { name: '管理者摘要' })).not.toBeInTheDocument()
expect(screen.queryByRole('region', { name: '管理者信号' })).not.toBeInTheDocument()
~~~

这能保证“暂无报告”不会被 0 个扫描、0 个风险或任何仿真治理成果误读。

- [ ] **Step 2: 运行局部测试，确认它因缺少新摘要而失败**

Run from web/console:

~~~powershell
.\node_modules\.bin\vitest.CMD run src\features\dashboard\DashboardPage.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

Expected: FAIL，错误明确指出找不到“管理者摘要”或上述新文案；既有状态用例不应成为失败原因。

- [ ] **Step 3: 为治理动态状态矩阵增加失败断言**

新增一个成功响应 fixture，其中 recent_tasks 同时包含 pending、dispatching、running、succeeded。断言：

~~~tsx
expect(summary).toHaveTextContent('近 30 日已完成扫描60')
expect(summary).toHaveTextContent('执行中任务3')
expect(summary).toHaveTextContent('待调度任务2')
~~~

这里“执行中”按 pending、dispatching、running 计数；“待调度”仅按 pending、dispatching 计数。不要把成功、失败或取消任务计入。

- [ ] **Step 4: 运行局部测试，确认新的矩阵断言失败**

Run the same Vitest command.

Expected: FAIL，只因为摘要及其派生值尚未实现。

- [ ] **Step 5: Commit the red tests**

~~~powershell
git add web/console/src/features/dashboard/DashboardPage.test.tsx
git commit -m "test: define executive dashboard summary"
~~~

### Task 2: 实现纯展示的双轴摘要组件

**Files:**
- Create: web/console/src/features/dashboard/components/ExecutiveSummary.tsx
- Test: web/console/src/features/dashboard/DashboardPage.test.tsx

- [ ] **Step 1: 创建带中文模块注释的组件文件**

建立 ExecutiveSummary.tsx。文件头遵循项目中文注释格式，准确说明：

- 功能：从已校验 DashboardView 呈现首层风险态势与治理动态。
- 实现：纯展示与本地派生，不发请求、不重算服务端安全分。
- 输入：DashboardView。
- 输出：具名的管理者摘要 region。
- 依赖：React、Fluent UI 与 dashboard DTO。

- [ ] **Step 2: 实现并导出唯一的派生函数与类型**

在组件内导出 GovernanceActivity 类型和一个单一辅助函数 deriveGovernanceActivity，输入 trend 和 recent_tasks，输出：

~~~ts
{
  completedScans: number
  activeTasks: number
  queuedTasks: number
}
~~~

实现必须：

- completedScans 是所有 trend.completed 的和；
- activeTasks 的 status 是 pending、dispatching 或 running；
- queuedTasks 的 status 是 pending 或 dispatching；
- 不查看原始网络响应，也不引入接口未提供的覆盖率百分比。

- [ ] **Step 3: 渲染语义化管理者摘要**

以 section aria-label 为“管理者摘要”渲染两个同等级命名子区域：

1. “风险态势”：当前总体安全分、高／中／低风险和多映射版本提示。
2. “本期治理动态”：近 30 日已完成扫描、执行中任务、待调度任务。

数值使用 tabular-nums。安全分为 null 时展示“暂无”，不要替换为 0 或 100。风险与治理动态文本必须可由辅助技术直接读出。

- [ ] **Step 4: 用局部 makeStyles 实现已批准的浅色雾灰层级**

只在新组件内定义样式：

- 暖白面板、细暖灰边框、克制圆角和浅阴影；
- 深海军蓝用于标题和关键数值；
- 鼠尾草绿仅表达治理推进；
- 低饱和铜色仅表达高风险；
- 960px 以下将双轴摘要收为一列；
- 不使用渐变、霓虹色或与真实数据无关的进度条。

- [ ] **Step 5: 运行局部测试，确认 Task 1 的红测转绿**

Run the Task 1 Vitest command.

Expected: PASS，包含原有成功、空态、403、重试和敏感字段回归用例。

- [ ] **Step 6: Commit the summary**

~~~powershell
git add web/console/src/features/dashboard/components/ExecutiveSummary.tsx web/console/src/features/dashboard/DashboardPage.test.tsx
git commit -m "feat: add executive dashboard summary"
~~~

### Task 3: 重新组织 DashboardPage，而不改变查询与可追溯台账

**Files:**
- Create: web/console/src/features/dashboard/components/ManagementSignals.tsx
- Modify: web/console/src/features/dashboard/DashboardPage.tsx: imports、DashboardContent、局部样式
- Test: web/console/src/features/dashboard/DashboardPage.test.tsx

- [ ] **Step 1: 创建第二层 ManagementSignals 组件**

建立 ManagementSignals.tsx，带准确的中文模块注释。组件接收：

~~~ts
{
  risk: DashboardView['risk']
  mappingVersions: DashboardView['mapping_versions']
  activity: GovernanceActivity
}
~~~

以 section aria-label 为“管理者信号”渲染高风险、执行中任务和风险映射版本三行紧凑文本。组件不接收完整网络响应、不重新计算完成扫描数，也不渲染进度条或覆盖率。

- [ ] **Step 2: 删除首页对通用四指标卡的依赖**

移除 DashboardPage 中仅供旧四卡网格使用的 MetricCard import 和 metrics 样式。不要删除 taskTypeLabels、taskStatusLabels、formatDateTime、AttentionTable 或 RecentTaskTable。

- [ ] **Step 3: 只在有真实数据时放置摘要与信号**

在 DashboardContent 内：

1. 保留“暂无已完成报告”的空态 StatePanel。
2. 仅在 view.has_data 为 true 时，在趋势前渲染 ExecutiveSummary；为 false 时不渲染摘要数值。
3. 仅在 view.has_data 为 true 时，派生 GovernanceActivity 并把 ManagementSignals 放入趋势旁的第二层。
4. 无数据时仍保留趋势区域与两张台账的事实性空状态，但不显示“管理者摘要”或“管理者信号”。
5. 保留映射版本提示，但让它出现在风险态势语境内，且仍可由现有测试匹配。
6. 保留高风险待办和最近任务的 region 名称、DataTable caption 和链接 href。

DashboardPage 本身继续只负责 useQuery、403 分支、普通错误重试和 data 分支，不把派生规则移回页面容器。

- [ ] **Step 4: 以管理者阅读顺序调整布局**

将页面顺序固定为：

1. PageHeader；
2. 空态说明（如适用）；
3. ExecutiveSummary（仅有数据时）；
4. 风险趋势与 ManagementSignals 并列（仅有数据时）；
5. 高风险待办与最近任务的台账区域。

添加 DashboardPage 本地样式：

- 宽屏趋势／信号列为 1.25:0.75；
- 960px 及以下变为单列；
- 台账各自被局部 overflowX:auto 包裹，避免页面 main 出现横向溢出；
- 不改 AppShell、Sidebar、Topbar 或全局 theme。

- [ ] **Step 5: 扩展测试，锁定页面顺序、空态和台账回归**

在成功响应用例中断言：

- 管理者摘要、最近 30 日趋势、高风险待办、最近任务全部存在；
- 摘要在 DOM 中位于趋势前；
- 管理者信号在 DOM 中位于趋势区域内或紧邻趋势的第二层，不在管理者摘要内重复出现；
- 高风险报告与任务链接仍保留原有 href；
- 多映射版本文字仍出现。

在无数据用例中确认摘要和信号两个 region 都不存在，同时趋势和两张台账的空状态仍存在。

不要断言 Griffel 类名或精确像素值。

- [ ] **Step 6: 运行局部测试**

Run the Task 1 Vitest command.

Expected: PASS，且测试不依赖私有原始响应字段。

- [ ] **Step 7: Commit the integration**

~~~powershell
git add web/console/src/features/dashboard/DashboardPage.tsx web/console/src/features/dashboard/components/ManagementSignals.tsx web/console/src/features/dashboard/DashboardPage.test.tsx
git commit -m "feat: arrange executive governance dashboard"
~~~

### Task 4: 改造趋势图以适应窄屏并保留可访问性

**Files:**
- Modify: web/console/src/features/dashboard/components/RiskTrend.tsx
- Test: web/console/src/features/dashboard/DashboardPage.test.tsx

- [ ] **Step 1: 增加趋势的失败性结构断言**

在成功数据测试中，除 30 个 listitem 外，断言趋势 figure 的名称仍为“最近 30 日快照平均安全分”，并确认其父区域名称为“最近 30 日趋势”。

- [ ] **Step 2: 运行局部测试，确认断言在改造前描述现有语义**

Run the Task 1 Vitest command.

Expected: PASS；这一步证明重构时的无障碍基线，而不是制造人工失败。

- [ ] **Step 3: 移除 720px 固定最小宽度**

在 RiskTrend 的局部样式中：

- 将 30 列改为 repeat(30, minmax(0, 1fr))；
- 允许窄屏柱宽收缩，使用小间距和最小可辨识高度；
- 移除依赖内部横向滚动的 minWidth: 720px 与专用 viewport 包裹；
- 保留 figure、ol、li、每个日期的 aria-label、空值的空柱样式和首末日期轴。

不要修改趋势的服务端数据或改变每个 li 的 30 日数量。

- [ ] **Step 4: 给趋势增加与雾灰工作台协调的局部视觉**

使用局部 makeStyles 将普通柱调为低饱和鼠尾草色，空柱保持浅暖灰，坐标与标题使用中性灰。避免使用高饱和蓝色或渐变。

- [ ] **Step 5: 运行局部测试**

Run the Task 1 Vitest command.

Expected: PASS，30 个 listitem 和所有区域标签保持不变。

- [ ] **Step 6: Commit the responsive trend**

~~~powershell
git add web/console/src/features/dashboard/components/RiskTrend.tsx web/console/src/features/dashboard/DashboardPage.test.tsx
git commit -m "feat: refine responsive dashboard trend"
~~~

### Task 5: 完成静态检查、容器门禁与浏览器验收

**Files:**
- Verify: web/console/src/features/dashboard/DashboardPage.tsx
- Verify: web/console/src/features/dashboard/components/ExecutiveSummary.tsx
- Verify: web/console/src/features/dashboard/components/ManagementSignals.tsx
- Verify: web/console/src/features/dashboard/components/RiskTrend.tsx
- Verify: web/console/src/features/dashboard/DashboardPage.test.tsx

- [ ] **Step 1: 运行格式与局部回归检查**

Run:

~~~powershell
git diff --check
cd web/console
.\node_modules\.bin\vitest.CMD run src\features\dashboard\DashboardPage.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
pnpm run lint
pnpm run typecheck
~~~

Expected: 每项退出码为 0。

- [ ] **Step 2: 在隔离容器中运行完整前端门禁**

Run from repository root:

~~~powershell
docker compose -f deploy\compose\docker-compose.frontend-test.yml run --rm console-test
~~~

Expected: lint、typecheck、全部 Vitest 与 production build 均通过。记录任何第三方 source-map 或 bundle-size 警告，但不把非失败警告误报为产品回归。

- [ ] **Step 3: 进行浏览器视觉验收**

使用已启动的隔离预览：

1. 在宽屏确认首屏双轴摘要、趋势和信号的层级；
2. 在约 390px 确认单列、无页面横向溢出、所有真实数值清晰；
3. 检查加载、空态和错误文案没有被视觉改造掩盖；
4. 如需在浏览器中输入测试登录凭据，先在输入前向用户取得操作时确认。

- [ ] **Step 4: Commit any verification fixes**

~~~powershell
git add web/console/src/features/dashboard
git commit -m "fix: address dashboard verification findings"
~~~

Only create this commit if the verification or independent review produced an actual source change; do not create an empty verification commit.

- [ ] **Step 5: 交付前代码审查**

使用 superpowers:requesting-code-review 对最终差异进行独立审查。若发现阻塞问题，修复后重跑受影响测试和完整容器门禁。
