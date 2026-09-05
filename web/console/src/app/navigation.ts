/**
 * 功能：集中声明企业控制台主导航、二级导航、占位说明与角色授权矩阵。
 * 实现：以固定顺序的只读元数据驱动侧栏可见项、二级入口和路由授权。
 * 输入：当前身份角色。
 * 输出：完整导航元数据或该角色可见的有序子集。
 * 依赖：共享身份 DTO 类型。
 */
import type { SubjectRole } from '../shared/api/types'

export interface NavigationItem {
  id: string
  path: string
  label: string
  description: string
  allowedRoles: readonly SubjectRole[]
}

export interface SecondaryNavigationItem {
  id: string
  path: string
  label: string
  allowedRoles: readonly SubjectRole[]
}

const ALL_ROLES: readonly SubjectRole[] = ['user', 'auditor', 'admin']
const AUDIT_ROLES: readonly SubjectRole[] = ['auditor', 'admin']
const ADMIN_ONLY: readonly SubjectRole[] = ['admin']

export const navigationItems: readonly NavigationItem[] = [
  { id: 'overview', path: '/', label: '治理总览', description: '查看安全评分、30 日趋势、风险待办与最近任务。', allowedRoles: ALL_ROLES },
  { id: 'tasks', path: '/tasks', label: '扫描任务', description: '创建、筛选并跟踪受治理的扫描任务。', allowedRoles: ALL_ROLES },
  { id: 'reports', path: '/reports', label: '安全报告', description: '查看不可变报告快照并执行受审计 PDF 导出。', allowedRoles: ALL_ROLES },
  { id: 'models', path: '/models', label: '凭证配置', description: '查看受治理模型，并按角色管理加密凭据。', allowedRoles: ALL_ROLES },
  { id: 'knowledge', path: '/knowledge', label: '规则与知识库', description: '浏览六类规则与知识资产，并按角色执行受审计治理。', allowedRoles: ALL_ROLES },
  { id: 'users', path: '/admin/users', label: '用户管理', description: '创建、启停、改角色或受控发起站外密码重置。', allowedRoles: ADMIN_ONLY },
  { id: 'audit', path: '/admin/audit', label: '审计日志', description: '按权限读取服务端分页的治理审计摘要。', allowedRoles: AUDIT_ROLES },
  { id: 'brand', path: '/admin/brand', label: '品牌设置', description: '维护经浏览器与服务端双重校验的企业品牌。', allowedRoles: ADMIN_ONLY },
  { id: 'system', path: '/system', label: '系统信息', description: '读取同步状态；仅管理员可发起受控数据同步。', allowedRoles: AUDIT_ROLES },
]

const secondaryNavigation: Readonly<Record<string, readonly SecondaryNavigationItem[]>> = {
  tasks: [
    { id: 'mcp-scan', path: '/tasks/new?scan=mcp', label: 'MCP 扫描', allowedRoles: ALL_ROLES },
    { id: 'skills-scan', path: '/tasks/new?scan=skills', label: 'Skills 扫描', allowedRoles: ALL_ROLES },
    { id: 'ai-infra-scan', path: '/tasks/ai-infra', label: 'AI 基础设施扫描', allowedRoles: ALL_ROLES },
    { id: 'agent-workflow-scan', path: '/tasks/new?scan=agent-workflow', label: 'Agent 工作流扫描', allowedRoles: ALL_ROLES },
  ],
  credentials: [
    { id: 'model-config', path: '/models', label: '模型配置', allowedRoles: ALL_ROLES },
    { id: 'agent-config', path: '/knowledge/agents', label: '智能体配置', allowedRoles: ALL_ROLES },
  ],
}

export function visibleNavigationFor(role: SubjectRole): readonly NavigationItem[] {
  return navigationItems.filter(({ allowedRoles }) => allowedRoles.includes(role))
}

export function secondaryNavigationFor(parentID: string, role: SubjectRole): readonly SecondaryNavigationItem[] {
  return secondaryNavigation[parentID]?.filter(({ allowedRoles }) => allowedRoles.includes(role)) ?? []
}
