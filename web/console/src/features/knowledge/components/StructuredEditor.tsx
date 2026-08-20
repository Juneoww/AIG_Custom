/**
 * 功能：提供保留原文字节的 YAML/JSON 编辑、校验、行号、高亮预览和文件导入。
 * 实现：使用受限 YAML AST、原生 Textarea 与安全 React 文本节点，不依赖 contenteditable 或 HTML 注入。
 * 输入：格式、原始文本、变更回调、只读状态与可选校验回调。
 * 输出：未自动格式化的文本和带行列位置的本地格式校验结果。
 * 依赖：React、Fluent UI v9、yaml 2.8.1 与知识库文件大小合同。
 */
import {
  Field,
  MessageBar,
  MessageBarBody,
  Text,
  Textarea,
  makeStyles,
  tokens,
} from '@fluentui/react-components'
import { Fragment, useEffect, useMemo, useRef, useState, type ChangeEvent, type ReactNode } from 'react'
import { LineCounter, isAlias, isMap, isScalar, isSeq, parseDocument } from 'yaml'

import { MAX_KNOWLEDGE_FILE_BYTES } from '../api'

export interface StructuredValidationResult {
  valid: boolean
  message: string
  line?: number
  column?: number
  renderable?: boolean
}

interface StructuredEditorProps {
  format: 'yaml' | 'json'
  label: string
  value: string
  onChange: (value: string) => void
  disabled?: boolean
  onValidationChange?: (result: StructuredValidationResult) => void
}

const MAX_STRUCTURE_DEPTH = 64
const MAX_STRUCTURE_NODES = 20_000
const MAX_STRUCTURE_LINES = 10_000
const MAX_YAML_ALIASES = 50
const DANGEROUS_KEYS = new Set(['__proto__', 'prototype', 'constructor'])

