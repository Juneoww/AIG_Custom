# 功能:
#   校验发布文档和 NOTICE 是否保留上游项目的必要归属声明。
# 实现:
#   读取固定文件并检查必需的归属文本和上游仓库地址，缺失时以非零状态退出。
# 输入:
#   仓库根目录下的 NOTICE 与 docs/deployment/attribution.md。
# 输出:
#   校验成功信息，或说明缺失文件/文本的错误信息。
# 依赖:
#   PowerShell 5.1 或更高版本。
# 用法:
#   powershell -ExecutionPolicy Bypass -File scripts/check-attribution.ps1

$ErrorActionPreference = 'Stop'

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$requiredText = 'Based on Tencent Zhuque Lab AI-Infra-Guard'
$upstreamUrl = 'https://github.com/Tencent/AI-Infra-Guard'
$files = @(
    'NOTICE',
    'docs/deployment/attribution.md'
)

foreach ($relativePath in $files) {
    $path = Join-Path $repositoryRoot $relativePath
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Missing attribution file: $relativePath"
    }

    $content = Get-Content -LiteralPath $path -Raw
    foreach ($requiredValue in @($requiredText, $upstreamUrl)) {
        if (-not $content.Contains($requiredValue)) {
            throw "Attribution file $relativePath is missing required text: $requiredValue"
        }
    }
}

Write-Host 'Attribution check passed.'
