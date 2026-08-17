/**
 * 功能：集中声明企业控制台导航、占位说明与角色授权矩阵。
 * 实现：以固定顺序的只读元数据同时驱动侧栏可见项和路由授权。
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

const ALL_ROLES: readonly SubjectRole[] = ['user', 'auditor', 'admin']
const AUDIT_ROLES: readonly SubjectRole[] = ['auditor', 'admin']
const ADMIN_ONLY: readonly SubjectRole[] = ['admin']

export const navigationItems: readonly NavigationItem[] = [
  { id: 'overview', path: '/', label: '治理总览', description: '查看安全评分、30 日趋势、风险待办与最近任务。', allowedRoles: ALL_ROLES },
  { id: 'tasks', path: '/tasks', label: '扫描任务', description: '创建、筛选并跟踪受治理的扫描任务。', allowedRoles: ALL_ROLES },
  { id: 'reports', path: '/reports', label: '安全报告', description: '查看不可变报告快照并执行受审计 PDF 导出。', allowedRoles: ALL_ROLES },
  { id: 'models', path: '/models', label: '模型与凭据', description: '模型与凭据管理将在后续任务接入。', allowedRoles: ALL_ROLES },
  { id: 'knowledge', path: '/knowledge', label: '规则与知识库', description: '规则与知识库管理将在后续任务接入。', allowedRoles: ALL_ROLES },
  { id: 'users', path: '/admin/users', label: '用户管理', description: '用户管理将在后续任务接入。', allowedRoles: ADMIN_ONLY },
  { id: 'audit', path: '/admin/audit', label: '审计日志', description: '审计日志将在后续任务接入。', allowedRoles: AUDIT_ROLES },
  { id: 'brand', path: '/admin/brand', label: '品牌设置', description: '品牌设置将在后续任务接入。', allowedRoles: ADMIN_ONLY },
  { id: 'system', path: '/system', label: '系统信息', description: '系统信息将在后续任务接入。', allowedRoles: AUDIT_ROLES },
]

export function visibleNavigationFor(role: SubjectRole): readonly NavigationItem[] {
  return navigationItems.filter(({ allowedRoles }) => allowedRoles.includes(role))
}
