# 安全报告决策中心 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 `/reports` 与 `/reports/:reportId` 从基础归档视图升级为基于真实不可变快照的浅雾灰报告决策中心。

**Architecture:** `ReportListPage` 保持 URL、React Query、服务端分页和原生台账，新增纯展示的 `ReportPageReviewSummary` 仅从当前页 `ReportSummaryView[]` 与服务端 `total` 派生复核信号。`RiskSummary` 吸收详情首屏的覆盖范围与结论，`ReportDetailPage` 仅调整同一 RenderModel 的阅读顺序与局部响应式容器；不触碰 API、解析器、PDF 生命周期或共享表格。

**Tech Stack:** React 19、TypeScript、TanStack Query、Fluent UI v9、React Router、Vitest、Testing Library、Griffel makeStyles。

---

## File structure

- Create: `web/console/src/features/reports/components/ReportPageReviewSummary.tsx`
  - 纯展示当前服务端查询、本页复核信号和受控空态。
- Create: `web/console/src/features/reports/components/ReportPageReviewSummary.test.tsx`
  - 独立锁定风险派生、可访问语义和零结果边界。
- Modify: `web/console/src/features/reports/ReportListPage.tsx`
  - 接入摘要、列表空库/越界空页分支、雾灰台账与窄屏 viewport。
- Modify: `web/console/src/features/reports/components/RiskSummary.tsx`
  - 把固化覆盖范围与结论纳入决策摘要，保留重点风险排序。
- Modify: `web/console/src/features/reports/ReportDetailPage.tsx`
  - 调整同一快照的阅读优先级与局部响应式布局。
- Modify: `web/console/src/features/reports/ReportPages.test.tsx`
  - 锁定列表真实口径、空库/越界页、详情决策层和既有导出边界。

## Task 1: 用失败测试定义报告列表的真实复核范围

**Files:**
- Modify: `web/console/src/features/reports/ReportPages.test.tsx:1-123`

- [ ] **Step 1: 创建只含白名单字段的多风险列表 fixture**

从已存在的 `detail()` fixture 投影 `ReportSummaryView`，不要把 `render`、技术发现或原始响应放入列表 fixture：

```tsx
const reviewReports = [
  { id: 'report-high-a', task_id: 'task-a', task_type: 'mcp_scan', completed_at: '2026-08-18T01:00:00Z', created_at: '2026-08-18T01:01:00Z', brand_product_name: '历史快照品牌', risk: { mapping_version: 'risk-v2', high: 2, medium: 1, low: 3, score: 72 } },
  { id: 'report-medium', task_id: 'task-b', task_type: 'agent_scan', completed_at: '2026-08-19T01:00:00Z', created_at: '2026-08-19T01:01:00Z', brand_product_name: '历史快照品牌', risk: { mapping_version: 'risk-v2', high: 0, medium: 2, low: 1, score: 84 } },
  { id: 'report-high-b', task_id: 'task-c', task_type: 'ai_infra_scan', completed_at: '2026-08-20T01:00:00Z', created_at: '2026-08-20T01:01:00Z', brand_product_name: '历史快照品牌', risk: { mapping_version: 'risk-v2', high: 1, medium: 0, low: 0, score: 63 } },
] as const satisfies readonly ReportSummaryView[]
```

导入 `ReportSummaryView` 类型；不要使用 `as any` 或把详情 snapshot 混入列表。

- [ ] **Step 2: 写成功列表的失败断言**

以 `items: reviewReports, total: 45, page: 2, page_size: 20` 渲染 `/reports?page=2`。断言：

```tsx
const summary = await screen.findByRole('region', { name: '报告复核态势' })
expect(summary).toHaveTextContent('当前查询')
expect(summary).toHaveTextContent('全部报告')
expect(summary).toHaveTextContent('匹配报告 45')
expect(summary).toHaveTextContent('本页需优先复核 2')
expect(summary).toHaveTextContent('本页高风险发现 3')
expect(summary).not.toHaveTextContent('本页高风险发现 45')
```

同时保留原生 `table`、caption “不可变安全报告台账”、风险分布列、准确请求 URL、报告链接和服务端页码断言。不得新增“平均风险”“全局风险”或非白名单指标。

- [ ] **Step 3: 写两类空结果的失败断言**

增加独立测试：

1. `items: [], total: 0, page: 1`：摘要显示“匹配报告 0”与“暂无安全报告”，不显示本页复核/高风险发现零值或信号组。
2. `items: [], total: 45, page: 3`：摘要显示“匹配报告 45”，页面显示“当前页没有报告”，保留“共 45 条，第 3 页”和可用“上一页”，不显示“暂无安全报告”或零值信号。

