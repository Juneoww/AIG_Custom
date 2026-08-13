# 功能:
#   校验 docs 目录只保留人读文档，并验证目标文档结构和旧路径引用已经完成迁移。
# 实现:
#   从脚本位置推导仓库根目录，检查 docs 中的禁止文件、目标必需文件，以及代码、配置和
#   Markdown 实际链接目标中的旧路径；Markdown 正文中的迁移映射和历史命令不会被当作链接。
# 输入:
#   仓库中的 docs、internal/apidocs，以及 .go、.ps1、.yml、.yaml、.md 文件。
# 输出:
#   成功时输出通过信息；失败时汇总违规文件、缺失文件和旧路径引用并以非零状态退出。
# 依赖:
#   PowerShell 5.1 或更高版本；无需网络或第三方模块。
# 用法:
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-docs-layout.ps1

$ErrorActionPreference = 'Stop'

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$docsRoot = Join-Path $repositoryRoot 'docs'
$issues = New-Object 'System.Collections.Generic.List[string]'

if (-not (Test-Path -LiteralPath $docsRoot -PathType Container)) {
    $issues.Add('缺少 docs 目录。')
} else {
    $forbiddenFiles = Get-ChildItem -LiteralPath $docsRoot -Recurse -File | Where-Object {
        $_.Extension -eq '.go' -or $_.Name -in @('swagger.json', 'swagger.yaml')
    }
    foreach ($file in $forbiddenFiles) {
        $relativePath = $file.FullName.Substring($repositoryRoot.Length + 1).Replace('\', '/')
        $issues.Add("docs 目录仍包含代码或 Swagger 生成物: $relativePath")
    }
}

$requiredPaths = @(
    'docs/README.md',
    'docs/product/prd.md',
    'docs/project/status.md',
    'docs/project/plans/documentation.md',
    'docs/architecture/evolution.md',
    'docs/architecture/enterprise-console.md',
    'docs/architecture/documentation.md',
    'docs/api/reference.md',
    'docs/api/reference.en.md',
    'docs/api/data-sync.md',
    'docs/archive/2026-08-enterprise-platform/README.md',
    'internal/apidocs/docs.go',
    'internal/apidocs/swagger.json',
    'internal/apidocs/swagger.yaml',
    'internal/apidocs/swagger_sync_test.go'
)
foreach ($relativePath in $requiredPaths) {
    if (-not (Test-Path -LiteralPath (Join-Path $repositoryRoot $relativePath) -PathType Leaf)) {
        $issues.Add("缺少目标文件: $relativePath")
    }
}

# 代码和配置按正文扫描；脚本本身包含检查模式，因此从扫描输入中排除。
$legacyContentPattern = '(?i)(?:docs[\\/](?:prd\.md|architecture_evolution\.md|api_data_update\.md|superpowers[\\/]|swagger\.(?:json|ya?ml))|github\.com[\\/]Juneoww[\\/]AIG_Custom[\\/]docs|(?:\.\.?[\\/])api(?:_zh)?\.md)'
$contentFiles = Get-ChildItem -LiteralPath $repositoryRoot -Recurse -File | Where-Object {
    $_.FullName -ne $PSCommandPath -and $_.Extension -in @('.go', '.ps1', '.yml', '.yaml')
}
foreach ($file in $contentFiles) {
    $lineNumber = 0
    foreach ($line in [System.IO.File]::ReadAllLines($file.FullName)) {
        $lineNumber++
        if ($line -match $legacyContentPattern) {
            $relativePath = $file.FullName.Substring($repositoryRoot.Length + 1).Replace('\', '/')
            $issues.Add("仍引用旧路径: ${relativePath}:$lineNumber")
        }
    }
}

# Markdown 只检查 ]( 后的实际链接目标。治理文档中的反引号迁移表、命令，以及归档正文中的
# 历史命令可以保留；若这些文档把旧路径写成链接，仍会在此处被报告。
$legacyLinkPattern = '(?i)(?:docs[\\/](?:prd\.md|architecture_evolution\.md|api_data_update\.md|superpowers[\\/]|swagger\.(?:json|ya?ml))|github\.com[\\/]Juneoww[\\/]AIG_Custom[\\/]docs|(?:^|[\\/])api(?:_zh)?\.md(?:$|[?#]))'
$archiveRoot = Join-Path $docsRoot 'archive'
$markdownFiles = Get-ChildItem -LiteralPath $repositoryRoot -Recurse -File -Filter '*.md' | Where-Object {
    # 归档历史正文保留当时的旧链接；归档 README 属于当前导航，仍必须检查。
    -not ($_.FullName.StartsWith($archiveRoot + [System.IO.Path]::DirectorySeparatorChar) -and $_.Name -ne 'README.md')
}
foreach ($file in $markdownFiles) {
    $lineNumber = 0
    foreach ($line in [System.IO.File]::ReadAllLines($file.FullName)) {
        $lineNumber++
        $links = [regex]::Matches($line, '\]\(\s*(?<target><[^>]+>|[^\s\)]+)')
        foreach ($link in $links) {
            $target = $link.Groups['target'].Value.Trim('<', '>')
            if ($target -match $legacyLinkPattern) {
                $relativePath = $file.FullName.Substring($repositoryRoot.Length + 1).Replace('\', '/')
                $issues.Add("Markdown 链接仍指向旧路径: ${relativePath}:$lineNumber -> $target")
            }
        }
    }
}

if ($issues.Count -gt 0) {
    throw ("文档布局检查失败：`n- " + ($issues -join "`n- "))
}

Write-Host '文档布局检查通过。'
