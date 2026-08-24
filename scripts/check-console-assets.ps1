# Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
#
# 功能：校验 Go 嵌入的企业控制台静态资源清单，拒绝旧版 A.I.G 资产和外部样式依赖。
# 实现：检查白名单文件、字体哈希、目录边界、文本标识及 HTML/CSS 的远程资源引用。
# 输入：-StaticDirectory 指向待校验的静态目录，默认 common/websocket/static。
# 输出：校验成功时输出目录；发现不合规资源时抛出错误并以非零状态退出。
# 依赖：PowerShell 7+。
# 用法：pwsh ./scripts/check-console-assets.ps1 [-StaticDirectory ./common/websocket/static]

[CmdletBinding()]
param(
    [Parameter()]
    [ValidateNotNullOrEmpty()]
    [string]$StaticDirectory = (Join-Path $PSScriptRoot "..\\common\\websocket\\static")
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Fail-ConsoleAssetCheck {
    param([Parameter(Mandatory)][string]$Message)

    throw "控制台静态资源校验失败：$Message"
}

if (-not (Test-Path -LiteralPath $StaticDirectory -PathType Container)) {
    Fail-ConsoleAssetCheck "目录不存在：$StaticDirectory"
}

$resolvedStaticDirectory = (Resolve-Path -LiteralPath $StaticDirectory).Path
$allItems = Get-ChildItem -LiteralPath $resolvedStaticDirectory -Recurse -Force
foreach ($item in $allItems) {
    if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
        Fail-ConsoleAssetCheck "不允许符号链接或重解析点：$($item.FullName)"
    }
}

$requiredFiles = @(
    "index.html",
    "fonts/DroidSansFallbackFull.ttf",
    "fonts/IBMPlexSans-Regular.woff2",
    "fonts/IBMPlexSans-SemiBold.woff2",
    "licenses/APACHE-2.0.txt",
    "licenses/DROID_FONT_LICENSE.txt",
    "licenses/IBM_PLEX_LICENSE.txt"
)
foreach ($relativePath in $requiredFiles) {
    $absolutePath = Join-Path $resolvedStaticDirectory ($relativePath -replace "/", [System.IO.Path]::DirectorySeparatorChar)
    if (-not (Test-Path -LiteralPath $absolutePath -PathType Leaf)) {
        Fail-ConsoleAssetCheck "缺少必需文件：$relativePath"
    }
}

$expectedHashes = @{
    "fonts/DroidSansFallbackFull.ttf"    = "2392015530438bafc48edfc4aee6d9de2387f627a6134d8ab3dfcc99d21c8240"
    "fonts/IBMPlexSans-Regular.woff2"    = "ba711a3085ff9f27440b6b9c4550cfc47c97bf36591d5da958b975bb3add8c1a"
    "fonts/IBMPlexSans-SemiBold.woff2"   = "f78048030eab62e860efa39a0df79e2e5581bf122eb95b9bc42c0b8a4988d205"
}
foreach ($entry in $expectedHashes.GetEnumerator()) {
    $fontPath = Join-Path $resolvedStaticDirectory ($entry.Key -replace "/", [System.IO.Path]::DirectorySeparatorChar)
    $actualHash = (Get-FileHash -LiteralPath $fontPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualHash -ne $entry.Value) {
        Fail-ConsoleAssetCheck "字体哈希不匹配：$($entry.Key)"
    }
}

$assetDirectory = Join-Path $resolvedStaticDirectory "assets"
$javaScriptAssets = @(Get-ChildItem -LiteralPath $assetDirectory -Filter "index-*.js" -File -ErrorAction SilentlyContinue)
$styleAssets = @(Get-ChildItem -LiteralPath $assetDirectory -Filter "index-*.css" -File -ErrorAction SilentlyContinue)
if ($javaScriptAssets.Count -ne 1 -or $styleAssets.Count -ne 1) {
    Fail-ConsoleAssetCheck "assets/ 必须恰好包含一份哈希 JavaScript 和一份哈希 CSS 入口文件"
}

$expectedFiles = @($requiredFiles) + @("assets/$($javaScriptAssets[0].Name)", "assets/$($styleAssets[0].Name)")
$actualFiles = @(Get-ChildItem -LiteralPath $resolvedStaticDirectory -Recurse -File -Force | ForEach-Object {
    $_.FullName.Substring($resolvedStaticDirectory.Length).TrimStart([char[]]@([System.IO.Path]::DirectorySeparatorChar, [System.IO.Path]::AltDirectorySeparatorChar)).Replace([System.IO.Path]::DirectorySeparatorChar, [char]"/")
})
if ($actualFiles.Count -ne $expectedFiles.Count) {
    Fail-ConsoleAssetCheck "静态目录包含未批准的文件"
}
foreach ($relativePath in $actualFiles) {
    if ($relativePath -notin $expectedFiles) {
        Fail-ConsoleAssetCheck "发现未批准的文件：$relativePath"
    }
}

$topLevelItems = @(Get-ChildItem -LiteralPath $resolvedStaticDirectory -Force)
foreach ($item in $topLevelItems) {
    if ($item.Name -notin @("assets", "fonts", "licenses", "index.html")) {
        Fail-ConsoleAssetCheck "发现未批准的顶层资源：$($item.Name)"
    }
}

$forbiddenPaths = @("aigdocs", "fonts/Tencentsans.ttf")
foreach ($relativePath in $forbiddenPaths) {
    $absolutePath = Join-Path $resolvedStaticDirectory ($relativePath -replace "/", [System.IO.Path]::DirectorySeparatorChar)
    if (Test-Path -LiteralPath $absolutePath) {
        Fail-ConsoleAssetCheck "发现已弃用资源：$relativePath"
    }
}

$indexPath = Join-Path $resolvedStaticDirectory "index.html"
$indexContent = [System.IO.File]::ReadAllText($indexPath)
if ($indexContent -match '(?is)<(?:script|link)\b[^>]+(?:src|href)\s*=\s*[\x22\x27]https?://') {
    Fail-ConsoleAssetCheck "index.html 不得加载远程脚本或样式"
}

$textFiles = @($indexPath) + $styleAssets.FullName + $javaScriptAssets.FullName
$forbiddenText = @("A.I.G", "aigdocs", "TencentSans", "/api/v1/app/")
foreach ($textFile in $textFiles) {
    $content = [System.IO.File]::ReadAllText($textFile)
    foreach ($text in $forbiddenText) {
        if ($content.IndexOf($text, [System.StringComparison]::OrdinalIgnoreCase) -ge 0) {
            Fail-ConsoleAssetCheck "发现已弃用标识 '$text'：$textFile"
        }
    }
    if ($textFile.EndsWith(".css", [System.StringComparison]::OrdinalIgnoreCase) -and $content -match '(?i)(?:url|@import)\s*\(?\s*[\x22\x27]?https?://') {
        Fail-ConsoleAssetCheck "CSS 不得加载远程资源：$textFile"
    }
}

Write-Output "控制台静态资源校验通过：$resolvedStaticDirectory"
