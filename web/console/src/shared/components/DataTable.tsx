/**
 * 功能：提供面向监管台账的类型安全语义表格。
 * 实现：使用 Fluent Table 组件和原生 caption、列头作用域渲染调用方数据。
 * 输入：表题、列定义、数据行与稳定行键函数。
 * 输出：可由辅助技术正确导航的原生表格结构。
 * 依赖：React 与 Fluent UI v9。
 */
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableHeaderCell,
  TableRow,
  makeStyles,
  mergeClasses,
  tokens,
} from '@fluentui/react-components'
import type { Key, ReactNode } from 'react'

export interface DataTableColumn<T> {
  id: string
  header: string
  render: (row: T) => ReactNode
}

interface DataTableProps<T> {
  caption: string
  columns: readonly DataTableColumn<T>[]
  rows: readonly T[]
  getRowKey: (row: T) => Key
  className?: string
}

const useStyles = makeStyles({
  table: {
    width: '100%',
    backgroundColor: tokens.colorNeutralBackground1,
    borderRadius: tokens.borderRadiusMedium,
  },
  caption: {
    paddingBottom: tokens.spacingVerticalS,
    color: tokens.colorNeutralForeground2,
    fontWeight: tokens.fontWeightSemibold,
    textAlign: 'left',
  },
  headerCell: {
    color: tokens.colorNeutralForeground2,
    fontWeight: tokens.fontWeightSemibold,
  },
})

export function DataTable<T>({ caption, columns, rows, getRowKey, className }: DataTableProps<T>) {
  const styles = useStyles()
  const columnIds = new Set(columns.map((column) => column.id))
  if (columnIds.size !== columns.length) {
    throw new Error('表格列 id 必须唯一')
  }

  return (
    <Table className={mergeClasses(styles.table, className)}>
      <caption className={styles.caption}>{caption}</caption>
      <TableHeader>
        <TableRow>
          {columns.map((column) => (
            <TableHeaderCell className={styles.headerCell} key={column.id} scope="col">
              {column.header}
            </TableHeaderCell>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={getRowKey(row)}>
            {columns.map((column) => (
              <TableCell key={column.id}>{column.render(row)}</TableCell>
            ))}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
