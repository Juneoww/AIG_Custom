# 功能:
#   校验 docs 目录只保留人读文档，并验证目标文档结构和旧路径引用已经完成迁移。
# 实现:
#   从脚本位置推导仓库根目录，以 Git 返回的已跟踪和未忽略文件为边界，检查 docs 文件
#   类型、目标必需文件、当前导航入口，以及代码、配置和 Markdown 实际链接目标中的旧路径。
# 输入:
#   Git 已跟踪或未忽略的未跟踪文件，以及仓库中的 docs 和 internal/apidocs 目标文件。
# 输出:
#   成功时输出通过信息；失败时汇总违规文件、缺失文件和旧路径引用并以非零状态退出。
# 依赖:
#   PowerShell 5.1 或更高版本、Git；无需网络或第三方模块。
# 用法:
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/check-docs-layout.ps1

$ErrorActionPreference = 'Stop'

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$docsRoot = Join-Path $repositoryRoot 'docs'
$issues = New-Object 'System.Collections.Generic.List[string]'

$repositoryFiles = @(& git -c core.quotepath=false -C $repositoryRoot ls-files -co --exclude-standard)
if ($LASTEXITCODE -ne 0) {
    throw '无法通过 git ls-files 获取仓库文件列表。'
}
$repositoryFiles = @($repositoryFiles | ForEach-Object { $_.Replace('\', '/') })

# docs 只允许人读 Markdown；任何其他已跟踪或未忽略文件都必须移出文档目录。
foreach ($relativePath in ($repositoryFiles | Where-Object { $_.StartsWith('docs/') })) {
    if ([System.IO.Path]::GetExtension($relativePath) -ne '.md') {
        $issues.Add("docs 目录包含非 Markdown 文件: $relativePath")
    }
}

$requiredPaths = @(
    'docs/README.md',
    'docs/product/prd.md',
    'docs/product/features.md',
    'docs/project/status.md',
    'docs/project/plans/documentation.md',
    'docs/architecture/evolution.md',
    'docs/architecture/enterprise-console.md',
    'docs/architecture/documentation.md',
    'docs/api/reference.md',
    'docs/api/reference.en.md',
    'docs/api/data-sync.md',
    'docs/archive/2026-08-enterprise-platform/README.md',
    'docs/archive/legacy-api/README.md',
    'docs/archive/legacy-api/reference.ja.md',
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

# 判断字符前是否有奇数个反斜杠；奇数表示 Markdown 转义。
function Test-IsEscapedMarkdownCharacter {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Line,
        [Parameter(Mandatory = $true)][int]$Position
    )

    $backslashCount = 0
    $positionBeforeCharacter = $Position - 1
    while ($positionBeforeCharacter -ge 0 -and $Line[$positionBeforeCharacter] -eq '\') {
        $backslashCount++
        $positionBeforeCharacter--
    }
    return $backslashCount % 2 -eq 1
}

# 只有后续存在同长度、未转义的 closing delimiter 时，反引号才构成代码 span。
function Test-HasClosingCodeSpanDelimiter {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string[]]$Lines,
        [Parameter(Mandatory = $true)][int]$StartLineIndex,
        [Parameter(Mandatory = $true)][int]$StartCharacterIndex,
        [Parameter(Mandatory = $true)][int]$DelimiterLength
    )

    for ($lineIndex = $StartLineIndex; $lineIndex -lt $Lines.Count; $lineIndex++) {
        $currentLine = $Lines[$lineIndex]
        if ($lineIndex -gt $StartLineIndex -and [string]::IsNullOrWhiteSpace($currentLine)) {
            return $false
        }
        $position = if ($lineIndex -eq $StartLineIndex) { $StartCharacterIndex } else { 0 }
        while ($position -lt $currentLine.Length) {
            if ([int][char]$currentLine[$position] -ne 96) {
                $position++
                continue
            }
            $runStart = $position
            while ($position -lt $currentLine.Length -and [int][char]$currentLine[$position] -eq 96) {
                $position++
            }
            if ($position - $runStart -eq $DelimiterLength -and -not (Test-IsEscapedMarkdownCharacter $currentLine $runStart)) {
                return $true
            }
        }
    }
    return $false
}

# 从单行中剔除跨行 HTML 注释和已确认的 backtick 代码 span，防止隐藏示例充当导航入口。
function Get-VisibleMarkdownLine {
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Line,
        [Parameter(Mandatory = $true)][AllowEmptyString()][string[]]$AllLines,
        [Parameter(Mandatory = $true)][int]$LineIndex,
        [Parameter(Mandatory = $true)][ref]$InsideHtmlComment,
        [Parameter(Mandatory = $true)][ref]$ActiveCodeSpanLength
    )

    $result = New-Object System.Text.StringBuilder
    $position = 0
    while ($position -lt $Line.Length) {
        if ($ActiveCodeSpanLength.Value -gt 0) {
            if ([int][char]$Line[$position] -ne 96) {
                $position++
                continue
            }
            $runStart = $position
            while ($position -lt $Line.Length -and [int][char]$Line[$position] -eq 96) {
                $position++
            }
            if ($position - $runStart -eq $ActiveCodeSpanLength.Value -and -not (Test-IsEscapedMarkdownCharacter $Line $runStart)) {
                $ActiveCodeSpanLength.Value = 0
            }
            continue
        }

        if ($InsideHtmlComment.Value) {
            if ($position + 2 -lt $Line.Length -and $Line.Substring($position, 3) -eq '-->') {
                $InsideHtmlComment.Value = $false
                $position += 3
            }
            else {
                $position++
            }
            continue
        }

        if ($position + 3 -lt $Line.Length -and $Line.Substring($position, 4) -eq '<!--') {
            $InsideHtmlComment.Value = $true
            $position += 4
            continue
        }
        if ([int][char]$Line[$position] -eq 96) {
            $runStart = $position
            while ($position -lt $Line.Length -and [int][char]$Line[$position] -eq 96) {
                $position++
            }
            $delimiterLength = $position - $runStart
            if (-not (Test-IsEscapedMarkdownCharacter $Line $runStart) -and (Test-HasClosingCodeSpanDelimiter $AllLines $LineIndex $position $delimiterLength)) {
                $ActiveCodeSpanLength.Value = $delimiterLength
                continue
            }
            [void]$result.Append($Line.Substring($runStart, $delimiterLength))
            continue
        }

        [void]$result.Append($Line[$position])
        $position++
    }
    return $result.ToString()
}

