/**
 * 功能：为企业控制台准备经过审查的离线字体与发布许可。
 * 实现：在唯一临时树校验固定白名单；IBM 许可证来源只将 CRLF 规范为 LF 后校验，其余资产逐字节校验，再以失败关闭方式替换 Vite publicDir。
 * 输入：仓库内 IBM 资产，以及可由环境变量指定的 Droid 字体和许可源。
 * 输出：仅含固定 fonts 与 licenses 白名单的 .generated 目录。
 * 依赖：Node.js 22 的 fs、path、crypto 与 url 标准库。
 * 用法：pnpm run prepare:fonts
 */
import { createHash, randomUUID } from 'node:crypto'
import { lstat, mkdir, mkdtemp, readFile, rename, rm, stat, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const consoleRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const outputDirectory = resolve(consoleRoot, '.generated')
const droidSource =
  process.env.AIG_DROID_FONT_SOURCE ??
  resolve(consoleRoot, '../../internal/platform/reports/assets/DroidSansFallbackFull.ttf')
const droidLicenseSource =
  process.env.AIG_DROID_LICENSE_SOURCE ??
  resolve(consoleRoot, '../../internal/platform/reports/assets/DROID_FONT_LICENSE.txt')
const apacheLicenseSource =
  process.env.AIG_APACHE_LICENSE_SOURCE ?? resolve(consoleRoot, '../../LICENSE')

const assets = [
  {
    name: 'DroidSansFallbackFull.ttf',
    directory: 'fonts',
    source: droidSource,
    bytes: 4_033_576,
    sha256: '2392015530438bafc48edfc4aee6d9de2387f627a6134d8ab3dfcc99d21c8240',
  },
  {
    name: 'IBMPlexSans-Regular.woff2',
    directory: 'fonts',
    source: resolve(consoleRoot, 'assets/fonts/IBMPlexSans-Regular.woff2'),
    bytes: 63_020,
    sha256: 'ba711a3085ff9f27440b6b9c4550cfc47c97bf36591d5da958b975bb3add8c1a',
  },
  {
    name: 'IBMPlexSans-SemiBold.woff2',
    directory: 'fonts',
    source: resolve(consoleRoot, 'assets/fonts/IBMPlexSans-SemiBold.woff2'),
    bytes: 67_060,
    sha256: 'f78048030eab62e860efa39a0df79e2e5581bf122eb95b9bc42c0b8a4988d205',
  },
  {
    name: 'IBM_PLEX_LICENSE.txt',
    directory: 'licenses',
    source: resolve(consoleRoot, 'assets/fonts/IBM_PLEX_LICENSE.txt'),
    normalizeLineEndings: true,
    bytes: 4_362,
    sha256: 'd741e57d5f865e294df801f96b7b5161a88b211df65887e4358d271c9fc5fb4f',
  },
  {
    name: 'DROID_FONT_LICENSE.txt',
    directory: 'licenses',
    source: droidLicenseSource,
    bytes: 1_029,
    sha256: '0c56af5bd38c399e388c0a342dc756cbeddf2d83db960aca2cb6857b2ec5dc04',
  },
  {
    name: 'APACHE-2.0.txt',
    directory: 'licenses',
    source: apacheLicenseSource,
    bytes: 11_564,
    sha256: '848823f8eef2c36ab75e06cc15c1e7f424fb1c2144d2b21ae19eff9668c2c042',
  },
]

function normalizeAssetContent(content, asset) {
  if (!asset.normalizeLineEndings) return content
  const normalized = Buffer.allocUnsafe(content.length)
  let destination = 0
  for (let source = 0; source < content.length; source += 1) {
    if (content[source] === 0x0d && content[source + 1] === 0x0a) {
      normalized[destination] = 0x0a
      destination += 1
      source += 1
      continue
    }
    normalized[destination] = content[source]
    destination += 1
  }
  return normalized.subarray(0, destination)
}

async function verifyAsset(path, asset, requirePlainFile = false, normalizeLineEndings = false) {
  try {
    const metadata = requirePlainFile ? await lstat(path) : await stat(path)
    const rawContent = await readFile(path)
    const content = normalizeLineEndings ? normalizeAssetContent(rawContent, asset) : rawContent
    const hash = createHash('sha256').update(content).digest('hex')
    if (
      content.length !== asset.bytes ||
      hash !== asset.sha256 ||
      (requirePlainFile && (!metadata.isFile() || metadata.isSymbolicLink()))
    ) {
      throw new Error('mismatch')
    }
    return content
  } catch {
    throw new Error(`字体资产校验失败：${asset.name}`)
  }
}

async function existingOutputIsSafe() {
  try {
    const metadata = await lstat(outputDirectory)
    if (!metadata.isDirectory() || metadata.isSymbolicLink()) {
      throw new Error('生成目录必须是普通目录')
    }
    return true
  } catch (error) {
    if (error && typeof error === 'object' && 'code' in error && error.code === 'ENOENT') {
      return false
    }
    throw error
  }
}

async function populateTemporaryTree(temporaryDirectory) {
  await mkdir(resolve(temporaryDirectory, 'fonts'))
  await mkdir(resolve(temporaryDirectory, 'licenses'))

  for (const asset of assets) {
    const content = await verifyAsset(asset.source, asset, false, asset.normalizeLineEndings === true)
    const destination = resolve(temporaryDirectory, asset.directory, asset.name)
    await writeFile(destination, content, { flag: 'wx' })
    await verifyAsset(destination, asset, true)
  }
}

async function publishGeneratedTree() {
  const hasExistingOutput = await existingOutputIsSafe()
  const temporaryDirectory = await mkdtemp(resolve(consoleRoot, '.generated.tmp-'))
  let temporaryExists = true

  try {
    await populateTemporaryTree(temporaryDirectory)

    if (!hasExistingOutput) {
      await rename(temporaryDirectory, outputDirectory)
      temporaryExists = false
      return
    }

    const backupDirectory = resolve(consoleRoot, `.generated.backup-${randomUUID()}`)
    await rename(outputDirectory, backupDirectory)
    try {
      await rename(temporaryDirectory, outputDirectory)
      temporaryExists = false
    } catch {
      try {
        await rename(backupDirectory, outputDirectory)
      } catch {
        // 原输出仍保留在唯一备份目录；不删除可能有效的发布资产。
      }
      throw new Error('无法安全替换生成目录')
    }
    await rm(backupDirectory, { recursive: true, force: true })
  } finally {
    if (temporaryExists) {
      await rm(temporaryDirectory, { recursive: true, force: true })
    }
  }
}

await publishGeneratedTree()
