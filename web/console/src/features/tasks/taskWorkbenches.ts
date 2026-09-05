/** 功能：集中声明专属任务工作台的业务文案与路径，供共用布局读取。 */
import type { TaskType } from '../../shared/api/types'

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
}

export function taskWorkbenchFor(taskType?: TaskType): TaskWorkbench | undefined {
  return taskType ? workbenches[taskType] : undefined
}
