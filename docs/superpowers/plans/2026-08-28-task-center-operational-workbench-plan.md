# 扫描任务运营工作台 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** 将 /tasks 从基础筛选表格升级为呈现真实运行态势、异常关注与可追溯台账的浅雾灰任务工作台。

**Architecture:** TaskListPage 保持会话授权、URL 规范化、React Query、状态分支、表格和分页。新增 TaskOperationsSummary 只从已校验的当前页 TaskSummary、服务端总数和受控筛选派生查询上下文及“本页”信号；不发请求、不写入、不修改 URL。状态标记、局部表格滚动和响应式样式保留在列表页，避免改变共享 DataTable API。

**Tech Stack:** React 19、TypeScript、TanStack Query、Fluent UI v9、React Router、Vitest、Testing Library、Griffel makeStyles。

---

## File structure

- Create: web/console/src/features/tasks/components/TaskOperationsSummary.tsx
  - 纯展示当前查询、服务端匹配总数、当前页状态派生和可选清除筛选入口。
- Create: web/console/src/features/tasks/components/TaskOperationsSummary.test.tsx
  - 在尚未接入列表前，独立锁定本页状态派生、空态与无副作用展示语义。
- Modify: web/console/src/features/tasks/TaskListPage.tsx
  - 接入工作台摘要、状态标记、雾灰筛选/台账布局、局部滚动和清除筛选回调。
- Modify: web/console/src/features/tasks/TaskPages.test.tsx
  - 锁定真实本页口径、空态、清除筛选与既有台账/权限回归。

### Task 1: 用失败测试定义任务工作台的真实范围

**Files:**
- Modify: web/console/src/features/tasks/TaskPages.test.tsx:44-132

- [ ] **Step 1: 为多状态当前页建立安全 fixture**

在既有 task fixture 下创建 operationalTasks，只由 TaskSummary 白名单字段组成：

~~~tsx
const operationalTasks = [
  { ...task, id: 'task-pending', status: 'pending' },
  { ...task, id: 'task-dispatching', status: 'dispatching' },
  { ...task, id: 'task-running', status: 'running' },
  { ...task, id: 'task-failed', status: 'failed' },
  { ...task, id: 'task-unknown', status: 'dispatch_unknown' },
  { ...task, id: 'task-finished', status: 'succeeded' },
] as const
~~~

不要添加未白名单的服务端字段，也不要让测试通过 any 逃逸。

- [ ] **Step 2: 写出成功列表的失败断言**

新增普通用户列表测试，mock items 为 operationalTasks、total 为 47、page 为 1、page_size 为 20，并断言：

~~~tsx
const summary = await screen.findByRole('region', { name: '任务运行态势' })
expect(summary).toHaveTextContent('当前查询')
expect(summary).toHaveTextContent('匹配任务 47')
expect(summary).toHaveTextContent('本页正在执行 2')
expect(summary).toHaveTextContent('本页等待调度 1')
expect(summary).toHaveTextContent('本页需关注 2')
expect(summary).not.toHaveTextContent('本页正在执行 47')
~~~

同时确认 table 保持原生语义、caption 仍为“扫描任务台账”、每条查看任务 task-* 链接存在。不得添加耗时、风险或完成率等 API 没有返回的指标。

- [ ] **Step 3: 写出空态和清除筛选的失败断言**

添加两种边界：

1. 筛选后空响应保留任务运行态势中的当前查询和清除筛选按钮，但没有“本页正在执行”“本页等待调度”“本页需关注”零值信号。
2. 从 /tasks?page=3&status=running&task_type=agent_scan 渲染成功列表后点击“清除筛选”，验证第二次请求恰好是：
   http://localhost:3000/api/v1/platform/tasks?page=1&page_size=20

无筛选的 /tasks 不出现清除筛选。每个成功、空态和失败路径都断言 `screen.getByRole('group', { name: '任务筛选' })` 可找到筛选容器，并在其中通过标签找到“任务状态”和“任务类型”两个 select。使用 waitFor 和顺序 mock 响应，不读取路由内部状态来代替可见交互。

- [ ] **Step 4: 运行定向测试，确认它因工作台尚未实现而失败**

从 web/console 运行：

~~~powershell
.\node_modules\.bin\vitest.CMD run src\features\tasks\TaskPages.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

Expected: FAIL，明确找不到“任务运行态势”或“清除筛选”；既有取消、创建、附件和权限用例不应成为失败原因。

- [ ] **Step 5: 提交红灯测试**

~~~powershell
git add web/console/src/features/tasks/TaskPages.test.tsx
git commit -m "test: define task operational workbench"
~~~

