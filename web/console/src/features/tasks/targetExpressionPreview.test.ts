/**
 * 功能：验证 AI 基础设施扫描目标表达式的浏览器预览语义。
 * 实现：覆盖逐行展开、稳定去重、容量上限和易误用格式，确保仅作引导的预览与服务端规则一致。
 * 输入：任务创建页输入框中的原始多行文本。
 * 输出：目标列表、展开数量或可读的本地校验错误。
 * 依赖：Vitest 与纯 TypeScript 预览器。
 */
import { describe, expect, it } from 'vitest'

import { previewTargetExpressions } from './targetExpressionPreview'

const sevenLineRanges = [
  '104.147.75.1-104.147.75.10',
  '104.147.75.12-104.147.75.13',
  '104.147.75.15-104.147.75.30',
  '104.147.75.32-104.147.75.34',
  '104.147.75.37-104.147.75.39',
  '104.147.75.41-104.147.75.102',
  '104.147.75.104-104.147.75.107',
].join('\n')

describe('previewTargetExpressions', () => {
  it('保留单个 URL 与 IPv4 目标', () => {
    const preview = previewTargetExpressions('https://ai.example.com\n192.168.10.2')

    expect(preview).toEqual({ ok: true, count: 2, targets: ['https://ai.example.com', '192.168.10.2'] })
  })

  it('展开 IPv4 CIDR', () => {
    const preview = previewTargetExpressions('192.168.10.0/30')

    expect(preview).toEqual({ ok: true, count: 4, targets: ['192.168.10.0', '192.168.10.1', '192.168.10.2', '192.168.10.3'] })
  })

  it('展开闭区间 IPv4 范围', () => {
    const preview = previewTargetExpressions('192.168.10.2-192.168.10.10')

    expect(preview).toMatchObject({ ok: true, count: 9 })
    expect(preview.targets).toEqual([
      '192.168.10.2', '192.168.10.3', '192.168.10.4', '192.168.10.5', '192.168.10.6',
      '192.168.10.7', '192.168.10.8', '192.168.10.9', '192.168.10.10',
    ])
  })

  it('拒绝带端口的 IPv4 连字符范围', () => {
    const preview = previewTargetExpressions('192.168.10.2:80-192.168.10.10:80')

    expect(preview).toMatchObject({ ok: false, count: 0, targets: [] })
    expect(preview.ok ? '' : preview.error).toContain('端口')
  })

  it('展开末尾 IPv4 通配符', () => {
    const smallPreview = previewTargetExpressions('22.2.10.*')
    const largePreview = previewTargetExpressions('22.2.*.*')

    expect(smallPreview).toMatchObject({ ok: true, count: 256 })
    expect(smallPreview.targets[0]).toBe('22.2.10.0')
    expect(smallPreview.targets.at(-1)).toBe('22.2.10.255')
    expect(largePreview).toMatchObject({ ok: true, count: 65_536 })
    expect(largePreview.targets[0]).toBe('22.2.0.0')
    expect(largePreview.targets.at(-1)).toBe('22.2.255.255')
  })

  it('跨多行稳定去重且保留首次出现顺序', () => {
    const preview = previewTargetExpressions([
      '192.168.10.2',
      '192.168.10.2-192.168.10.4',
      '192.168.10.3',
    ].join('\n'))

    expect(preview).toEqual({
      ok: true,
      count: 3,
      targets: ['192.168.10.2', '192.168.10.3', '192.168.10.4'],
    })
  })

  it('将用户提供的七行范围展开为 100 个目标', () => {
    const preview = previewTargetExpressions(sevenLineRanges)

    expect(preview).toMatchObject({ ok: true, count: 100 })
    expect(preview.targets[0]).toBe('104.147.75.1')
    expect(preview.targets.at(-1)).toBe('104.147.75.107')
  })

  it('在合并后的唯一目标超过 65,536 个时拒绝输入', () => {
    const expressions = Array.from({ length: 65_537 }, (_, index) => {
      const second = Math.floor(index / 65_536)
      const third = Math.floor((index % 65_536) / 256)
      const fourth = index % 256
      return `10.${second}.${third}.${fourth}`
    })

    const preview = previewTargetExpressions(expressions.join('\n'))

    expect(preview).toMatchObject({ ok: false, count: 0, targets: [] })
    expect(preview.ok ? '' : preview.error).toContain('65,536')
  })

  it.each([
    '22.*.10.*',
    '104.147.75.1\\~104.147.75.10',
    '192.168.10.2-not-an-ip',
    '192.168.10.2 192.168.10.3',
    '2001:db8::1',
  ])('拒绝不支持的目标表达式 %s', (expression) => {
    const preview = previewTargetExpressions(expression)

    expect(preview).toMatchObject({ ok: false, count: 0, targets: [] })
    expect(preview.ok ? '' : preview.error).not.toHaveLength(0)
  })
})
