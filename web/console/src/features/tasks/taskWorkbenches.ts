/**
 * 功能：集中定义专属扫描工作台的文案与路径。
 * 实现：小型静态配置复用同一列表、指标和详情生命周期。
 * 输入：任务类型；输出：可选工作台配置；依赖：无。
 */
import type { TaskType } from '../../shared/api/types'

export type DedicatedTaskType = 'ai_infra_scan' | 'agent_scan' | 'skills_scan'

export interface TaskWorkbench {
  title: string
  description: string
  path: string
}

const workbenches: Partial<Record<TaskType, TaskWorkbench>> = {
  ai_infra_scan: {
    title: 'AI 基础设施扫描',
    description: '集中跟踪已授权目标的扫描任务',
    path: '/tasks/ai-infra',
  },
  skills_scan: {
    title: 'Skills 扫描',
    description: '集中跟踪技能包的静态安全扫描任务',
    path: '/tasks/skills',
  },
  agent_scan: {
    title: 'Agent 工作流扫描',
    description: '跟踪 Agent 动态安全扫描，查看任务状态与复核报告',
    path: '/tasks/agent-workflow',
  },
}

export function taskWorkbenchFor(taskType?: TaskType): TaskWorkbench | undefined {
  return taskType ? workbenches[taskType] : undefined
}
