/**
 * 功能：将已审阅的 MCP 专属合同同步至现有 Swagger 三件套。
 * 实现：只替换 MCP paths/definitions 和通用接口的 MCP 退役约束，保留其他模块。
 * 输入：internal/apidocs/swagger.yaml；输出：YAML、JSON、docs.go 内嵌模板。
 * 依赖：web/console 已安装的 yaml；从仓库根目录运行 node scripts/sync-mcp-openapi.mjs。
 * 注意：这是受控合同同步，不调用 swag init，也不修改人读 API 参考文档。
 */
import fs from 'node:fs'
import { execFileSync } from 'node:child_process'
import { createRequire } from 'node:module'
const require = createRequire(new URL('../web/console/package.json', import.meta.url))
const YAML = require('yaml')
const base = new URL('../internal/apidocs/', import.meta.url)
const source = YAML.parse(fs.readFileSync(new URL('swagger.yaml', base), 'utf8'))
const previous = JSON.parse(process.argv.includes('--preserve-head-order') ? execFileSync('git', ['show', 'HEAD:internal/apidocs/swagger.json'], { encoding: 'utf8', maxBuffer: 8 * 1024 * 1024 }) : fs.readFileSync(new URL('swagger.json', base), 'utf8'))
const ref = (name) => ({ $ref: '#/definitions/' + name })
const str = (extra = {}) => ({ type: 'string', ...extra })
const integer = (min = 1, extra = {}) => ({ type: 'integer', minimum: min, ...extra })
const array = (items, extra = {}) => ({ type: 'array', items, ...extra })
const object = (properties, required = Object.keys(properties), description) => ({ type: 'object', additionalProperties: false, properties, required, ...(description ? { description } : {}) })
const status = str({ enum: ['pending', 'dispatching', 'running', 'succeeded', 'failed', 'dispatch_failed', 'dispatch_unknown', 'cancelled'] })
const kind = str({ enum: ['repository', 'service'] })
const historicalKind = str({ enum: ['repository', 'service', 'legacy_unknown'] })
const transport = str({ enum: ['auto', 'http', 'sse'] })
const authKind = str({ enum: ['none', 'bearer', 'api_key_header', 'custom_headers'] })
const id = str({ format: 'uuid' })
const timestamp = str({ format: 'date-time' })
const revision = str({ pattern: '^[1-9][0-9]*$', description: 'Decimal string; send quoted as If-Match, for example "2". Not a connection version.' })
const summary = { id, owner: str(), status, source_kind: historicalKind, created_at: timestamp, updated_at: timestamp }
const connection = { id, name: str({ maxLength: 80 }), description: str({ maxLength: 500 }), scope: str({ enum: ['private', 'global'] }), current_version: integer(), resource_revision: revision, enabled: { type: 'boolean' }, transport, detected_transport: str({ enum: ['http', 'sse'] }), probe_status: str({ enum: ['not_tested', 'passed', 'failed'] }), authentication_kind: str({ enum: [...authKind.enum, 'unknown'] }), created_at: timestamp, updated_at: timestamp }
const auth = object({ kind: authKind, header_name: str({ maxLength: 64 }), secret: str({ format: 'password', 'x-writeOnly': true }) }, ['kind'], 'Write-only authentication. bearer requires secret; api_key_header requires header_name and secret; none forbids credentials and headers; custom_headers requires at least one header.')
const header = object({ name: str({ maxLength: 64 }), value: str({ maxLength: 8192, format: 'password', 'x-writeOnly': true }) })
const configInput = { name: connection.name, description: connection.description, server_url: str({ format: 'uri', description: 'Configured HTTP(S) endpoint; validated against server-owned outbound CIDRs. No URL credentials, query or fragment.' }), transport, authentication: auth, headers: array(header, { maxItems: 10 }) }
const createDescription = 'Dedicated mcp_scan only. Flat strict JSON object, exact case-sensitive keys; null, duplicates and unknown fields rejected. source_kind repository accepts exactly one HTTPS Git repository_url or 1..10 ready owner-scoped MCP attachment_ids; both service fields and authorization_confirmed must be absent. service requires connection_config_id, exact immutable connection_config_version, authorization_confirmed=true and no repository fields. Only enabled, tested and policy-approved saved connections are eligible. model_id is an optional governed reference; thread defaults to 4 and is 1..32. Language is server-fixed zh_CN; task_type, language, country_iso_code, content, headers and secrets are forbidden. Source-related audit metadata contains source_kind and the Boolean authorization confirmation; safe phase metadata may appear in the general task audit. Task/binding/replay writes are atomic. HTTP 202 is the accepted task; 200 plus Idempotent-Replay:true is the same success replay. A reused key with a different payload returns 409. Never automatically repeat a POST after an uncertain response; explicit retry reuses its key. No endpoint, Git URL or credential is returned.'
const definitions = {
  'mcpscans.CreateRequest': object({ source_kind: kind, repository_url: str({ maxLength: 8192, format: 'uri' }), attachment_ids: array(id, { minItems: 1, maxItems: 10, uniqueItems: true }), connection_config_id: id, connection_config_version: integer(), authorization_confirmed: { type: 'boolean', description: 'Required true only for service; repository forbids this field.' }, model_id: id, thread: integer(1, { maximum: 32, default: 4 }) }, ['source_kind'], createDescription),
  'mcpscans.MutationResponse': object({ task_id: id, status }),
  'mcpscans.Summary': object(summary),
  'mcpscans.Detail': object({ ...summary, input_summary: object({ language: str({ enum: ['zh_CN'] }), source_kind: historicalKind, model_id: id, thread: integer(1, { maximum: 32 }) }, ['language', 'source_kind']), report_id: id }, [...Object.keys(summary), 'input_summary']),
  'mcpscans.ListResponse': object({ items: array(ref('mcpscans.Summary')), total: integer(0), page: integer(1, { maximum: 1000 }), page_size: integer(1, { maximum: 100 }) }),
  'mcpscans.AttachmentResponse': object({ id, state: str({ enum: ['uploading', 'ready'] }), size: integer(0), max_file_bytes: integer(), max_chunk_bytes: integer() }),
  'mcpconnections.CreateRequest': object(configInput, ['name', 'server_url', 'transport', 'authentication'], 'Always creates a private connection owned by the authenticated Subject. scope/owner IDs and outbound policy flags are not accepted.'),
  'mcpconnections.UpdateRequest': object({ ...configInput, authentication: object({ ...auth.properties }, [], 'Omitted secret retains a previously configured secret only for the same kind and header name. Kind/header-name changes require replacement. Explicit blank secret is invalid when required.'), headers: array(object(header.properties, ['name']), { maxItems: 10 }), enabled: { type: 'boolean' } }, [], 'Partial patch. enabled must be the only field when present. Connection-material or transport changes create a new immutable version, disable it and require a new test. Metadata-only edits preserve version and eligibility. headers:[] removes headers; omitted values preserve matching names. none must remove prior headers. If-Match revision CAS required.'),
  'mcpconnections.Summary': object(connection, Object.keys(connection).filter((key) => key !== 'detected_transport')),
  'mcpconnections.ListResponse': object({ items: array(ref('mcpconnections.Summary')) }),
  'mcpconnections.Detail': object({ ...connection, server_url: configInput.server_url, authentication_header_name: str(), authentication_configured: { type: 'boolean' }, headers: array(object({ name: str(), configured: { type: 'boolean' } })) }, [...Object.keys(connection).filter((key) => key !== 'detected_transport'), 'server_url', 'authentication_configured', 'headers'], 'Owner/admin management-only exception: returns endpoint and configured header names for editing, but never secret/header values. No-store. Auditor has no management detail access.'),
  'mcpconnections.MutationResponse': object({ id, current_version: integer(), resource_revision: revision, status: str({ enum: ['created', 'updated', 'enabled', 'disabled', 'passed', 'failed'] }) }),
  'mcpconnections.OptionsResponse': object({ items: array(object({ connection_id: id, connection_version: integer(), name: str(), scope: connection.scope, transport: str({ enum: ['http', 'sse'] }), authentication_kind: authKind })) }),
}
Object.assign(source.definitions, definitions)
source.definitions['mcpscans.CreateRequest'].properties.model_id = { ...id, description: 'Optional governed model. Omit for basic read-only checks without an LLM; no default-model or environment-credential fallback. Basic report coverage and reference scores are explicitly limited.' }
const csrf = { in: 'header', name: 'X-CSRF-Token', type: 'string', required: true, description: 'Must match current aig_csrf Cookie.' }
const idem = { in: 'header', name: 'Idempotency-Key', type: 'string', required: true, maxLength: 128 }
const match = { in: 'header', name: 'If-Match', type: 'string', required: true, description: 'Quoted current resource_revision; missing or malformed is 428.' }
const pathParam = (name) => ({ in: 'path', name, type: 'string', required: true })
const body = (schema) => ({ in: 'body', name: 'request', required: true, schema })
const errors = { 400: { description: 'Invalid strict request or server-owned outbound policy rejected.' }, 401: { description: 'Authentication required.' }, 403: { description: 'Role, first-password-change or CSRF gate denied.' }, 404: { description: 'Absent or inaccessible opaque resource.' }, 409: { description: 'IDEMPOTENCY_KEY_REUSED, MCP_CONNECTION_VERSION_CONFLICT or state conflict; refresh before a new operation.' }, 500: { description: 'Fixed safe error only; no upstream diagnostics or secrets.' }, 503: { description: 'Required governed runtime or egress unavailable.' } }
const operation = (summary, schema, { write = false, code = 200, parameters = [], description = '', multipart = false, idempotent = true } = {}) => ({ summary, description: 'Authenticated Cookie Subject; first-password-change gate. Ordinary users access their own resources, admins govern all, auditors read safe lists/details only and cannot mutate or obtain connection management details/options. ' + description, tags: ['MCP'], produces: ['application/json'], ...(write ? { consumes: [multipart ? 'multipart/form-data' : 'application/json'] } : {}), parameters: [...(write ? [csrf, ...(idempotent ? [idem] : [])] : []), ...parameters], responses: { ...errors, [code]: { description: 'Safe response.', ...(schema ? { schema } : {}) }, ...(write && idempotent ? { 200: { description: 'Successful operation or Idempotent-Replay:true.', ...(schema ? { schema } : {}) } } : {}) } })
const paging = [{ in: 'query', name: 'page', ...integer(1, { maximum: 1000, default: 1 }) }, { in: 'query', name: 'page_size', ...integer(1, { maximum: 100, default: 20 }) }, { in: 'query', name: 'status', ...status }]
const root = '/api/v1/platform/'
source.paths[root + 'mcp-scans'] = { get: operation('List MCP scans', ref('mcpscans.ListResponse'), { parameters: paging, description: 'Only MCP canonical and historical aliases. No engine polling or raw parameter projection. Source kind is repository, service or legacy_unknown.' }), post: operation('Create MCP scan', ref('mcpscans.MutationResponse'), { write: true, code: 202, parameters: [body(ref('mcpscans.CreateRequest'))], description: createDescription + ' After commit, dispatch failure or unknown acknowledgement still returns 202 with the accepted task_id. Read dedicated task details for actual dispatch state; never create again with a new key.' }) }
source.paths[root + 'mcp-scans'].post.description += ' Gateway or server 5xx is also an unknown outcome; explicit retry retains the original idempotency key.'
source.paths[root + 'mcp-scans/{taskID}'] = { get: operation('Read MCP scan', ref('mcpscans.Detail'), { parameters: [pathParam('taskID')], description: 'Historical MCP remains readable. No raw target/parameter/log/attachment-name fields. report_id only for an existing authorized completed snapshot.' }) }
source.paths[root + 'mcp-scans/{taskID}/cancel'] = { post: operation('Cancel MCP scan', ref('mcpscans.MutationResponse'), { write: true, parameters: [pathParam('taskID'), body(object({}))], description: 'Engine cancellation is idempotent; final task status, audit and replay response commit atomically. No automatic POST retry.' }) }
source.paths[root + 'mcp-connection-configs'] = { get: operation('List safe MCP connection configurations', ref('mcpconnections.ListResponse')), post: operation('Create MCP connection configuration', ref('mcpconnections.MutationResponse'), { write: true, code: 201, parameters: [body(ref('mcpconnections.CreateRequest'))] }) }
source.paths[root + 'mcp-connection-configs/{configID}'] = { get: operation('Read editable MCP configuration', ref('mcpconnections.Detail'), { parameters: [pathParam('configID')], description: 'Owner or administrator only; no-store; ETag is quoted resource_revision. Secrets remain write-only.' }), patch: operation('Update MCP configuration', ref('mcpconnections.MutationResponse'), { write: true, parameters: [pathParam('configID'), match, body(ref('mcpconnections.UpdateRequest'))], description: 'Version eligibility and revision checked under lock. Missing/malformed If-Match:428.' }) }
source.paths[root + 'mcp-connection-configs/{configID}/test'] = { post: operation('Test configured MCP connection', ref('mcpconnections.MutationResponse'), { write: true, parameters: [pathParam('configID'), match, body(object({}))], description: 'Controlled initialize only; no tools/call. Begins by disabling connection, clearing prior result and reserving a monotonic attempt outside the network call. Final passed/failed result, audit and replay commit together. A real connection failure returns safe status failed with HTTP 200; no upstream details. At least one minute between attempts per config. Successful test does not enable automatically. 428 missing If-Match; 429 rate limit.' }) }
source.paths[root + 'mcp-connection-options'] = { get: operation('Select eligible MCP connections', ref('mcpconnections.OptionsResponse'), { description: 'Writer roles only. Includes only visible enabled, passed current immutable versions with a concrete transport and current outbound eligibility. No endpoint/header values.' }) }
for (const p of ['mcp-connection-configs/{configID}', 'mcp-connection-configs/{configID}/test']) for (const [method, op] of Object.entries(source.paths[root + p])) if (method !== 'get') op.responses['428'] = { description: 'Quoted If-Match resource revision required.' }
source.paths[root + 'mcp-connection-configs/{configID}/test'].post.responses['429'] = { description: 'Probe rate limited.' }
const file = (name) => ({ in: 'formData', name, type: 'file', required: true })
source.paths[root + 'mcp-scan-attachments'] = { post: operation('Upload MCP-only code attachment', ref('mcpscans.AttachmentResponse'), { write: true, code: 201, idempotent: false, multipart: true, parameters: [file('file')], description: 'MCP-only namespace. Response has opaque ID, state, bytes and configured size limits; no filename. There is no browser download endpoint.' }) }
source.paths[root + 'mcp-scan-attachments/chunked'] = { post: operation('Begin MCP chunked attachment', ref('mcpscans.AttachmentResponse'), { write: true, code: 201, idempotent: false, parameters: [body(object({ filename: str({ maxLength: 255 }), size: integer() }))] }) }
source.paths[root + 'mcp-scan-attachments/{attachmentID}/chunks'] = { post: operation('Upload idempotent MCP chunk', null, { write: true, code: 204, idempotent: false, multipart: true, parameters: [pathParam('attachmentID'), { in: 'formData', name: 'chunk_index', type: 'integer', minimum: 0, required: true }, file('chunk')], description: 'Idempotency is server-derived from upload ID, chunk index and SHA-256 digest. Exact replay returns 204; same index with different bytes returns 409. Per-upload lock serializes chunk/merge/abort/cleanup. Counter, audit and replay are atomic.' }) }
source.paths[root + 'mcp-scan-attachments/{attachmentID}/merge'] = { post: operation('Merge MCP code attachment', ref('mcpscans.AttachmentResponse'), { write: true, parameters: [pathParam('attachmentID'), body(object({ total_chunks: integer(), file_size: integer() }))], description: 'Validates complete bytes under per-upload resource lock. Same-key replay returns ready without remerging. No browser download route.' }) }
source.paths[root + 'mcp-scan-attachments/{attachmentID}'] = { delete: operation('Abort unbound MCP attachment', null, { write: true, code: 204, idempotent: false, parameters: [pathParam('attachmentID')], description: 'Only uploading/ready owner-scoped attachments. Attached files cannot be removed.' }) }
const retired = ' MCP mcp_scan and Mcp-Scan are isolated: return 409 MCP_SPECIALIZED_ENDPOINT_REQUIRED with specialized_path pointing to /api/v1/platform/mcp-scans. Use the dedicated endpoint; generic default lists exclude both aliases before count and pagination.'
const generic = source.paths[root + 'tasks']
generic.post.description = generic.post.description.replace('mcp_scan params allow model_id and thread; ', '').replace(/For mcp_scan, params\.source_kind[\s\S]*?Every model_id/, 'Every model_id').replace(retired, '') + retired
generic.get.description = generic.get.description.replace(retired, '') + retired
for (const op of [generic.get, generic.post, source.paths[root + 'tasks/{taskID}'].get, source.paths[root + 'tasks/{taskID}/cancel'].post]) { op.responses['409'] = { description: 'MCP_SPECIALIZED_ENDPOINT_REQUIRED. specialized_path identifies the dedicated MCP endpoint.' }; if (!op.description.includes('MCP_SPECIALIZED_ENDPOINT_REQUIRED')) op.description += retired }
generic.get.parameters.find((p) => p.name === 'task_type').enum = ['ai_infra_scan', 'model_redteam_report', 'agent_scan', 'skills_scan']
const genericBody = generic.post.parameters.find((p) => p.in === 'body').schema
genericBody.properties.task_type.enum = ['ai_infra_scan', 'model_redteam_report', 'agent_scan', 'skills_scan']
delete genericBody.properties.params.properties
// 保留既有 JSON 成员顺序，避免重排无关模块造成大范围生成差异。
function preserveOrder(value, prior) {
  if (Array.isArray(value)) return value.map((entry, index) => preserveOrder(entry, prior?.[index]))
  if (!value || typeof value !== 'object') return value
  const keys = [...Object.keys(prior && typeof prior === 'object' ? prior : {}).filter((key) => Object.hasOwn(value, key)), ...Object.keys(value).filter((key) => !prior || !Object.hasOwn(prior, key))]
  return Object.fromEntries(keys.map((key) => [key, preserveOrder(value[key], prior?.[key])]))
}
const json = JSON.stringify(preserveOrder(source, previous), null, 4)
fs.writeFileSync(new URL('swagger.yaml', base), YAML.stringify(source, { lineWidth: 0, aliasDuplicateObjects: false, indentSeq: false, singleQuote: true }))
fs.writeFileSync(new URL('swagger.json', base), json + '\n')
const goPath = new URL('docs.go', base)
const goSource = fs.readFileSync(goPath, 'utf8')
fs.writeFileSync(goPath, goSource.replace(/const docTemplate = `[\s\S]*?`\r?\n\r?\nvar SwaggerInfo/, 'const docTemplate = `' + json + '`\n\nvar SwaggerInfo'))
console.log('MCP OpenAPI synchronized; run go test ./internal/apidocs.')