# 当前文档总入口必须以完整、可见的 Markdown 链接公开平台功能清单；代码块和图片不构成导航入口。
function Test-FeatureCatalogNavigationLink {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string[]]$Lines)

    $activeFenceCharacter = $null
    $activeFenceLength = 0
    $insideHtmlComment = $false
    $activeCodeSpanLength = 0
    $catalogLinkPattern = '(?<![!\\])(?:\\\\)*\[(?<label>[^\]\r\n]*\S[^\]\r\n]*)\]\(\s*(?<target><[^>\r\n]+>|[^\s\)]+)(?:\s+(?:"[^"]*"|''[^'']*''|\([^\)]*\)))?\s*\)'

    for ($lineIndex = 0; $lineIndex -lt $Lines.Count; $lineIndex++) {
        $sourceLine = $Lines[$lineIndex]
        if ($null -ne $activeFenceCharacter) {
            $closingFencePattern = '^(?: {0,3})' + [regex]::Escape($activeFenceCharacter) + '{' + $activeFenceLength + ',}[ \t]*$'
            if ($sourceLine -match $closingFencePattern) {
                $activeFenceCharacter = $null
                $activeFenceLength = 0
            }
            continue
        }

        $openingFence = [regex]::Match($sourceLine, '^(?: {0,3})(?<marker>`{3,}|~{3,}).*$')
        if ($openingFence.Success) {
            $marker = $openingFence.Groups['marker'].Value
            $activeFenceCharacter = $marker.Substring(0, 1)
            $activeFenceLength = $marker.Length
            continue
        }
        if ($sourceLine -match '^(?: {4}|\t| {1,3}\t)') {
            continue
        }

        $visibleLine = Get-VisibleMarkdownLine $sourceLine $Lines $lineIndex ([ref]$insideHtmlComment) ([ref]$activeCodeSpanLength)
        $links = [regex]::Matches($visibleLine, $catalogLinkPattern)
        foreach ($link in $links) {
            $target = $link.Groups['target'].Value.Trim('<', '>')
            if ($target -eq 'product/features.md') {
                return $true
            }
        }
    }
    return $false
}

