# Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
#
# 功能：在固定 Node 22 容器中构建企业控制台，并原子替换 Go 的嵌入静态资源目录。
# 实现：容器内将 node_modules 与 pnpm store 放在 Linux 匿名卷，执行冻结依赖安装、字体准备、质量检查和 Vite 构建；临时目录校验后以同卷移动替换。
# 输入：当前仓库及本机已运行的 Docker Desktop。
# 输出：受检查的 common/websocket/static 生产资源；失败时保留原有静态目录。
# 依赖：PowerShell 7+、Docker Desktop。
# 用法：pwsh ./scripts/build-console.ps1

[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-WithinDirectory {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$ParentPath
    )

    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $fullParentPath = [System.IO.Path]::GetFullPath($ParentPath).TrimEnd([char[]]@([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar))
    if (-not $fullPath.StartsWith("$fullParentPath$([System.IO.Path]::DirectorySeparatorChar)", [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "拒绝在预期目录之外替换静态资源：$fullPath"
    }
}

$repositoryRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "..")).Path
$consoleDirectory = Join-Path $repositoryRoot "web\\console"
$distDirectory = Join-Path $consoleDirectory "dist"
$websocketDirectory = Join-Path $repositoryRoot "common\\websocket"
$staticDirectory = Join-Path $websocketDirectory "static"
$temporaryStaticDirectory = Join-Path $websocketDirectory ("static.replacement-" + [System.Guid]::NewGuid().ToString("N"))
$backupStaticDirectory = Join-Path $websocketDirectory ("static.backup-" + [System.Guid]::NewGuid().ToString("N"))
$checkScript = Join-Path $PSScriptRoot "check-console-assets.ps1"

foreach ($path in @($consoleDirectory, $websocketDirectory, $checkScript)) {
    if (-not (Test-Path -LiteralPath $path)) {
        throw "缺少构建所需路径：$path"
    }
}
Assert-WithinDirectory -Path $staticDirectory -ParentPath $websocketDirectory
Assert-WithinDirectory -Path $temporaryStaticDirectory -ParentPath $websocketDirectory
Assert-WithinDirectory -Path $backupStaticDirectory -ParentPath $websocketDirectory

$dockerArguments = @(
    "run",
    "--rm",
    "-v", "${repositoryRoot}:/workspace",
    "-v", "/workspace/web/console/node_modules",
    "-v", "/pnpm-store",
    "-w", "/workspace/web/console",
    "-e", "AIG_DROID_FONT_SOURCE=/workspace/internal/platform/reports/assets/DroidSansFallbackFull.ttf",
    "-e", "AIG_DROID_LICENSE_SOURCE=/workspace/internal/platform/reports/assets/DROID_FONT_LICENSE.txt",
    "-e", "AIG_APACHE_LICENSE_SOURCE=/workspace/LICENSE",
    "-e", "CI=true",
    "node:22.18.0-alpine",
    "sh",
    "-ec",
    "corepack enable && pnpm install --frozen-lockfile --store-dir /pnpm-store && pnpm run prepare:fonts && pnpm lint && pnpm typecheck && pnpm test:run && pnpm build"
)

& docker @dockerArguments
if ($LASTEXITCODE -ne 0) {
    throw "企业控制台容器构建失败，未替换嵌入静态资源。"
}
if (-not (Test-Path -LiteralPath $distDirectory -PathType Container)) {
    throw "前端构建未生成 dist 目录：$distDirectory"
}

# Vite 会保留工作树 HTML 模板的 CRLF；在复制前固定为 UTF-8/LF，避免生成物产生仅行尾差异。
$distIndex = Join-Path $distDirectory "index.html"
if (Test-Path -LiteralPath $distIndex -PathType Leaf) {
    $indexContent = [System.IO.File]::ReadAllText($distIndex)
    $normalizedIndexContent = $indexContent -replace "`r`n", "`n"
    if ($normalizedIndexContent -ne $indexContent) {
        [System.IO.File]::WriteAllText($distIndex, $normalizedIndexContent, [System.Text.UTF8Encoding]::new($false))
    }
}

try {
    New-Item -ItemType Directory -Path $temporaryStaticDirectory | Out-Null
    Get-ChildItem -LiteralPath $distDirectory -Force | Copy-Item -Destination $temporaryStaticDirectory -Recurse -Force
    & $checkScript -StaticDirectory $temporaryStaticDirectory

    if (Test-Path -LiteralPath $staticDirectory) {
        Move-Item -LiteralPath $staticDirectory -Destination $backupStaticDirectory
    }
    try {
        Move-Item -LiteralPath $temporaryStaticDirectory -Destination $staticDirectory
        & $checkScript -StaticDirectory $staticDirectory
    } catch {
        if (Test-Path -LiteralPath $staticDirectory) {
            Remove-Item -LiteralPath $staticDirectory -Recurse -Force
        }
        if (Test-Path -LiteralPath $backupStaticDirectory) {
            Move-Item -LiteralPath $backupStaticDirectory -Destination $staticDirectory
        }
        throw
    }

    if (Test-Path -LiteralPath $backupStaticDirectory) {
        Remove-Item -LiteralPath $backupStaticDirectory -Recurse -Force
    }
    Write-Output "企业控制台已构建并原子替换 Go 嵌入静态资源。"
} finally {
    if (Test-Path -LiteralPath $temporaryStaticDirectory) {
        Remove-Item -LiteralPath $temporaryStaticDirectory -Recurse -Force
    }
    if ((Test-Path -LiteralPath $backupStaticDirectory) -and -not (Test-Path -LiteralPath $staticDirectory)) {
        Move-Item -LiteralPath $backupStaticDirectory -Destination $staticDirectory
    }
}
