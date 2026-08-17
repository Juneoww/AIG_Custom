/**
 * 功能：声明企业控制台身份接口的精确浏览器 DTO。
 * 实现：字段与后端 Swagger 合同保持一致，不包含 Cookie 或内部会话数据。
 * 输入：身份 API 的 JSON 响应和表单请求值。
 * 输出：供 API 客户端、会话状态机和页面使用的类型。
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