加载、403、500 路径继续不显示报告复核态势。

- [ ] **Step 4: 运行红灯测试**

从 `web/console` 运行：

```powershell
.\node_modules\.bin\vitest.CMD run src\features\reports\ReportPages.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
```

Expected: FAIL，缺少“报告复核态势”且现有代码错误地把 `total > 0` 的空当前页展示成“暂无安全报告”；既有详情/PDF 测试不得成为新失败原因。

- [ ] **Step 5: 提交红灯契约**

```powershell
git add web/console/src/features/reports/ReportPages.test.tsx
git commit -m "test: define report decision workbench"
```

## Task 2: 实现无副作用的报告复核摘要

**Files:**
- Create: `web/console/src/features/reports/components/ReportPageReviewSummary.tsx`
- Create: `web/console/src/features/reports/components/ReportPageReviewSummary.test.tsx`
- Verify: `web/console/src/features/reports/ReportPages.test.tsx`

- [ ] **Step 1: 为独立展示组件写失败测试**

在 `ReportPageReviewSummary.test.tsx` 中直接导入待创建组件，使用安全 `ReportSummaryView` fixture，断言：

```tsx
expect(deriveReportPageReviewActivity(reports)).toEqual({ priorityReports: 2, highFindings: 3 })
expect(screen.getByRole('region', { name: '报告复核态势' })).toHaveTextContent('匹配报告 45')
expect(screen.getByRole('group', { name: '当前查询' })).toHaveTextContent('全部报告')
expect(screen.getByRole('group', { name: '本页复核信号' })).toHaveTextContent('本页需优先复核 2')
```

针对 `reports={[]}` 断言区域和总数保留、信号组以及“本页需优先复核 0”“本页高风险发现 0”两个零值文本均不存在。测试不观察 class 名、颜色或 URL。

- [ ] **Step 2: 确认组件尚不存在时红灯**

```powershell
.\node_modules\.bin\vitest.CMD run src\features\reports\components\ReportPageReviewSummary.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
```

Expected: FAIL，无法解析 `./ReportPageReviewSummary`；尚未接入的 `ReportPages.test.tsx` 红灯保持预期。

- [ ] **Step 3: 创建中文文件头的纯展示组件**

建立 `ReportPageReviewSummary.tsx`，文件头用中文说明：功能是当前页报告复核摘要；实现只在本地派生；输入安全摘要和服务端总数；输出具名 region；依赖 React、Fluent 和报告 DTO。

导出唯一状态派生函数：

```ts
export interface ReportPageReviewActivity {
  priorityReports: number
  highFindings: number
}

export function deriveReportPageReviewActivity(reports: readonly ReportSummaryView[]): ReportPageReviewActivity {
  return reports.reduce<ReportPageReviewActivity>((activity, report) => {
    if (report.risk.high > 0) activity.priorityReports += 1
    activity.highFindings += report.risk.high
    return activity
  }, { priorityReports: 0, highFindings: 0 })
}
```

受控 props 固定为：

```ts
interface ReportPageReviewSummaryProps {
  reports: readonly ReportSummaryView[]
  total: number
}
```

根 Card 使用 `role="region" aria-label="报告复核态势"`；内部“当前查询”和“本页复核信号”是具名 group。始终显示“全部报告”“匹配报告 {total}”；仅 `reports.length > 0` 显示“本页需优先复核”和“本页高风险发现”。使用 Fluent tokens 的雾灰表面，复核/高风险为警示或危险语义；不使用绿色表达“已完成”。不得请求、写 URL、接收 query 对象或包含空页决定逻辑。

- [ ] **Step 4: 将组件测试跑绿并确认集成红灯范围**

先运行组件测试，Expected: PASS。随后重跑 `ReportPages.test.tsx`；Expected: 仅列表尚未接入摘要/空页分流的失败，详情和 PDF 旧用例继续通过。

- [ ] **Step 5: 提交组件**

```powershell
git add web/console/src/features/reports/components/ReportPageReviewSummary.tsx web/console/src/features/reports/components/ReportPageReviewSummary.test.tsx
git commit -m "feat: add report review summary"
```

## Task 3: 接入报告台账、服务端空页与响应式局部滚动

**Files:**
- Modify: `web/console/src/features/reports/ReportListPage.tsx`
- Modify: `web/console/src/features/reports/ReportPages.test.tsx`
- Verify: `web/console/src/features/reports/components/ReportPageReviewSummary.test.tsx`

