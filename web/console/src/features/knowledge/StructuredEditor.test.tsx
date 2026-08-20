/**
 * 功能：验证结构化编辑器的原文字节、行号、高亮、错误定位与安全导入。
 * 实现：以真实输入事件和受控 File.arrayBuffer 覆盖 YAML/JSON 与卸载竞态。
 * 输入：编辑文本、危险 JSON、超限/无效 UTF-8 文件和延迟导入。
 * 输出：不自动格式化的原文、固定校验结果和无卸载后写入。
 * 依赖：Testing Library、Vitest 与 StructuredEditor。
 */
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { MAX_KNOWLEDGE_FILE_BYTES } from './api'
import { StructuredEditor } from './components/StructuredEditor'

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
})

afterEach(() => vi.unstubAllGlobals())

describe('StructuredEditor', () => {
  it('保持原文并提供行号与无HTML注入的语法高亮', () => {
    const onChange = vi.fn()
    const original = '# comment\ninfo:\n  id: demo\nenabled: true\n'
    render(<StructuredEditor format="yaml" label="规则原文" value={original} onChange={onChange} />)

    expect(screen.getByRole('textbox', { name: '规则原文' })).toHaveValue(original)
    expect(screen.getByLabelText('编辑器行号')).toHaveTextContent('1234')
    const preview = screen.getByLabelText('YAML 语法高亮预览')
    expect(preview.querySelector('[data-token="comment"]')).toHaveTextContent('# comment')
    expect(preview.querySelector('[data-token="key"]')).toHaveTextContent('info')

    fireEvent.change(screen.getByRole('textbox', { name: '规则原文' }), { target: { value: `${original}note: "<script>"\n` } })
    expect(onChange).toHaveBeenCalledWith(`${original}note: "<script>"\n`)
    expect(preview.querySelector('script')).toBeNull()
  })

  it('定位JSON语法错误并拒绝原型污染和过深结构', async () => {
    const onValidationChange = vi.fn()
    const { rerender } = render(
      <StructuredEditor format="json" label="JSON 原文" value={'{\n  "name":\n}'} onChange={() => undefined} onValidationChange={onValidationChange} />,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(/第 3 行，第 1 列/)

    rerender(<StructuredEditor format="json" label="JSON 原文" value={'{"__proto__":{"polluted":true}}'} onChange={() => undefined} onValidationChange={onValidationChange} />)
    expect(await screen.findByRole('alert')).toHaveTextContent(/危险字段/)

    const deep = `${'{"next":'.repeat(70)}null${'}'.repeat(70)}`
    rerender(<StructuredEditor format="json" label="JSON 原文" value={deep} onChange={() => undefined} onValidationChange={onValidationChange} />)
    expect(await screen.findByRole('alert')).toHaveTextContent(/嵌套层级/)
    expect(onValidationChange).toHaveBeenLastCalledWith(expect.objectContaining({ valid: false }))
  })

  it('JSON超过公共行数上限时不构建行号和高亮节点', async () => {
    const excessiveLines = `[\n${Array.from({ length: 10_000 }, () => '0,').join('\n')}\n0\n]`
    render(<StructuredEditor format="json" label="JSON 原文" value={excessiveLines} onChange={() => undefined} />)

    expect(await screen.findByRole('alert')).toHaveTextContent(/行数超过安全上限/)
    expect(screen.getByLabelText('编辑器行号')).toBeEmptyDOMElement()
    expect(screen.getByLabelText('JSON 语法高亮预览')).toBeEmptyDOMElement()
  })

  it('使用受限YAML语法树拒绝重复键、过深流结构、别名和超量节点', async () => {
    const { rerender } = render(<StructuredEditor format="yaml" label="YAML 原文" value={'name: one\nname: two\n'} onChange={() => undefined} />)
    expect(await screen.findByRole('alert')).toHaveTextContent(/YAML 格式错误：第 2 行/)

    const deepFlow = `value: ${'['.repeat(65)}null${']'.repeat(65)}\n`
    rerender(<StructuredEditor format="yaml" label="YAML 原文" value={deepFlow} onChange={() => undefined} />)
    expect(await screen.findByRole('alert')).toHaveTextContent(/嵌套层级/)

    const aliases = `base: &base value\nitems: [${Array.from({ length: 51 }, () => '*base').join(', ')}]\n`
    rerender(<StructuredEditor format="yaml" label="YAML 原文" value={aliases} onChange={() => undefined} />)
    expect(await screen.findByRole('alert')).toHaveTextContent(/别名数量/)

    const excessiveLines = Array.from({ length: 10_001 }, (_, index) => `key${index}: value`).join('\n')
    rerender(<StructuredEditor format="yaml" label="YAML 原文" value={excessiveLines} onChange={() => undefined} />)
    expect(await screen.findByRole('alert')).toHaveTextContent(/行数超过安全上限/)
    expect(screen.getByLabelText('编辑器行号')).toBeEmptyDOMElement()
    expect(screen.getByLabelText('YAML 语法高亮预览')).toBeEmptyDOMElement()
  })

  it('按UTF-8原字节导入并拒绝超限与无效编码', async () => {
    const onChange = vi.fn()
    render(<StructuredEditor format="yaml" label="YAML 原文" value="" onChange={onChange} />)
    const input = screen.getByLabelText('导入 YAML 文件')
    const exactText = '# 注释\r\nkey: value\r\n'
    const exactBytes = new TextEncoder().encode(exactText)
    const exact = { name: 'rule.yaml', size: exactBytes.byteLength, arrayBuffer: vi.fn().mockResolvedValue(exactBytes.buffer) }
    fireEvent.change(input, { target: { files: [exact] } })
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(exactText))

    const oversized = { name: 'large.yaml', size: MAX_KNOWLEDGE_FILE_BYTES + 1, arrayBuffer: vi.fn() }
    fireEvent.change(input, { target: { files: [oversized] } })
    expect(await screen.findByRole('alert')).toHaveTextContent('文件超过 1 MiB 上限')
    expect(oversized.arrayBuffer).not.toHaveBeenCalled()

    const invalid = { name: 'invalid.yaml', size: 2, arrayBuffer: vi.fn().mockResolvedValue(Uint8Array.from([0xc3, 0x28]).buffer) }
    fireEvent.change(input, { target: { files: [invalid] } })
    expect(await screen.findByRole('alert')).toHaveTextContent('文件不是有效的 UTF-8 文本')
  })

  it('忽略卸载后才完成的文件读取', async () => {
    let resolve!: (value: ArrayBuffer) => void
    const pending = new Promise<ArrayBuffer>((done) => { resolve = done })
    const file = { name: 'slow.json', size: 2, arrayBuffer: vi.fn(() => pending) }
    const onChange = vi.fn()
    const view = render(<StructuredEditor format="json" label="JSON 原文" value="" onChange={onChange} />)

    fireEvent.change(screen.getByLabelText('导入 JSON 文件'), { target: { files: [file] } })
    view.unmount()
    resolve(new TextEncoder().encode('{}').buffer as ArrayBuffer)
    await pending
    await Promise.resolve()

    expect(onChange).not.toHaveBeenCalled()
  })
})
