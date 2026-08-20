/**
 * 功能：展示与治理组件指纹 YAML 规则。
 * 实现：复用服务端分页原文台账，所有写操作仅由管理员经既有 API 发起。
 * 输入：URL 分页/搜索、指纹安全 DTO 与原始 YAML。
 * 输出：指纹台账、详情编辑器和受确认的创建/更新/删除动作。
 * 依赖：知识 API 与 RawResourceLedger。
 */
import type { DataTableColumn } from '../../shared/components/DataTable'
import {
  createFingerprint,
  deleteFingerprint,
  fetchFingerprintPage,
  fetchRawKnowledge,
  updateFingerprint,
  type FingerprintSummary,
} from './api'
import { RawResourceLedger } from './components/RawResourceLedger'

const columns: readonly DataTableColumn<FingerprintSummary>[] = [
  { id: 'name', header: '规则名称', render: (item) => item.name },
  { id: 'description', header: '说明', render: (item) => item.description || '未提供' },
  { id: 'author', header: '维护者', render: (item) => item.author || '未提供' },
  { id: 'severity', header: '级别', render: (item) => item.severity || '未标注' },
]

function fetchFingerprintRaw(id: string, signal?: AbortSignal) {
  return fetchRawKnowledge('fingerprint', id, signal)
}

export function FingerprintPage() {
  return <RawResourceLedger
    resourceKey="fingerprints"
    title="指纹规则"
    description="查看组件识别规则；管理员修改仅影响后续扫描，不改变历史报告快照。"
    resourceLabel="指纹规则"
    format="yaml"
    columns={columns}
    getID={(item) => item.name}
    fetchPage={fetchFingerprintPage}
    fetchRaw={fetchFingerprintRaw}
    createResource={createFingerprint}
    updateResource={updateFingerprint}
    deleteResource={deleteFingerprint}
    sampleContent={'info:\n  name: example-product\n  author: security-team\n  desc: 示例组件指纹\n  severity: info\nhttp: []\nversion: []\n'}
    sampleFileName="指纹规则样例.yaml"
  />
}