- [ ] **Step 1: 保留列表失败断言并锁定完整风险文本**

在成功 fixture 中确认表格仍能看见完整 “高 2 / 中 1 / 低 3” 风险分布与 `查看报告 report-high-a` 精确 href；不测试 CSS。

- [ ] **Step 2: 只在成功数据上接入摘要**

在 `query.isSuccess` 时渲染：

```tsx
<ReportPageReviewSummary reports={query.data.items} total={query.data.total} />
```

加载、403、错误路径不得显示摘要或上一次查询的残余指标。

- [ ] **Step 3: 分流空库与越界当前页**

只使用已经解析的服务端响应：

```tsx
const hasEmptyPage = query.data?.items.length === 0
const hasEmptyCollection = hasEmptyPage && query.data.total === 0
const hasOutOfRangePage = hasEmptyPage && query.data.total > 0
```

`hasEmptyCollection` 保留原“暂无安全报告”；`hasOutOfRangePage` 使用 `StatePanel state="empty" title="当前页没有报告" description="报告数据可能已变化，请返回上一页继续查看。"`。两个分支均保留现有 pagination，使页码和可用上一页可以修正 URL；绝不能修改 API 响应、强制跳转页码或在浏览器端自行填充数据。

- [ ] **Step 4: 收拢雾灰台账布局**

在现有 `makeStyles` 内增加：

- `page` 和 `tableViewport` 的 `minWidth: 0`；viewport 仅包 `DataTable`，使用 `overflowX: 'auto'`。
- 响应式 `pagination` / actions，在 960px 以下垂直堆叠但保留按钮。
- 安全分与风险分布的局部状态文本标记，文字始终同时表达“高 / 中 / 低”；色彩仅用 Fluent tokens，不能改变列序或替换成图标。

不得改 `pageFrom`、URL 正常化、query key、`fetchReportList` 参数、retry、caption、链接或时间 formatter。

- [ ] **Step 5: 锁定加载与错误状态不泄露摘要**

为加载、403、500 各增加或扩展一个 `ReportPages.test.tsx` 断言。加载使用一个仍未 resolve 的 `fetch` Promise，并先确认“正在加载安全报告”可见；三种路径均断言：

```tsx
expect(screen.queryByRole('region', { name: '报告复核态势' })).not.toBeInTheDocument()
```

加载用例在结束前 unmount，避免悬挂 Promise 污染下一例。不要只测试状态标题来间接推断摘要不存在。

- [ ] **Step 6: 全部列表测试跑绿**

```powershell
.\node_modules\.bin\vitest.CMD run src\features\reports\ReportPages.test.tsx src\features\reports\components\ReportPageReviewSummary.test.tsx --reporter=verbose --pool=forks --maxWorkers=1 --no-file-parallelism
```

Expected: PASS，所有列表、详情与 PDF 用例以及组件测试通过。

- [ ] **Step 7: 提交列表工作台**

```powershell
git add web/console/src/features/reports/ReportListPage.tsx web/console/src/features/reports/ReportPages.test.tsx
git commit -m "feat: refine report decision ledger"
```

## Task 4: 将详情重排为快照决策简报

**Files:**
- Modify: `web/console/src/features/reports/components/RiskSummary.tsx`
- Modify: `web/console/src/features/reports/ReportDetailPage.tsx`
- Modify: `web/console/src/features/reports/ReportPages.test.tsx`

- [ ] **Step 1: 先扩展详情的失败断言**

在 `只呈现同一不可变快照并按高、中、低稳定排序` 用例中，要求：

```tsx
const brief = await screen.findByRole('region', { name: '报告决策摘要' })
expect(brief).toHaveTextContent('安全评分')
expect(brief).toHaveTextContent('覆盖 1/1 条可信发现')
expect(brief).toHaveTextContent('完成修复后复核。')
expect(screen.getByRole('region', { name: '快照信息' })).toHaveTextContent('#2457a7')
expect(screen.getByRole('region', { name: '重点风险' })).toHaveTextContent('立即修复')
expect(screen.getByRole('region', { name: '修复建议' })).toHaveTextContent('优先修复高风险项')
```

继续断言高、中、低的稳定顺序、技术发现、趋势台账、品牌和水印，以及 PDF 测试全部保留。

- [ ] **Step 2: 运行详情红灯**

运行 Task 3 的同一报告定向命令。Expected: FAIL，尚无“报告决策摘要”或决策内容尚未归入该区域；导出失败/取消相关用例不得失败。

