/**
 * 功能：展示配置产品名、本地构建版本和随发行版提供的许可证归属。
 * 实现：只读取匿名安全版本 DTO，不查询远程更新服务或加载外部内容。
 * 输入：PublicBrand 上下文与 GET /api/v1/version 白名单响应。
 * 输出：版本、提交、构建时间和本地字体许可说明。
 * 依赖：TanStack Query、公共品牌、管理 API 和共享状态组件。
 */
import { useQuery } from '@tanstack/react-query'
import { makeStyles, tokens } from '@fluentui/react-components'

import { PageHeader } from '../../shared/components/PageHeader'
import { StatePanel } from '../../shared/components/StatePanel'
import { usePublicBrand } from '../../shared/brand/PublicBrandProvider'
import { fetchSafeVersion } from '../admin/api'

const useStyles = makeStyles({
  details: { display: 'grid', gridTemplateColumns: 'max-content minmax(0, 1fr)', gap: tokens.spacingVerticalS, margin: 0 },
  term: { color: tokens.colorNeutralForeground2 },
})

export function AboutPage() {
  const styles = useStyles()
  const { productName } = usePublicBrand()
  const query = useQuery({ queryKey: ['safe-version'], queryFn: ({ signal }) => fetchSafeVersion(signal), retry: false, staleTime: Number.POSITIVE_INFINITY })

  return (
    <section>
      <PageHeader title="关于" description={`${productName} 使用本地构建元数据；该页面不请求互联网内容。`} />
      {query.isPending ? <StatePanel state="loading" title="正在加载本地版本信息" /> : null}
      {query.isError ? <StatePanel state="error" title="暂时无法加载本地版本信息" description="请稍后重试。" actionLabel="重试" onAction={() => void query.refetch()} /> : null}
      {query.data ? (
        <>
          <dl className={styles.details}>
            <dt className={styles.term}>产品</dt><dd>{productName}</dd>
            <dt className={styles.term}>版本</dt><dd>{query.data.version}</dd>
            <dt className={styles.term}>提交</dt><dd>{query.data.commit}</dd>
            <dt className={styles.term}>构建时间</dt><dd>{query.data.build_time}</dd>
          </dl>
          <p>本控制台使用随发行版提供的 IBM Plex Sans、Droid Sans Fallback 与 Apache-2.0 许可文本。</p>
        </>
      ) : null}
    </section>
  )
}
