/**
 * 功能：展示不可变报告的决策摘要、风险分布、覆盖结论和重点风险。
 * 实现：使用语义定义列表，并按高、中、低及原始次序稳定排列风险项。
 * 输入：已校验的风险摘要、评分说明、映射版本、覆盖结论和 Top 风险。
 * 输出：可访问且适配长中文的报告决策摘要区域。
 * 依赖：Fluent UI v9 与报告安全 DTO。
 */
import { Badge, Card, Text, makeStyles, tokens } from '@fluentui/react-components'

import type { RiskSeverity, RiskSummaryView, TopRiskView } from '../api'

const severityOrder: Record<RiskSeverity, number> = { high: 0, medium: 1, low: 2 }
const severityLabels: Record<RiskSeverity, string> = { high: '高风险', medium: '中风险', low: '低风险' }
const badgeColors: Record<RiskSeverity, 'danger' | 'warning' | 'informative'> = {
  high: 'danger', medium: 'warning', low: 'informative',
}

const useStyles = makeStyles({
  root: { minWidth: 0, padding: tokens.spacingVerticalL, boxShadow: 'none' },
  title: { margin: 0, fontSize: tokens.fontSizeBase500, lineHeight: tokens.lineHeightBase500 },
  score: { fontSize: tokens.fontSizeHero900, lineHeight: tokens.lineHeightHero900, fontWeight: tokens.fontWeightSemibold },
  summary: { display: 'grid', gridTemplateColumns: 'minmax(160px, 1fr) minmax(0, 3fr)', gap: tokens.spacingHorizontalXL,
    '@media (max-width: 960px)': { gridTemplateColumns: '1fr' } },
  metadata: { display: 'grid', gridTemplateColumns: 'max-content minmax(0, 1fr)', gap: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalM}`, margin: 0,
    '@media (max-width: 960px)': { gridTemplateColumns: '1fr' } },
  term: { color: tokens.colorNeutralForeground2 },
  value: { margin: 0, overflowWrap: 'anywhere' },
  distribution: { display: 'flex', flexWrap: 'wrap', gap: tokens.spacingHorizontalS, marginTop: tokens.spacingVerticalM },
  risks: { marginTop: tokens.spacingVerticalL },
  riskList: { display: 'grid', gap: tokens.spacingVerticalS, padding: 0, listStyle: 'none' },
  riskItem: { padding: tokens.spacingVerticalM, border: `1px solid ${tokens.colorNeutralStroke2}`, borderRadius: tokens.borderRadiusMedium },
  riskCopy: { display: 'block', marginTop: tokens.spacingVerticalXS, overflowWrap: 'anywhere' },
})

interface RiskSummaryProps {
  risk: RiskSummaryView
  explanation: string
  mappingVersion: string
  coverage: string
  conclusion: string
  topRisks: readonly TopRiskView[]
}

export function RiskSummary({ risk, explanation, mappingVersion, coverage, conclusion, topRisks }: RiskSummaryProps) {
  const styles = useStyles()
  const ordered = topRisks.map((item, index) => ({ item, index }))
    .sort((left, right) => severityOrder[left.item.severity] - severityOrder[right.item.severity] || left.index - right.index)

  return (
    <Card className={styles.root} role="region" aria-label="报告决策摘要">
      <h2 className={styles.title}>报告决策摘要</h2>
      <div className={styles.summary}>
        <div>
          <Text block>安全评分</Text>
          <Text className={styles.score}>{risk.score}</Text>
        </div>
        <div>
          <div className={styles.distribution} aria-label="风险分布">
            <Badge color="danger">高风险 {risk.high}</Badge>
            <Badge color="warning">中风险 {risk.medium}</Badge>
            <Badge color="informative">低风险 {risk.low}</Badge>
          </div>
          <dl className={styles.metadata}>
            <dt className={styles.term}>风险映射版本</dt><dd className={styles.value}>{mappingVersion}</dd>
            <dt className={styles.term}>评分说明</dt><dd className={styles.value}>{explanation}</dd>
            <dt className={styles.term}>覆盖范围</dt><dd className={styles.value}>{coverage || '未说明'}</dd>
            <dt className={styles.term}>结论</dt><dd className={styles.value}>{conclusion || '未说明'}</dd>
          </dl>
        </div>
      </div>
      <section className={styles.risks} aria-label="重点风险">
        <h3>重点风险</h3>
        {ordered.length ? (
          <ol className={styles.riskList}>
            {ordered.map(({ item, index }) => (
              <li className={styles.riskItem} key={`${item.severity}-${index}`}>
                <Badge color={badgeColors[item.severity]}>{severityLabels[item.severity]} {item.count}</Badge>
                <Text className={styles.riskCopy}>影响：{item.impact}</Text>
                <Text className={styles.riskCopy}>处置：{item.remediation}</Text>
              </li>
            ))}
          </ol>
        ) : <Text>此快照没有重点风险项。</Text>}
      </section>
    </Card>
  )
}
