<#
功能：仅使用固定 Go Docker 镜像执行指定 Go 包的测试。
实现：挂载项目源码和命名 Go 模块/构建缓存，在容器中运行 go test；不回退到宿主 Go。
输入：Package 为 Go 包路径；Run 为可选的 go test -run 正则表达式。
输出：Docker 中 go test 的原始退出码和输出。
依赖：Docker Desktop，以及可拉取的 golang:1.23.2-alpine 镜像。
用法：.\scripts\docker-go-test.ps1 ./pkg/database -Run 'TestMigration'
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Package,
    [string]$Run
)

$repositoryRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$dockerArguments = @(
    "run", "--rm",
    "--mount", "type=bind,source=$repositoryRoot,target=/workspace",
    "--mount", "type=volume,source=aig-go-mod-cache,target=/go/pkg/mod",
    "--mount", "type=volume,source=aig-go-build-cache,target=/root/.cache/go-build",
    "-w", "/workspace",
    "golang:1.23.2-alpine",
    "go", "test", $Package, "-count=1"
)

if ($Run) {
    $dockerArguments += "-run"
    $dockerArguments += $Run
}

& docker @dockerArguments
exit $LASTEXITCODE