const useStyles = makeStyles({
  root: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalS },
  toolbar: { display: 'flex', alignItems: 'end', justifyContent: 'space-between', gap: tokens.spacingHorizontalM, flexWrap: 'wrap' },
  editorGrid: {
    display: 'grid',
    gridTemplateColumns: '3rem minmax(0, 1fr)',
    minHeight: '18rem',
    border: `${tokens.strokeWidthThin} solid ${tokens.colorNeutralStroke1}`,
    borderRadius: tokens.borderRadiusMedium,
    overflow: 'hidden',
    backgroundColor: tokens.colorNeutralBackground1,
  },
  lineNumbers: {
    margin: 0,
    padding: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalS}`,
    listStyle: 'none',
    textAlign: 'right',
    color: tokens.colorNeutralForeground3,
    backgroundColor: tokens.colorNeutralBackground2,
    fontFamily: tokens.fontFamilyMonospace,
    fontSize: tokens.fontSizeBase200,
    lineHeight: '1.5',
    userSelect: 'none',
  },
  textarea: {
    border: 0,
    borderRadius: 0,
    minHeight: '18rem',
    '& textarea': {
      minHeight: '18rem',
      fontFamily: tokens.fontFamilyMonospace,
      fontSize: tokens.fontSizeBase200,
      lineHeight: '1.5',
      whiteSpace: 'pre',
      overflow: 'auto',
    },
  },
  preview: {
    margin: 0,
    padding: tokens.spacingVerticalS,
    maxHeight: '14rem',
    overflow: 'auto',
    border: `${tokens.strokeWidthThin} solid ${tokens.colorNeutralStroke2}`,
    borderRadius: tokens.borderRadiusMedium,
    backgroundColor: tokens.colorNeutralBackground2,
    fontFamily: tokens.fontFamilyMonospace,
    fontSize: tokens.fontSizeBase200,
    lineHeight: '1.5',
    whiteSpace: 'pre-wrap',
    overflowWrap: 'anywhere',
  },
  key: { color: tokens.colorPaletteBlueForeground2 },
  string: { color: tokens.colorPaletteGreenForeground2 },
  literal: { color: tokens.colorPaletteDarkOrangeForeground2 },
  comment: { color: tokens.colorNeutralForeground3 },
  hint: { color: tokens.colorNeutralForeground2 },
})

function positionFromOffset(text: string, offset: number): { line: number; column: number } {
  const before = text.slice(0, Math.max(0, Math.min(offset, text.length)))
  const lines = before.split('\n')
  return { line: lines.length, column: (lines.at(-1)?.length ?? 0) + 1 }
}

function jsonSyntaxPosition(text: string, error: unknown): { line: number; column: number } {
  const message = error instanceof Error ? error.message : ''
  const explicit = /line\s+(\d+)\s+column\s+(\d+)/i.exec(message)
  if (explicit) return { line: Number(explicit[1]), column: Number(explicit[2]) }
  const offset = /position\s+(\d+)/i.exec(message)
  if (offset) return positionFromOffset(text, Number(offset[1]))
  const unexpectedToken = /unexpected token\s+'([^']+)'/i.exec(message)?.[1]
  if (unexpectedToken) {
    const tokenOffset = text.lastIndexOf(unexpectedToken)
    if (tokenOffset >= 0) return positionFromOffset(text, tokenOffset)
  }
  return positionFromOffset(text, 0)
}

function inspectJSON(value: unknown): StructuredValidationResult {
  const stack: Array<{ value: unknown; depth: number }> = [{ value, depth: 1 }]
  const visited = new Set<object>()
  let nodes = 0
  while (stack.length > 0) {
    const current = stack.pop()
    if (!current) break
    nodes += 1
    if (nodes > MAX_STRUCTURE_NODES) return { valid: false, message: 'JSON 节点数量超过安全上限。', renderable: false }
    if (current.depth > MAX_STRUCTURE_DEPTH) return { valid: false, message: 'JSON 嵌套层级超过安全上限。', renderable: false }
    if (typeof current.value !== 'object' || current.value === null) continue
    if (visited.has(current.value)) continue
    visited.add(current.value)
    for (const [key, child] of Object.entries(current.value)) {
      if (DANGEROUS_KEYS.has(key)) return { valid: false, message: `JSON 包含危险字段“${key}”。` }
      stack.push({ value: child, depth: current.depth + 1 })
    }
  }
  return { valid: true, message: 'JSON 格式有效。' }
}

function validateJSON(text: string): StructuredValidationResult {
  try {
    const parsed = JSON.parse(text) as unknown
    return inspectJSON(parsed)
  } catch (error) {
    const { line, column } = jsonSyntaxPosition(text, error)
    return { valid: false, message: `JSON 格式错误：第 ${line} 行，第 ${column} 列。`, line, column }
  }
}

function validateYAML(text: string): StructuredValidationResult {
  const lineCounter = new LineCounter()
  const document = parseDocument(text, { lineCounter, prettyErrors: false, strict: true, uniqueKeys: true })
  const firstError = document.errors[0]
  if (firstError) {
    const position = lineCounter.linePos(firstError.pos[0])
    return { valid: false, message: `YAML 格式错误：第 ${position.line} 行，第 ${position.col} 列。`, line: position.line, column: position.col }
  }

  let nodes = 0
  let aliases = 0
  let violation: StructuredValidationResult | undefined
  const inspect = (node: unknown, depth: number): void => {
    if (violation || node === null || node === undefined) return
    nodes += 1
    if (nodes > MAX_STRUCTURE_NODES) {
      violation = { valid: false, message: 'YAML 节点数量超过安全上限。', renderable: false }
      return
    }
    if (depth > MAX_STRUCTURE_DEPTH) {
      violation = { valid: false, message: 'YAML 嵌套层级超过安全上限。', renderable: false }
      return
    }
    if (isAlias(node)) {
      aliases += 1
      if (aliases > MAX_YAML_ALIASES) violation = { valid: false, message: 'YAML 别名数量超过安全上限。', renderable: false }
      return
    }
    if (isMap(node)) {
      for (const pair of node.items) {
        if (isScalar(pair.key) && typeof pair.key.value === 'string' && DANGEROUS_KEYS.has(pair.key.value)) {
          violation = { valid: false, message: 'YAML 包含危险字段。' }
          return
        }
        inspect(pair.key, depth + 1)
        inspect(pair.value, depth + 1)
      }
    } else if (isSeq(node)) {
      for (const item of node.items) inspect(item, depth + 1)
    }
  }
  inspect(document.contents, 1)
  return violation ?? { valid: true, message: 'YAML 格式有效，保存时服务端将执行完整 schema 校验。' }
}

export function validateStructuredText(format: 'yaml' | 'json', text: string): StructuredValidationResult {
  if (new TextEncoder().encode(text).byteLength > MAX_KNOWLEDGE_FILE_BYTES) return { valid: false, message: '内容超过 1 MiB 上限。', renderable: false }
  if (text.length === 0) return { valid: false, message: '内容不能为空。', line: 1, column: 1 }
  let lineCount = 1
  for (let index = 0; index < text.length; index += 1) {
    if (text.charCodeAt(index) === 10 && ++lineCount > MAX_STRUCTURE_LINES) {
      return { valid: false, message: `${format.toUpperCase()} 行数超过安全上限。`, renderable: false }
    }
  }
  return format === 'json' ? validateJSON(text) : validateYAML(text)
}

function highlightedScalar(text: string, styles: ReturnType<typeof useStyles>): ReactNode {
  const trimmed = text.trim()
  const className = /^(?:true|false|null|[-+]?\d+(?:\.\d+)?)$/.test(trimmed) ? styles.literal : styles.string
  return <span className={className} data-token={className === styles.literal ? 'literal' : 'string'}>{text}</span>
}

function highlightedLine(line: string, format: 'yaml' | 'json', styles: ReturnType<typeof useStyles>): ReactNode {
  const trimmed = line.trimStart()
  if (trimmed.startsWith('#')) return <span className={styles.comment} data-token="comment">{line}</span>
  if (format === 'yaml') {
    const mapping = /^(\s*(?:-\s*)?)([^:#][^:]*)(:)(.*)$/.exec(line)
    if (mapping) {
      return <>{mapping[1]}<span className={styles.key} data-token="key">{mapping[2]}</span>{mapping[3]}{highlightedScalar(mapping[4] ?? '', styles)}</>
    }
    return highlightedScalar(line, styles)
  }
  const mapping = /^(\s*)("(?:\\.|[^"\\])*")(\s*:\s*)(.*?)(,?)$/.exec(line)
  if (mapping) {
    return <>{mapping[1]}<span className={styles.key} data-token="key">{mapping[2]}</span>{mapping[3]}{highlightedScalar(mapping[4] ?? '', styles)}{mapping[5]}</>
  }
  return highlightedScalar(line, styles)
}

export function StructuredEditor({ format, label, value, onChange, disabled = false, onValidationChange }: StructuredEditorProps) {
  const styles = useStyles()
  const [importError, setImportError] = useState('')
  const mountedRef = useRef(true)
  const importEpochRef = useRef(0)
  const validation = useMemo(() => validateStructuredText(format, value), [format, value])
  const lines = useMemo(() => validation.renderable === false ? [] : value.split('\n'), [validation.renderable, value])
  const displayedError = importError || (!validation.valid ? validation.message : '')

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      importEpochRef.current += 1
    }
  }, [])

  useEffect(() => {
    onValidationChange?.(validation)
  }, [onValidationChange, validation])

  const importFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const input = event.currentTarget
    const file = input.files?.[0]
    const epoch = ++importEpochRef.current
    setImportError('')
    if (!file) return
    if (file.size > MAX_KNOWLEDGE_FILE_BYTES) {
      setImportError('文件超过 1 MiB 上限。')
      input.value = ''
      return
    }
    try {
      const bytes = await file.arrayBuffer()
      if (!mountedRef.current || importEpochRef.current !== epoch) return
      const content = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
      if (new TextEncoder().encode(content).byteLength > MAX_KNOWLEDGE_FILE_BYTES) {
        setImportError('文件超过 1 MiB 上限。')
        return
      }
      onChange(content)
    } catch {
      if (mountedRef.current && importEpochRef.current === epoch) setImportError('文件不是有效的 UTF-8 文本。')
    } finally {
      if (mountedRef.current) input.value = ''
    }
  }

  return (
    <div className={styles.root}>
      <div className={styles.toolbar}>
        <Text className={styles.hint}>内容按 UTF-8 原样保存；本地校验不会自动格式化。</Text>
        <Field label={`导入 ${format.toUpperCase()} 文件`}>
          <input aria-label={`导入 ${format.toUpperCase()} 文件`} type="file" accept={format === 'json' ? '.json,application/json' : '.yaml,.yml,text/yaml,application/yaml'} disabled={disabled} onChange={(event) => void importFile(event)} />
        </Field>
      </div>
      {displayedError ? <MessageBar intent="error" role="alert"><MessageBarBody>{displayedError}</MessageBarBody></MessageBar> : null}
      <div className={styles.editorGrid}>
        <ol className={styles.lineNumbers} aria-label="编辑器行号">
          {lines.map((_, index) => <li key={index}>{index + 1}</li>)}
        </ol>
        <Textarea className={styles.textarea} aria-label={label} value={value} disabled={disabled} resize="vertical" onChange={(_, data) => onChange(data.value)} />
      </div>
      <pre className={styles.preview} aria-label={`${format.toUpperCase()} 语法高亮预览`}>
        {lines.map((line, index) => (
          <Fragment key={index}>{highlightedLine(line, format, styles)}{index < lines.length - 1 ? '\n' : null}</Fragment>
        ))}
      </pre>
    </div>
  )
}
