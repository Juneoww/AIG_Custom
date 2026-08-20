/**
 * 功能：展示与治理 MCP 扫描插件 YAML。
 * 实现：消费后端无分页目录及其 RawData，管理员沿用既有 MCP CRUD 路由。
 * 输入：MCP 安全摘要、opaque 插件 ID 与原始 YAML。
 * 输出：MCP 台账、原文查看和受确认治理动作。
 * 依赖：知识 API 与 RawResourceLedger。
 */
import type { DataTableColumn } from '../../shared/components/DataTable'
import {
  createMCPPlugin,
  deleteMCPPlugin,
  fetchMCPPlugins,
  fetchMCPRaw,
  updateMCPPlugin,
  type MCPPluginSummary,
} from './api'
import { RawResourceLedger } from './components/RawResourceLedger'

const columns: readonly DataTableColumn<MCPPluginSummary>[] = [
  { id: 'id', header: '插件 ID', render: (item) => item.id },
  { id: 'name', header: '插件名称', render: (item) => item.name || '未提供' },
  { id: 'description', header: '说明', render: (item) => item.description || '未提供' },
  { id: 'categories', header: '类别', render: (item) => item.categories.join('、') || '未标注' },
]

async function fetchPage(_query: { page: number; size: number; query: string }, signal?: AbortSignal) {
  const items = await fetchMCPPlugins(signal)
  return { items, total: items.length, page: 1, size: Math.max(1, items.length) }
}

async function fetchRaw(id: string, signal?: AbortSignal): Promise<string> {
  return fetchMCPRaw(id, signal)
}

export function MCPPage() {
  return <RawResourceLedger
    resourceKey="mcp"
    title="MCP 插件"
    description="查看 MCP 代码扫描插件；目录接口不提供服务端筛选，因此本页完整展示真实目录。"
    resourceLabel="MCP 插件"
    format="yaml"
    columns={columns}
    getID={(item) => item.id}
    fetchPage={fetchPage}
    fetchRaw={fetchRaw}
    createResource={createMCPPlugin}
    updateResource={updateMCPPlugin}
    deleteResource={deleteMCPPlugin}
    currentFileName={(id) => `${id}.yaml`}
    searchable={false}
  />
}
