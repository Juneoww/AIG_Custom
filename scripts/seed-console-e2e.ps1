# 功能:
#   为隔离的企业控制台 E2E compose 创建固定测试账号，不向日志、文件或 HTTP 响应输出密码、Cookie 或会话令牌。
# 实现:
#   严格校验唯一测试 DSN 与平台地址；通过真实 CSRF/Cookie 身份接口完成管理员首次改密，并用受治理的用户 API 创建测试角色。
# 输入:
#   AIG_E2E_DSN、AIG_E2E_PLATFORM_URL，以及 compose 内部的固定测试账号凭据。
# 输出:
#   仅写入 aig_e2e PostgreSQL；成功时输出无敏感数据的完成提示。
# 依赖:
#   PowerShell 7、已迁移的测试平台与仅 compose 内可达的 postgres-e2e。
# 使用:
#   docker compose -f deploy/compose/docker-compose.console-e2e.yml up --build --abort-on-container-exit --exit-code-from console-e2e
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$expectedDsn = 'postgres://aig_e2e:aig_e2e@postgres-e2e:5432/aig_e2e?sslmode=disable'
$expectedPlatformUrl = 'http://platform-e2e:8088'
$dsn = [Environment]::GetEnvironmentVariable('AIG_E2E_DSN')
$platformUrl = [Environment]::GetEnvironmentVariable('AIG_E2E_PLATFORM_URL')
if ($dsn -cne $expectedDsn -or $platformUrl -cne $expectedPlatformUrl) { throw '拒绝向非隔离 E2E 平台写入种子数据。' }

function Get-CsrfToken([Microsoft.PowerShell.Commands.WebRequestSession]$Session) {
  $response = Invoke-RestMethod -Method Get -Uri "$platformUrl/api/v1/auth/csrf" -WebSession $Session
  if ([string]::IsNullOrWhiteSpace($response.csrf_token)) { throw 'E2E CSRF 初始化失败。' }
  return [string]$response.csrf_token
}

function Invoke-SeedJson([Microsoft.PowerShell.Commands.WebRequestSession]$Session, [string]$Method, [string]$Path, [object]$Body) {
  $params = @{ Method = $Method; Uri = "$platformUrl$Path"; WebSession = $Session; Headers = @{ 'X-CSRF-Token' = (Get-CsrfToken $Session) }; ContentType = 'application/json' }
  if ($null -ne $Body) { $params.Body = $Body | ConvertTo-Json -Depth 4 -Compress }
  return Invoke-RestMethod @params
}

function New-ReadyAccount([string]$Username, [string]$Role, [string]$TemporaryPassword, [string]$ReadyPassword) {
  $adminSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
  Invoke-SeedJson $adminSession 'POST' '/api/v1/auth/login' @{ username = 'e2e-admin'; password = 'e2e-admin-password' } | Out-Null
  Invoke-SeedJson $adminSession 'POST' '/api/v1/platform/admin/users' @{ username = $Username; password = $TemporaryPassword; role = $Role } | Out-Null
  $subjectSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
  Invoke-SeedJson $subjectSession 'POST' '/api/v1/auth/login' @{ username = $Username; password = $TemporaryPassword } | Out-Null
  Invoke-SeedJson $subjectSession 'POST' '/api/v1/auth/change-password' @{ old_password = $TemporaryPassword; new_password = $ReadyPassword } | Out-Null
}

$bootstrapSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
Invoke-SeedJson $bootstrapSession 'POST' '/api/v1/auth/login' @{ username = 'e2e-admin'; password = 'e2e-admin-bootstrap' } | Out-Null
Invoke-SeedJson $bootstrapSession 'POST' '/api/v1/auth/change-password' @{ old_password = 'e2e-admin-bootstrap'; new_password = 'e2e-admin-password' } | Out-Null
New-ReadyAccount 'e2e-user' 'user' 'e2e-user-temporary' 'e2e-user-password'
New-ReadyAccount 'e2e-auditor' 'auditor' 'e2e-auditor-temporary' 'e2e-auditor-password'
$adminSession = [Microsoft.PowerShell.Commands.WebRequestSession]::new()
Invoke-SeedJson $adminSession 'POST' '/api/v1/auth/login' @{ username = 'e2e-admin'; password = 'e2e-admin-password' } | Out-Null
Invoke-SeedJson $adminSession 'POST' '/api/v1/platform/admin/users' @{ username = 'e2e-first-login'; password = 'e2e-first-login-password'; role = 'user' } | Out-Null
Write-Output '企业控制台 E2E 种子已写入隔离测试平台。'
