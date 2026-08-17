/**
 * 功能：展示单份不可变安全报告并导出同一快照的受审计 PDF。
 * 实现：读取安全 RenderModel，以语义台账呈现并用可取消 POST 下载固定文件名。
 * 输入：路由中的 opaque 报告 ID、详情 JSON 与有界 PDF 流。
 * 输出：快照详情、独立状态和显式可重试的 PDF 下载。
 * 依赖：Fluent UI、React Query、React Router 与报告组件。
 */
import { Button, Card, MessageBar, MessageBarBody, Text, makeStyles, tokens } from '@fluentui/react-components'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useParams } from 'react-router-dom'

import { ApiError } from '../../shared/api/errors'
import { DataTable, type DataTableColumn } from '../../shared/components/DataTable'
import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { exportReportPDF, fetchReportDetail, type TrendPointView } from './api'
import { RiskSummary } from './components/RiskSummary'
import { TechnicalFindings } from './components/TechnicalFindings'

const useStyles = makeStyles({
  page: { display: 'flex', flexDirection: 'column', gap: tokens.spacingVerticalL },
  snapshot: { padding: tokens.spacingVerticalL, boxShadow: 'none' },
  snapshotTitle: { margin: 0, fontSize: tokens.fontSizeBase500 },
  metadata: { display: 'grid', gridTemplateColumns: 'max-content minmax(0, 1fr)', gap: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalM}` },
  term: { color: tokens.colorNeutralForeground2 },
  value: { margin: 0, overflowWrap: 'anywhere' },
  section: { minWidth: 0, padding: tokens.spacingVerticalL, boxShadow: 'none' },
  sectionTitle: { margin: 0, fontSize: tokens.fontSizeBase500 },
  text: { whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
  recommendations: { display: 'grid', gap: tokens.spacingVerticalS },
})

const dateFormatter = new Intl.DateTimeFormat('zh-CN', { timeZone: 'UTC', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
const dayFormatter = new Intl.DateTimeFormat('zh-CN', { timeZone: 'UTC', month: '2-digit', day: '2-digit' })

function DetailError({ error, retry }: { error: unknown; retry: () => void }) {
  if (error instanceof ApiError && error.kind === 'forbidden') return <StatePanel state="forbidden" title="无权查看此安全报告" />
  if (error instanceof ApiError && error.kind === 'not-found') return <StatePanel state="empty" title="安全报告不存在" description="该报告不存在或不在当前权限范围内。" />
  return <StatePanel state="error" title="暂时无法加载安全报告" description="请稍后重试。" actionLabel="重试" onAction={retry} />
}

export function ReportDetailPage() {
  const styles = useStyles()
  const { reportId = '' } = useParams()
  const exportController = useRef<AbortController | null>(null)
  const exporting = useRef(false)
  const mounted = useRef(false)
  const exportEpoch = useRef(0)
  const activeURLs = useRef(new Set<string>())
  const releaseTimers = useRef(new Set<number>())
  const query = useQuery({
    queryKey: ['reports', 'detail', reportId],
    queryFn: ({ signal }) => fetchReportDetail(reportId, signal),
    retry: false,
  })
  const mutation = useMutation({
    retry: false,
    mutationFn: async (id: string) => {
      if (exporting.current) throw new ApiError('bad-request', 0)
      exporting.current = true
      exportController.current?.abort()
      const controller = new AbortController()
      exportController.current = controller
      const epoch = ++exportEpoch.current
      try {
        return { blob: await exportReportPDF(id, controller.signal), epoch }
      } finally {
        if (exportEpoch.current === epoch) exporting.current = false
      }
    },
    onSuccess: ({ blob, epoch }) => {
      if (!mounted.current || exportEpoch.current !== epoch) return
      const url = URL.createObjectURL(blob)
      activeURLs.current.add(url)
      if (!mounted.current || exportEpoch.current !== epoch) {
        URL.revokeObjectURL(url)
        activeURLs.current.delete(url)
        return
      }
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = '安全报告.pdf'
      if (!mounted.current || exportEpoch.current !== epoch) {
        URL.revokeObjectURL(url)
        activeURLs.current.delete(url)
        return
      }
      anchor.click()
      if (!mounted.current || exportEpoch.current !== epoch) {
        URL.revokeObjectURL(url)
        activeURLs.current.delete(url)
        return
      }
      const timer = window.setTimeout(() => {
        URL.revokeObjectURL(url)
        activeURLs.current.delete(url)
        releaseTimers.current.delete(timer)
      }, 0)
      releaseTimers.current.add(timer)
    },
  })
  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      exportEpoch.current += 1
      exportController.current?.abort()
      for (const timer of releaseTimers.current) window.clearTimeout(timer)
      for (const url of activeURLs.current) URL.revokeObjectURL(url)
      releaseTimers.current.clear()
      activeURLs.current.clear()
    }
  }, [])

  if (query.isPending) return <StatePanel state="loading" title="正在加载安全报告" />
  if (query.isError) return <DetailError error={query.error} retry={() => void query.refetch()} />
  const report = query.data
  const columns: readonly DataTableColumn<TrendPointView>[] = [
    { id: 'date', header: 'UTC 日期', render: (point) => dayFormatter.format(new Date(point.date)) },
    { id: 'completed', header: '报告数', render: (point) => point.completed },
    { id: 'risk', header: '风险分布', render: (point) => `高 ${point.high} / 中 ${point.medium} / 低 ${point.low}` },
  ]

  return (
    <section className={styles.page}>
      <PageHeader title="安全报告详情" description="在线内容与 PDF 均来自任务完成时固化的同一快照。">
        <Button appearance="primary" disabled={mutation.isPending} onClick={() => mutation.mutate(report.id)}>
          {mutation.isPending ? '正在导出' : mutation.isError ? '重新导出 PDF' : '导出 PDF'}
        </Button>
      </PageHeader>
      {mutation.isError ? <MessageBar intent="error" role="alert"><MessageBarBody>PDF 导出失败，请重试。</MessageBarBody></MessageBar> : null}
      <Card className={styles.snapshot} role="region" aria-label="快照信息">
        <h2 className={styles.snapshotTitle}>{report.render.product_name}</h2>
        <dl className={styles.metadata}>
          <dt className={styles.term}>报告 ID</dt><dd className={styles.value}>{report.id}</dd>
          <dt className={styles.term}>任务 ID</dt><dd className={styles.value}>{report.task_id}</dd>
          <dt className={styles.term}>完成时间</dt><dd className={styles.value}>{dateFormatter.format(new Date(report.completed_at))}</dd>
          <dt className={styles.term}>生成时间</dt><dd className={styles.value}>{dateFormatter.format(new Date(report.created_at))}</dd>
          <dt className={styles.term}>快照主色</dt><dd className={styles.value}>{report.render.primary_color}</dd>
          <dt className={styles.term}>水印</dt><dd className={styles.value}>{report.render.watermark || '无'}</dd>
        </dl>
      </Card>
      <RiskSummary risk={report.render.risk} explanation={report.render.score_explanation} mappingVersion={report.render.mapping_version} topRisks={report.render.top_risks} />
      <Card className={styles.section} role="region" aria-label="风险趋势">
        <h2 className={styles.sectionTitle}>30 日风险趋势</h2>
        <DataTable caption="不可变风险趋势" columns={columns} rows={report.render.risk_trend} getRowKey={(point) => point.date} />
      </Card>
      <TechnicalFindings findings={report.render.technical_findings} />
      <Card className={styles.section} role="region" aria-label="修复建议">
        <h2 className={styles.sectionTitle}>修复建议</h2>
        {report.render.recommendations.length ? <ol className={styles.recommendations}>{report.render.recommendations.map((item, index) => <li className={styles.text} key={`${item}-${index}`}>{item}</li>)}</ol> : <Text>此快照没有补充建议。</Text>}
      </Card>
      <Card className={styles.section} role="region" aria-label="覆盖与结论">
        <h2 className={styles.sectionTitle}>覆盖与结论</h2>
        <dl className={styles.metadata}>
          <dt className={styles.term}>覆盖范围</dt><dd className={styles.value}>{report.render.coverage || '未说明'}</dd>
          <dt className={styles.term}>结论</dt><dd className={styles.value}>{report.render.conclusion || '未说明'}</dd>
        </dl>
      </Card>
    </section>
  )
}