### Task 2: 实现无副作用的查询和本页态势摘要

**Files:**
- Create: web/console/src/features/tasks/components/TaskOperationsSummary.tsx
- Create: web/console/src/features/tasks/components/TaskOperationsSummary.test.tsx
- Verify: web/console/src/features/tasks/TaskPages.test.tsx

- [ ] **Step 1: 先为独立摘要组件写失败测试**

新建 `TaskOperationsSummary.test.tsx`，只通过公开 props 渲染组件并断言：

- `dispatching` 与 `running` 计入“本页正在执行”，`pending` 计入“本页等待调度”，`failed`、`dispatch_failed`、`dispatch_unknown` 计入“本页需关注”；`succeeded` 与 `cancelled` 不增加任何信号。
- 根节点是具名的“任务运行态势” region，包含“当前查询”“本页运行信号”“匹配任务 {total}”和筛选标签。
- `tasks` 为空时仍显示当前查询、总数和可选“清除筛选”，但完全不显示三个零值信号。
- 组件没有请求、URL 写入或回调副作用；仅在点击清除按钮时调用传入回调。

先运行该测试，确认因组件尚不存在而失败。`TaskPages.test.tsx` 的四个工作台集成红灯在 Task 3 前仍属预期。

- [ ] **Step 2: 创建带中文文件头注释的展示组件**

建立 TaskOperationsSummary.tsx。文件头说明：从已校验任务列表响应呈现当前查询和本页态势；只做本地派生；输入安全任务摘要、总数和筛选显示信息；输出具名 region；依赖 React、Fluent UI 和共享任务 DTO。

- [ ] **Step 3: 导出唯一的本页状态派生函数**

在组件内导出类型和函数，保持所有状态集合在此唯一处定义：

~~~ts
export interface TaskPageActivity {
  active: number
  pending: number
  attention: number
}

export function deriveTaskPageActivity(tasks: readonly TaskSummary[]): TaskPageActivity {
  return tasks.reduce<TaskPageActivity>((activity, task) => {
    if (task.status === 'dispatching' || task.status === 'running') activity.active += 1
    if (task.status === 'pending') activity.pending += 1
    if (task.status === 'failed' || task.status === 'dispatch_failed' || task.status === 'dispatch_unknown') activity.attention += 1
    return activity
  }, { active: 0, pending: 0, attention: 0 })
}
~~~

succeeded 和 cancelled 不能增加任何计数；不得通过状态标签文字或未知原始响应字段计算数量。

- [ ] **Step 4: 定义受控 props 和语义结构**

使用如下 props，不把 URLSearchParams 或查询对象传入组件：

~~~ts
interface TaskOperationsSummaryProps {
  tasks: readonly TaskSummary[]
  total: number
  filterLabels: readonly string[]
  onClearFilters?: () => void
}
~~~

根元素使用 Card 的 region 语义和“任务运行态势”名称。内部用具名 group 分开“当前查询”和“本页运行信号”；“匹配任务 {total}”永远展示。tasks 为空时渲染当前查询和可选清除按钮，但不渲染三个零值信号。所有表面、描边、文字和阴影来自 Fluent tokens；需关注用警示 token，执行和等待不使用代表完成的绿色。


- [ ] **Step 5: 将独立组件测试跑绿**

先运行：

~~~powershell
.\node_modules\.bin\vitest.CMD run src\features\tasks\components\TaskOperationsSummary.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

Expected: PASS。随后运行 `TaskPages.test.tsx`，确认四个工作台集成红灯仍仅因列表尚未接入摘要、筛选 group 和清除按钮；既有回归继续通过。

- [ ] **Step 6: 提交摘要组件和已通过的展示测试**

~~~powershell
git add web/console/src/features/tasks/components/TaskOperationsSummary.tsx web/console/src/features/tasks/components/TaskOperationsSummary.test.tsx
git commit -m "feat: add task operational summary"
~~~

### Task 3: 将列表整合成响应式运营台账

**Files:**
- Modify: web/console/src/features/tasks/TaskListPage.tsx
- Modify: web/console/src/features/tasks/TaskPages.test.tsx
- Test: web/console/src/features/tasks/TaskPages.test.tsx

- [ ] **Step 1: 保留剩余清除筛选和状态标记失败断言**

在 Task 1 的交互断言之外，锁定可见语义和链接，不测试 class 名或像素值：

~~~tsx
expect(screen.getByText('执行中')).toBeVisible()
expect(screen.getByText('调度状态待确认')).toBeVisible()
expect(screen.getByRole('link', { name: '查看任务 task-running' })).toHaveAttribute('href', '/tasks/task-running')
~~~

