/**
 * 功能：展示与治理安全评测集 JSON。
 * 实现：使用服务端真实分页、独立原文详情和保留字节的 JSON 编辑器。
 * 输入：URL 分页/搜索、评测集摘要与原始 JSON。
 * 输出：评测集台账、详情和管理员治理动作。
 * 依赖：知识 API 与 RawResourceLedger。
 */
import type { DataTableColumn } from '../../shared/components/DataTable'
import {
  createEvaluation,
  deleteEvaluation,
  fetchEvaluationPage,
  fetchRawKnowledge,
  updateEvaluation,
  type EvaluationSummary,
} from './api'
import { RawResourceLedger } from './components/RawResourceLedger'

const columns: readonly DataTableColumn<EvaluationSummary>[] = [
  { id: 'name', header: '评测集名称', render: (item) => item.name },
  { id: 'description', header: '说明', render: (item) => item.descriptionZh || item.description || '未提供' },
  { id: 'count', header: '样本数', render: (item) => item.count },
  { id: 'language', header: '语言', render: (item) => item.language || '未标注' },
]

function fetchEvaluationRaw(id: string, signal?: AbortSignal) {
  return fetchRawKnowledge('evaluation', id, signal)
}

export function EvaluationPage() {
  return <RawResourceLedger
    resourceKey="evaluations"
    title="安全评测集"
    description="查看用于安全评测的 JSON 数据；保存时服务端会按真实 schema 复核并更新 count。"
    resourceLabel="评测集"
    format="json"
    columns={columns}
    getID={(item) => item.name}
    fetchPage={fetchEvaluationPage}
    fetchRaw={fetchEvaluationRaw}
    createResource={createEvaluation}
    updateResource={updateEvaluation}
    deleteResource={deleteEvaluation}
    currentFileName={(id) => `${id}.json`}
  />
}
