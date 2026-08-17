/**
 * 功能：锁定控制台离线字体来源、许可、生成结果与全局可访问性样式。
 * 实现：读取本地资产并校验字节数、SHA-256、许可全文标记和 CSS 安全约束。
 * 输入：固定 Droid 源字体、IBM Plex 资产、生成字体目录与 global.css。
 * 输出：字体供应链、无公网 URL、焦点和减少动效回归断言。
 * 依赖：Node.js 文件系统、crypto 与 Vitest。
 */
import { createHash } from 'node:crypto'
import { readFile, stat } from 'node:fs/promises'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

const root = process.cwd()
const droidSource =
  process.env.AIG_DROID_FONT_SOURCE ??
  resolve(root, '../../internal/platform/reports/assets/DroidSansFallbackFull.ttf')

const expectedAssets = [
  {
    name: 'DroidSansFallbackFull.ttf',
    source: droidSource,
    bytes: 4_033_576,
    sha256: '2392015530438bafc48edfc4aee6d9de2387f627a6134d8ab3dfcc99d21c8240',
  },
  {
    name: 'IBMPlexSans-Regular.woff2',
    source: resolve(root, 'assets/fonts/IBMPlexSans-Regular.woff2'),
    bytes: 63_020,
    sha256: 'ba711a3085ff9f27440b6b9c4550cfc47c97bf36591d5da958b975bb3add8c1a',
  },
  {
    name: 'IBMPlexSans-SemiBold.woff2',
    source: resolve(root, 'assets/fonts/IBMPlexSans-SemiBold.woff2'),
    bytes: 67_060,
    sha256: 'f78048030eab62e860efa39a0df79e2e5581bf122eb95b9bc42c0b8a4988d205',
  },
] as const

async function digest(path: string): Promise<string> {
  return createHash('sha256').update(await readFile(path)).digest('hex')
}

describe('offline font assets', () => {
  it.each(expectedAssets)('pins $name to its reviewed bytes and SHA-256', async (asset) => {
    await expect(stat(asset.source)).resolves.toMatchObject({ size: asset.bytes })
    await expect(digest(asset.source)).resolves.toBe(asset.sha256)
  })

  it.each(expectedAssets)('prepares $name without changing its bytes', async (asset) => {
    const generated = resolve(root, '.generated/fonts', asset.name)
    await expect(stat(generated)).resolves.toMatchObject({ size: asset.bytes })
    await expect(digest(generated)).resolves.toBe(asset.sha256)
  })

  it('ships the complete IBM Plex OFL attribution', async () => {
    const licensePath = resolve(root, 'assets/fonts/IBM_PLEX_LICENSE.txt')
    const license = await readFile(licensePath, 'utf8')

    await expect(stat(licensePath)).resolves.toMatchObject({ size: 4_362 })
    await expect(digest(licensePath)).resolves.toBe(
      'd741e57d5f865e294df801f96b7b5161a88b211df65887e4358d271c9fc5fb4f',
    )
    expect(license.length).toBeGreaterThan(4_000)
    expect(license).toContain('Copyright © 2017 IBM Corp. with Reserved Font Name "Plex"')
    expect(license).toContain('SIL OPEN FONT LICENSE Version 1.1')
    expect(license).toContain('THE FONT SOFTWARE IS PROVIDED "AS IS"')
    expect(license).not.toMatch(/[ \t]+\r?$/m)
  })
})

describe('global typography and accessibility CSS', () => {
  it('uses local fonts only and keeps the Chinese body font first', async () => {
    const css = await readFile(resolve(root, 'src/shared/styles/global.css'), 'utf8')

    expect(css).not.toMatch(/https?:\/\//i)
    expect(css).not.toContain('Tencent')
    expect(css).toContain("url('/fonts/DroidSansFallbackFull.ttf')")
    expect(css).toMatch(/font-family:\s*'Droid Sans Fallback';[^}]*font-weight:\s*400;/s)
    expect(css).toMatch(/body\s*\{[^}]*font-family:\s*'Droid Sans Fallback'/s)
    expect(css).toMatch(/\.ledger-metric\s*\{[^}]*font-family:\s*'IBM Plex Sans'/s)
  })

  it('provides visible keyboard focus and a reduced-motion fallback', async () => {
    const css = await readFile(resolve(root, 'src/shared/styles/global.css'), 'utf8')

    expect(css).toContain(':focus-visible')
    expect(css).toContain('@media (prefers-reduced-motion: reduce)')
  })
})