- [ ] **Step 3: 让 `RiskSummary` 成为受控决策摘要**

为 `RiskSummaryProps` 新增 `coverage`、`conclusion`，把根区域改为 `aria-label="报告决策摘要"`，标题改为“报告决策摘要”。在评分/分布/说明后以语义定义列表呈现覆盖范围和结论；内部 `section aria-label="重点风险"` 保持不变，并继续使用 `severityOrder` 稳定排序。低风险使用中性/信息 token 语义，完整“低风险”文本必须保留，不能把低风险误表达成已完成。将既有 `summary` 的单列断点由 640px 调整为 960px，保证详情决策摘要在规格定义的窄屏范围内收拢。

不读取 API 原始值，不更改 `TopRiskView` 排序/内容，不在组件内触发导出或导航。

- [ ] **Step 4: 重排 `ReportDetailPage`，不改导出状态机**

保留 `useMutation`、AbortController、epoch、对象 URL 和全部错误文案。仅在成功渲染的 JSX 中调整为：页头/导出 → PDF 错误条 → `RiskSummary`（传入 `coverage`、`conclusion`）→ “修复建议” → `TechnicalFindings` → 风险趋势 → “快照信息”。

删除原独立“覆盖与结论” Card，避免同一快照文本重复；快照 metadata 继续包含报告 ID、任务 ID、完成时间、生成时间、品牌、`primary_color` 文本和值为“无”的水印回退。为 `metadata` 添加局部 `minWidth: 0` / 960px 以下单列规则；新增 `trendViewport`（`minWidth: 0`, `overflowX: 'auto'`）只包裹趋势 `DataTable`，使窄屏时仅该台账可横向滚动。所有颜色使用 tokens。

- [ ] **Step 5: 详情及回归测试跑绿**

运行 Task 3 的报告定向命令。Expected: PASS；PDF 固定文件名、失败显式重试、卸载取消、晚到响应和切换报告隔离继续全部通过。

- [ ] **Step 6: 提交详情决策简报**

```powershell
git add web/console/src/features/reports/components/RiskSummary.tsx web/console/src/features/reports/ReportDetailPage.tsx web/console/src/features/reports/ReportPages.test.tsx
git commit -m "feat: refine report decision brief"
```

## Task 5: 执行全量门禁、浏览器验收与最终复核

**Files:**
- Verify: `web/console/src/features/reports/ReportListPage.tsx`
- Verify: `web/console/src/features/reports/ReportDetailPage.tsx`
- Verify: `web/console/src/features/reports/components/ReportPageReviewSummary.tsx`
- Verify: `web/console/src/features/reports/components/RiskSummary.tsx`
- Verify: `web/console/src/features/reports/ReportPages.test.tsx`

- [ ] **Step 1: 运行静态与类型门禁**

从 `web/console` 运行：

```powershell
pnpm run lint
pnpm run typecheck
```

Expected: 两者 exit 0。

- [ ] **Step 2: 执行完整前端门禁**

从仓库根目录运行：

```powershell
git diff --check 3bcafcad..HEAD
docker compose -f deploy\compose\docker-compose.frontend-test.yml run --rm console-test
```

Expected: diff check 无输出；Docker 门禁的 lint、typecheck、全量 Vitest 与 production build 都以 exit 0 结束。记录第三方 Tabster source-map、Keyborg disposal 和 bundle-size 警告，但不能把真实失败标为成功。

- [ ] **Step 3: 做登录后浏览器验收**

复用已有本地预览，不输入、不保存任何用户凭据。已登录用户打开 `/reports` 及一份真实详情后，检查：

1. 1280px 下列表的查询、复核、台账、分页层级清楚；详情先显示决策摘要与导出，再显示建议、证据、趋势和溯源。
2. 390px 下页面根容器与正文没有横向溢出；仅报告台账和趋势台账的局部 viewport 横向滚动，导出、分页和查看链接可达。
3. 真实空库和真实越界空页各自使用正确文案；无“本页 0”伪信号。
4. 一份真实报告的主色作为历史文本保留，导出 PDF 仍来自同一快照。

- [ ] **Step 4: 请求独立最终复核并报告**

让独立 reviewer 只读审阅 `3bcafcad..HEAD` 中报告页相关 diff，重点检查真实口径、不可变快照、URL/API/PDF 不变、空页、a11y、主题 token、窄屏、敏感字段和范围纪律。按可复现问题做最小修复后重跑受影响门禁；不触碰 `.superpowers/`。
