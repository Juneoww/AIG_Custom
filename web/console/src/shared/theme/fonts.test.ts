/**
 * 功能：锁定控制台离线字体来源、许可、生成结果与全局可访问性样式。
 * 实现：读取本地资产并校验字节数、SHA-256、许可全文标记和 CSS 安全约束。
 * 输入：固定 Droid 源字体、IBM Plex 资产、生成字体目录与 global.css。
 * 输出：字体供应链、无公网 URL、焦点和减少动效回归断言。
 * 依赖：Node.js 文件系统、crypto 与 Vitest。
 */
import { execFile } from 'node:child_process'
import { createHash } from 'node:crypto'
import { copyFile, mkdir, mkdtemp, readFile, readdir, rm, stat, symlink, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { promisify } from 'node:util'

import { describe, expect, it } from 'vitest'

const root = process.cwd()
const droidSource =
  process.env.AIG_DROID_FONT_SOURCE ??
  resolve(root, '../../internal/platform/reports/assets/DroidSansFallbackFull.ttf')
const droidLicenseSource =
  process.env.AIG_DROID_LICENSE_SOURCE ??
  resolve(root, '../../internal/platform/reports/assets/DROID_FONT_LICENSE.txt')
const apacheLicenseSource = process.env.AIG_APACHE_LICENSE_SOURCE ?? resolve(root, '../../LICENSE')
const runFile = promisify(execFile)

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

const expectedLicenses = [
  {
    name: 'IBM_PLEX_LICENSE.txt',
    source: resolve(root, 'assets/fonts/IBM_PLEX_LICENSE.txt'),
    bytes: 4_362,
    sha256: 'd741e57d5f865e294df801f96b7b5161a88b211df65887e4358d271c9fc5fb4f',
  },
  {
    name: 'DROID_FONT_LICENSE.txt',
    source: droidLicenseSource,
    bytes: 1_029,
    sha256: '0c56af5bd38c399e388c0a342dc756cbeddf2d83db960aca2cb6857b2ec5dc04',
  },
  {
    name: 'APACHE-2.0.txt',
    source: apacheLicenseSource,
    bytes: 11_564,
    sha256: '848823f8eef2c36ab75e06cc15c1e7f424fb1c2144d2b21ae19eff9668c2c042',
  },
] as const

async function runPrepare(cwd = root): Promise<void> {
  await runFile(process.execPath, [resolve(cwd, 'scripts/prepare-fonts.mjs')], {
    cwd,
    env: {
      ...process.env,
      AIG_DROID_FONT_SOURCE: droidSource,
      AIG_DROID_LICENSE_SOURCE: droidLicenseSource,
      AIG_APACHE_LICENSE_SOURCE: apacheLicenseSource,
    },
  })
}

async function digest(path: string): Promise<string> {
  return digestContent(await readFile(path))
}

function digestContent(content: Uint8Array): string {
  return createHash('sha256').update(content).digest('hex')
}

function normalizeIbmPlexLicenseLineEndings(content: Buffer): Buffer {
  return Buffer.from(content.toString('utf8').replace(/\r\n/g, '\n'), 'utf8')
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
    const licenseBytes = normalizeIbmPlexLicenseLineEndings(await readFile(licensePath))
    const license = licenseBytes.toString('utf8')

    expect(licenseBytes.length).toBe(4_362)
    expect(digestContent(licenseBytes)).toBe(
      'd741e57d5f865e294df801f96b7b5161a88b211df65887e4358d271c9fc5fb4f',
    )
    expect(license.length).toBeGreaterThan(4_000)
    expect(license).toContain('Copyright © 2017 IBM Corp. with Reserved Font Name "Plex"')
    expect(license).toContain('SIL OPEN FONT LICENSE Version 1.1')
    expect(license).toContain('THE FONT SOFTWARE IS PROVIDED "AS IS"')
    expect(license).not.toMatch(/[ \t]+\r?$/m)
  })

  it.each(expectedLicenses)('pins the published $name bytes and SHA-256', async (license) => {
    const source = await readFile(license.source)
    const canonicalSource = license.name === 'IBM_PLEX_LICENSE.txt'
      ? normalizeIbmPlexLicenseLineEndings(source)
      : source
    expect(canonicalSource.length).toBe(license.bytes)
    expect(digestContent(canonicalSource)).toBe(license.sha256)
    await expect(stat(resolve(root, '.generated/licenses', license.name))).resolves.toMatchObject({
      size: license.bytes,
    })
    await expect(digest(resolve(root, '.generated/licenses', license.name))).resolves.toBe(
      license.sha256,
    )
  })

  it('publishes complete Droid attribution and Apache 2.0 text', async () => {
    const droidLicense = await readFile(
      resolve(root, '.generated/licenses/DROID_FONT_LICENSE.txt'),
      'utf8',
    )
    const apacheLicense = await readFile(resolve(root, '.generated/licenses/APACHE-2.0.txt'), 'utf8')

    expect(droidLicense).toContain('Copyright 2009 The Android Open Source Project')
    expect(droidLicense).toContain('License: Apache License, Version 2.0')
    expect(apacheLicense).toContain('Apache License')
    expect(apacheLicense).toContain('END OF TERMS AND CONDITIONS')
  })

  it('normalizes a CRLF IBM license source before strict publication', async () => {
    const fixtureRoot = await mkdtemp(join(tmpdir(), 'aig-font-fixture-'))

    try {
      await mkdir(resolve(fixtureRoot, 'scripts'), { recursive: true })
      await mkdir(resolve(fixtureRoot, 'assets/fonts'), { recursive: true })
      await copyFile(resolve(root, 'scripts/prepare-fonts.mjs'), resolve(fixtureRoot, 'scripts/prepare-fonts.mjs'))
      for (const asset of expectedAssets.filter((asset) => asset.name.startsWith('IBMPlex'))) {
        await copyFile(asset.source, resolve(fixtureRoot, 'assets/fonts', asset.name))
      }

      const canonicalLicense = normalizeIbmPlexLicenseLineEndings(
        await readFile(resolve(root, 'assets/fonts/IBM_PLEX_LICENSE.txt')),
      )
      const crlfLicense = Buffer.from(canonicalLicense.toString('utf8').replace(/\n/g, '\r\n'), 'utf8')
      await writeFile(resolve(fixtureRoot, 'assets/fonts/IBM_PLEX_LICENSE.txt'), crlfLicense)

      expect(crlfLicense).not.toEqual(canonicalLicense)
      await expect(runPrepare(fixtureRoot)).resolves.toBeUndefined()

      const generatedLicense = await readFile(
        resolve(fixtureRoot, '.generated/licenses/IBM_PLEX_LICENSE.txt'),
      )
      expect(generatedLicense).toEqual(canonicalLicense)
      expect(generatedLicense.length).toBe(4_362)
      expect(createHash('sha256').update(generatedLicense).digest('hex')).toBe(
        'd741e57d5f865e294df801f96b7b5161a88b211df65887e4358d271c9fc5fb4f',
      )

      const tamperedLicense = Buffer.from(crlfLicense)
      const copyrightIndex = tamperedLicense.indexOf(Buffer.from('Copyright', 'utf8'))
      expect(copyrightIndex).toBeGreaterThanOrEqual(0)
      tamperedLicense[copyrightIndex] = 'X'.charCodeAt(0)
      await writeFile(resolve(fixtureRoot, 'assets/fonts/IBM_PLEX_LICENSE.txt'), tamperedLicense)

      await expect(runPrepare(fixtureRoot)).rejects.toThrow('字体资产校验失败：IBM_PLEX_LICENSE.txt')
      await expect(readFile(resolve(fixtureRoot, '.generated/licenses/IBM_PLEX_LICENSE.txt'))).resolves.toEqual(
        canonicalLicense,
      )
    } finally {
      await rm(fixtureRoot, { recursive: true, force: true })
    }
  })

  it('replaces stale generated content with the fixed publication whitelist', async () => {
    await mkdir(resolve(root, '.generated/stale/nested'), { recursive: true })
    await writeFile(resolve(root, '.generated/stale/nested/sentinel.txt'), 'must disappear')

    await runPrepare()

    await expect(stat(resolve(root, '.generated/stale'))).rejects.toMatchObject({ code: 'ENOENT' })
    expect((await readdir(resolve(root, '.generated'))).sort()).toEqual(['fonts', 'licenses'])
    expect((await readdir(resolve(root, '.generated/fonts'))).sort()).toEqual(
      expectedAssets.map((asset) => asset.name).sort(),
    )
    expect((await readdir(resolve(root, '.generated/licenses'))).sort()).toEqual(
      expectedLicenses.map((license) => license.name).sort(),
    )
  })

  it('rejects a linked default output without touching its external target', async () => {
    const fixtureRoot = await mkdtemp(join(tmpdir(), 'aig-font-fixture-'))
    const externalTarget = await mkdtemp(join(tmpdir(), 'aig-font-sentinel-'))
    const sentinel = resolve(externalTarget, 'sentinel.txt')

    try {
      await mkdir(resolve(fixtureRoot, 'scripts'), { recursive: true })
      await mkdir(resolve(fixtureRoot, 'assets/fonts'), { recursive: true })
      await copyFile(resolve(root, 'scripts/prepare-fonts.mjs'), resolve(fixtureRoot, 'scripts/prepare-fonts.mjs'))
      for (const asset of expectedAssets.filter((asset) => asset.name.startsWith('IBMPlex'))) {
        await copyFile(asset.source, resolve(fixtureRoot, 'assets/fonts', asset.name))
      }
      await copyFile(
        resolve(root, 'assets/fonts/IBM_PLEX_LICENSE.txt'),
        resolve(fixtureRoot, 'assets/fonts/IBM_PLEX_LICENSE.txt'),
      )
      await writeFile(sentinel, 'outside must remain unchanged')
      // Windows 用同样受 lstat 检测的目录联接，避免测试依赖管理员符号链接权限。
      await symlink(externalTarget, resolve(fixtureRoot, '.generated'), process.platform === 'win32' ? 'junction' : 'dir')

      await expect(runPrepare(fixtureRoot)).rejects.toThrow()
      await expect(readFile(sentinel, 'utf8')).resolves.toBe('outside must remain unchanged')
      await expect(readdir(externalTarget)).resolves.toEqual(['sentinel.txt'])
    } finally {
      await rm(fixtureRoot, { recursive: true, force: true })
      await rm(externalTarget, { recursive: true, force: true })
      await runPrepare()
    }
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