- [ ] **Step 2: 用已有 URL 正常化函数接入摘要和清除行为**

在 TaskListPage 导入 TaskOperationsSummary，并构造显示标签：

~~~tsx
const filterLabels = [
  status ? '状态：' + taskStatusLabels[status] : '全部状态',
  taskType ? '类型：' + taskTypeLabels[taskType] : '全部类型',
]
const hasActiveFilters = Boolean(status || taskType)
~~~

仅在 query.isSuccess 时渲染摘要：

~~~tsx
<TaskOperationsSummary
  tasks={query.data.items}
  total={query.data.total}
  filterLabels={filterLabels}
  onClearFilters={hasActiveFilters ? () => updateSearch(1, undefined, undefined) : undefined}
/>
~~~

这样加载、403、错误不显示过时或虚构的摘要；空响应保留查询上下文。不得修改 normalizedTaskSearch、query key、fetchTaskList 参数、解析器或 retry 设置。

- [ ] **Step 3: 收拢雾灰布局、状态标记和窄屏台账 viewport**

在现有 makeStyles 中以 Fluent tokens 增加：

- filterPanel：淡中性背景、描边和圆角，用于容纳现有 Field/Select；其实际容器必须是 `role="group" aria-label="任务筛选"`，而不是仅有 aria-label 的普通 div；960px 以下单列。
- filters：大屏横向、窄屏单列，minWidth 为 0；选择框在窄屏占满。
- tableViewport：minWidth 为 0、overflowX 为 auto，只包裹 DataTable。
- statusMark 以及 active/pending/attention/terminal 变体：用 token 的文字、背景和描边使完整 taskStatusLabels 文案在浅/深/系统模式中可读。
- pagination：允许窄屏纵向堆叠，按钮仍可触达。

状态列改为包裹完整中文文本的 span，并根据既有安全 TaskStatus 给局部样式。不能替换成图标、缩写或只靠颜色的状态；保持列顺序、DataTable caption、查看链接和 UTC 格式化不变。

- [ ] **Step 4: 运行定向测试并确认全部通过**

从 web/console 运行：

~~~powershell
.\node_modules\.bin\vitest.CMD run src\features\tasks\TaskPages.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
~~~

Expected: PASS；新增工作台、清除筛选、空态和状态标记断言与全部既有任务页回归用例通过。

- [ ] **Step 5: 提交整合改动**

~~~powershell
git add web/console/src/features/tasks/TaskListPage.tsx web/console/src/features/tasks/TaskPages.test.tsx
git commit -m "feat: refine task operational workbench"
~~~

### Task 4: 全量校验与浏览器验收

**Files:**
- Verify: web/console/src/features/tasks/TaskListPage.tsx
- Verify: web/console/src/features/tasks/components/TaskOperationsSummary.tsx
- Verify: web/console/src/features/tasks/TaskPages.test.tsx

- [ ] **Step 1: 执行前端静态与类型门禁**

从 web/console 运行：

~~~powershell
pnpm run lint
pnpm run typecheck
~~~

Expected: 两条命令都以 exit code 0 结束。

- [ ] **Step 2: 执行格式和全量前端门禁**

从仓库根目录运行：

~~~powershell
git diff --check 26049827..HEAD
docker compose -f deploy\compose\docker-compose.frontend-test.yml run --rm console-test
~~~

Expected: git diff --check 无输出；Docker 门禁包含 lint、typecheck、全量 Vitest 和 production build，并以 exit code 0 结束。记录第三方 source map、Keyborg 或 bundle-size 警告，但不掩盖实际失败。

- [ ] **Step 3: 做登录后浏览器验收**

复用已有本地预览服务，不输入或保存任何用户凭据。由已登录用户访问 /tasks 后检查：

1. 1280px 宽度下，页头、筛选、查询上下文、本页态势、表格和分页有清晰层级；没有页面横向滚动。
2. 390px 宽度下，筛选和摘要为单列，表格只在自身容器横向滚动，创建入口与分页按钮可达。
3. 空态仅有当前查询与空态说明，不出现“本页 0”伪信号；筛选清除能返回无筛选 URL。
4. 已有详情链接、审计员只读与创建入口角色差异保持正确。

- [ ] **Step 4: 请求最终独立代码复核并报告结果**

让独立 reviewer 对 26049827..HEAD 中任务页面相关 diff 做只读审查，重点检查真实口径、URL/API 不变、空态、窄屏、主题 token、无障碍、角色与敏感字段。根据可复现问题做最小修复并重新运行受影响门禁；不触碰预先存在的 .superpowers/。
