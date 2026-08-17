/**
 * 功能：声明企业控制台身份、品牌及安全任务摘要的精确浏览器 DTO。
 * 实现：字段与后端 Swagger 合同保持一致，不包含 Cookie、原始结果或内部任务数据。
 * 输入：身份、公共品牌、总览及任务 API 的 JSON 响应和表单请求值。
 * 输出：供 API 客户端、会话、品牌和领域页面使用的安全类型。
 * 依赖：无运行时依赖。
 */
export type SubjectRole = 'admin' | 'user' | 'auditor'

export interface CSRFResponse {
  csrf_token: string
}

export interface LoginRequest {
  username: string
  password: string
}

export interface LoginResponse {
  must_change_password: boolean
}

export interface CurrentSubject {
  id: string
  username: string
  role: SubjectRole
  must_change_password: boolean
}

export interface ChangePasswordRequest {
  old_password: string
  new_password: string
}

export interface PasswordResetConfirmRequest {
  token: string
  temporary_password: string
}

export interface PublicBrandConfig {
  product_name: string
  primary_color: string
  logo_data_url: string
}

export type TaskType =
  | 'mcp_scan'
  | 'ai_infra_scan'
  | 'model_redteam_report'
  | 'agent_scan'
  | 'unknown'

export type TaskStatus =
  | 'pending'
  | 'dispatching'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'dispatch_failed'
  | 'dispatch_unknown'
  | 'cancelled'

export interface TaskSummary {
  id: string
  owner: string
  task_type: TaskType
  status: TaskStatus
  created_at: string
  updated_at: string
}

export interface TaskInputSummary {
  language?: 'zh' | 'en'
  thread?: number
  timeout?: number
  target_count?: number
  num_prompts?: number
}

export interface TaskDetail extends TaskSummary {
  input_summary: TaskInputSummary
}

export interface TaskListResponse {
  items: TaskSummary[]
  total: number
  page: number
  page_size: number
}

export interface TaskCreateRequest {
  task_type: Exclude<TaskType, 'unknown'>
  content: string
  params: {
    model_id?: string
    thread?: number
    timeout?: number
    dataset?: { numPrompts: number }
  }
  attachment_ids?: string[]
  country_iso_code?: 'zh' | 'zh_CN' | 'en'
}

export type AttachmentState = 'uploading' | 'ready'

export interface AttachmentView {
  id: string
  filename: string
  size: number
  state: AttachmentState
  created_at: string
}
