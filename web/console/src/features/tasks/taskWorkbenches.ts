/**
 * 功能：集中定义专属扫描工作台的文案与路径。
 * 实现：小型静态配置复用同一列表、指标和详情生命周期。
 * 输入：任务类型；输出：可选工作台配置；依赖：无。
 */
export type DedicatedTaskType = 'ai_infra_scan' | 'agent_scan'

const workbenches = {
  ai_infra_scan: { title: 'AI 基础设施扫描', path: '/tasks/ai-infra', description: '集中跟踪已授权目标的扫描任务' },
  agent_scan: { title: 'Agent 工作流扫描', path: '/tasks/agent-workflow', description: '跟踪 Agent 动态安全扫描，查看任务状态与复核报告' },
} as const

export function taskWorkbench(type?: string) {
  return type === 'ai_infra_scan' || type === 'agent_scan' ? workbenches[type] : undefined
}