$docsReadmePath = Join-Path $repositoryRoot 'docs/README.md'
if (Test-Path -LiteralPath $docsReadmePath -PathType Leaf) {
    $docsReadmeLines = [System.IO.File]::ReadAllLines($docsReadmePath, [System.Text.Encoding]::UTF8)
    if (-not (Test-FeatureCatalogNavigationLink $docsReadmeLines)) {
        $issues.Add('docs/README.md 缺少平台功能清单入口')
    }
}

# 代码和配置按正文扫描；脚本本身包含检查模式，因此从扫描输入中排除。
$legacyContentPattern = '(?i)(?:docs[\\/](?:prd\.md|architecture_evolution\.md|api_data_update\.md|superpowers[\\/]|swagger\.(?:json|ya?ml))|github\.com[\\/]Juneoww[\\/]AIG_Custom[\\/]docs|(?:\.\.?[\\/])api(?:_(?:zh|ja))?\.md)'
$scriptRelativePath = $PSCommandPath.Substring($repositoryRoot.Length + 1).Replace('\', '/')
$contentFiles = $repositoryFiles | Where-Object {
    $_ -ne $scriptRelativePath -and [System.IO.Path]::GetExtension($_) -in @('.go', '.ps1', '.yml', '.yaml')
}
foreach ($relativePath in $contentFiles) {
    $filePath = Join-Path $repositoryRoot $relativePath
    $lineNumber = 0
    foreach ($line in [System.IO.File]::ReadAllLines($filePath)) {
        $lineNumber++
        if ($line -match $legacyContentPattern) {
            $issues.Add("仍引用旧路径: ${relativePath}:$lineNumber")
        }
    }
}

# Markdown 只检查 ]( 后的实际链接目标。治理文档中的反引号迁移表、命令，以及归档正文中的
# 历史命令可以保留；若这些文档把旧路径写成链接，仍会在此处被报告。
$legacyLinkPattern = '(?i)(?:docs[\\/](?:prd\.md|architecture_evolution\.md|api_data_update\.md|api[\\/]reference\.ja\.md|superpowers[\\/]|swagger\.(?:json|ya?ml))|github\.com[\\/]Juneoww[\\/]AIG_Custom[\\/]docs|(?:^|[\\/])api(?:_(?:zh|ja))?\.md(?:$|[?#]))'
$markdownFiles = $repositoryFiles | Where-Object {
    # 归档历史正文保留当时的旧链接；归档 README 属于当前导航，仍必须检查。
    $isMarkdown = [System.IO.Path]::GetExtension($_) -eq '.md'
    $isArchiveHistory = $_.StartsWith('docs/archive/') -and [System.IO.Path]::GetFileName($_) -ne 'README.md'
    $isMarkdown -and -not $isArchiveHistory
}
foreach ($relativePath in $markdownFiles) {
    $filePath = Join-Path $repositoryRoot $relativePath
    $lineNumber = 0
    foreach ($line in [System.IO.File]::ReadAllLines($filePath)) {
        $lineNumber++
        $links = [regex]::Matches($line, '\]\(\s*(?<target><[^>]+>|[^\s\)]+)')
        foreach ($link in $links) {
            $target = $link.Groups['target'].Value.Trim('<', '>')
            if ($target -match $legacyLinkPattern) {
                $issues.Add("Markdown 链接仍指向旧路径: ${relativePath}:$lineNumber -> $target")
            }
        }
    }
}

if ($issues.Count -gt 0) {
    throw ("文档布局检查失败：`n- " + ($issues -join "`n- "))
}

Write-Host '文档布局检查通过。'
