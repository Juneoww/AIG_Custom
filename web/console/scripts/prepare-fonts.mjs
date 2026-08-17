/**
 * 功能：为企业控制台准备经过审查的离线字体资源。
 * 实现：先校验源文件字节数和 SHA-256，再复制到 Vite 的 ignored publicDir。
 * 输入：仓库内 IBM Plex 字体，以及可由 AIG_DROID_FONT_SOURCE 指定的 Droid 字体源。
 * 输出：.generated/fonts 下三份内容不变的字体文件。
 * 依赖：Node.js 22 的 fs、path、crypto 与 url 标准库。
 * 用法：pnpm run prepare:fonts
 */
import { createHash } from 'node:crypto'
import { copyFile, mkdir, readFile, stat } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const consoleRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const outputDirectory = resolve(consoleRoot, '.generated/fonts')
const droidSource =
  process.env.AIG_DROID_FONT_SOURCE ??
  resolve(consoleRoot, '../../internal/platform/reports/assets/DroidSansFallbackFull.ttf')

const assets = [
  {
    name: 'DroidSansFallbackFull.ttf',
    source: droidSource,
    bytes: 4_033_576,
    sha256: '2392015530438bafc48edfc4aee6d9de2387f627a6134d8ab3dfcc99d21c8240',
  },
  {
    name: 'IBMPlexSans-Regular.woff2',
    source: resolve(consoleRoot, 'assets/fonts/IBMPlexSans-Regular.woff2'),
    bytes: 63_020,
    sha256: 'ba711a3085ff9f27440b6b9c4550cfc47c97bf36591d5da958b975bb3add8c1a',
  },
  {
    name: 'IBMPlexSans-SemiBold.woff2',
    source: resolve(consoleRoot, 'assets/fonts/IBMPlexSans-SemiBold.woff2'),
    bytes: 67_060,
    sha256: 'f78048030eab62e860efa39a0df79e2e5581bf122eb95b9bc42c0b8a4988d205',
  },
]

async function verifyAsset(path, asset) {
  const metadata = await stat(path)
  const hash = createHash('sha256').update(await readFile(path)).digest('hex')
  if (metadata.size !== asset.bytes || hash !== asset.sha256) {
    throw new Error(`字体资产校验失败：${asset.name}`)
  }
}

await mkdir(outputDirectory, { recursive: true })
for (const asset of assets) {
  await verifyAsset(asset.source, asset)
  const destination = resolve(outputDirectory, asset.name)
  await copyFile(asset.source, destination)
  await verifyAsset(destination, asset)
}
