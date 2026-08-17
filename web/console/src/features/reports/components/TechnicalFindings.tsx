/**
 * 功能：展示不可变报告中的安全技术发现。
 * 实现：以有序语义列表呈现服务端已脱敏的证据、影响与修复建议。
 * 输入：已校验且有界的技术发现白名单数组。
 * 输出：支持长中文折行的技术发现区域或真实空态。
 * 依赖：Fluent UI v9 与报告安全 DTO。
 */
import { Card, Text, makeStyles, tokens } from '@fluentui/react-components'

import type { TechnicalFindingView } from '../api'

const useStyles = makeStyles({
  root: { minWidth: 0 },
  title: { margin: 0, fontSize: tokens.fontSizeBase500, lineHeight: tokens.lineHeightBase500 },
  list: { display: 'grid', gap: tokens.spacingVerticalM, padding: 0, listStyle: 'none' },
  item: { minWidth: 0, padding: tokens.spacingVerticalL, boxShadow: 'none' },
  findingTitle: { margin: 0, fontSize: tokens.fontSizeBase400, overflowWrap: 'anywhere' },
  details: { display: 'grid', gridTemplateColumns: 'max-content minmax(0, 1fr)', gap: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalM}`, marginBottom: 0 },
  term: { color: tokens.colorNeutralForeground2 },
  value: { margin: 0, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' },
})

export function TechnicalFindings({ findings }: { findings: readonly TechnicalFindingView[] }) {
  const styles = useStyles()
  return (
    <section className={styles.root} aria-label="技术发现">
      <h2 className={styles.title}>技术发现</h2>
      {findings.length ? (
        <ol className={styles.list}>
          {findings.map((finding, index) => (
            <li key={`${finding.title}-${index}`}>
              <Card className={styles.item}>
                <h3 className={styles.findingTitle}>{finding.title}</h3>
                <dl className={styles.details}>
                  <dt className={styles.term}>证据</dt><dd className={styles.value}>{finding.evidence || '未提供'}</dd>
                  <dt className={styles.term}>影响</dt><dd className={styles.value}>{finding.impact || '未提供'}</dd>
                  <dt className={styles.term}>修复</dt><dd className={styles.value}>{finding.remediation || '未提供'}</dd>
                </dl>
              </Card>
            </li>
          ))}
        </ol>
      ) : <Text>此快照没有技术发现。</Text>}
    </section>
  )
}
