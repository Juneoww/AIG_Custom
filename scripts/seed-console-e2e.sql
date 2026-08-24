-- 功能：为隔离企业控制台 E2E 注入可重复的任务与不可变报告快照。
-- 实现：仅从已由 PowerShell 种子创建的身份表读取测试用户；固定 ID 便于浏览器断言，数据卷使用 tmpfs。
-- 输入：postgres-e2e 数据库及 e2e-admin、e2e-user 两个已完成首次改密的账号。
-- 输出：两条任务和两份报告快照；不会输出密码、Cookie、Token 或原始结果。
-- 依赖：PostgreSQL 16、已完成平台迁移和 seed-console-e2e.ps1。
-- 使用：由 docker-compose.console-e2e.yml 的 fixture-e2e 服务执行。
BEGIN;

WITH e2e_user AS (
  SELECT id FROM identity_users WHERE username = 'e2e-user'
)
INSERT INTO platform_tasks (
  id, owner_user_id, owner_username, idempotency_key, engine_session_id, task_type,
  content, params, attachment_refs, country_iso_code, status, dispatch_error,
  dispatch_attempts, dispatch_claim_token, dispatch_lease_until, created_at, updated_at
)
SELECT
  'e2e-task-001', id, 'e2e-user', 'e2e-task-key-001', 'e2e-engine-001', 'mcp_scan',
  'https://e2e.invalid/fixture', '{"thread":4}'::jsonb, '[]'::jsonb, 'zh_CN', 'succeeded', '',
  1, '', NULL, NOW() - INTERVAL '2 minutes', NOW() - INTERVAL '1 minute'
FROM e2e_user
ON CONFLICT (id) DO NOTHING;

WITH e2e_user AS (
  SELECT id FROM identity_users WHERE username = 'e2e-user'
)
INSERT INTO platform_tasks (
  id, owner_user_id, owner_username, idempotency_key, engine_session_id, task_type,
  content, params, attachment_refs, country_iso_code, status, dispatch_error,
  dispatch_attempts, dispatch_claim_token, dispatch_lease_until, created_at, updated_at
)
SELECT
  'e2e-task-running', id, 'e2e-user', 'e2e-task-key-running', '', 'mcp_scan',
  'https://e2e.invalid/running', '{"thread":2}'::jsonb, '[]'::jsonb, 'zh_CN', 'running', '',
  1, '', NULL, NOW() - INTERVAL '1 minute', NOW()
FROM e2e_user
ON CONFLICT (id) DO NOTHING;

WITH constants AS (
  SELECT NOW() AS completed_at, NOW() AS generated_at
), render AS (
  SELECT jsonb_build_object(
    'render_version', 'report-render-v2',
    'mapping_version', 'risk-v2',
    'generated_at', generated_at,
    'completed_at', completed_at,
    'task_id', 'e2e-task-001',
    'task_type', 'mcp_scan',
    'product_name', 'E2E 企业安全平台',
    'primary_color', '#1677ff',
    'watermark', 'E2E',
    'risk', jsonb_build_object('mapping_version', 'risk-v2', 'high', 1, 'medium', 1, 'low', 0, 'score', 82),
    'score_explanation', 'E2E 固定快照只用于验证安全报告的不可变浏览器合同。',
    'risk_trend', (
      SELECT jsonb_agg(jsonb_build_object(
        'date', date_trunc('day', generated_at) - INTERVAL '29 days' + index * INTERVAL '1 day',
        'completed', CASE WHEN index = 29 THEN 1 ELSE 0 END,
        'high', CASE WHEN index = 29 THEN 1 ELSE 0 END,
        'medium', CASE WHEN index = 29 THEN 1 ELSE 0 END,
        'low', 0
      ) ORDER BY index)
      FROM generate_series(0, 29) AS index
    ),
    'risk_distribution', jsonb_build_object('high', 1, 'medium', 1, 'low', 0),
    'top_risks', jsonb_build_array(
      jsonb_build_object('severity', 'high', 'count', 1, 'impact', '高风险 E2E 验证项。', 'remediation', '完成修复后复测。'),
      jsonb_build_object('severity', 'medium', 'count', 1, 'impact', '中风险 E2E 验证项。', 'remediation', '纳入修复计划。')
    ),
    'technical_findings', jsonb_build_array(
      jsonb_build_object('title', 'E2E 固定发现', 'evidence', '测试快照证据。', 'impact', '仅用于端到端验证。', 'remediation', '无需生产处置。')
    ),
    'recommendations', jsonb_build_array('验证 PDF 导出与报告详情来自同一快照。'),
    'coverage', 'E2E 受控报告快照。',
    'conclusion', '该数据不参与生产扫描或规则决策。'
  ) AS data, completed_at, generated_at
  FROM constants
), e2e_user AS (
  SELECT id FROM identity_users WHERE username = 'e2e-user'
)
INSERT INTO report_snapshots (
  id, task_id, owner_user_id, task_type, completed_at, created_at,
  raw_result, risk_summary, render_data, brand_snapshot
)
SELECT
  'e2e-report-001', 'e2e-task-001', e2e_user.id, 'mcp_scan', render.completed_at, render.generated_at,
  '{"score":82,"results":[{"level":"high"},{"level":"medium"}]}'::jsonb,
  '{"mapping_version":"risk-v2","high":1,"medium":1,"low":0,"score":82}'::jsonb,
  render.data,
  '{"product_name":"E2E 企业安全平台","primary_color":"#1677ff","logo":null,"logo_mime":"","watermark":"E2E","updated_by":"e2e"}'::jsonb
FROM render CROSS JOIN e2e_user
ON CONFLICT (id) DO NOTHING;

WITH constants AS (
  SELECT NOW() AS completed_at, NOW() AS generated_at
), render AS (
  SELECT jsonb_build_object(
    'render_version', 'report-render-v2',
    'mapping_version', 'risk-v2',
    'generated_at', generated_at,
    'completed_at', completed_at,
    'task_id', 'e2e-task-admin-only',
    'task_type', 'mcp_scan',
    'product_name', 'E2E 企业安全平台',
    'primary_color', '#1677ff',
    'watermark', 'E2E',
    'risk', jsonb_build_object('mapping_version', 'risk-v2', 'high', 0, 'medium', 0, 'low', 0, 'score', 100),
    'score_explanation', '管理员可见性夹具。',
    'risk_trend', (
      SELECT jsonb_agg(jsonb_build_object(
        'date', date_trunc('day', generated_at) - INTERVAL '29 days' + index * INTERVAL '1 day',
        'completed', CASE WHEN index = 29 THEN 1 ELSE 0 END,
        'high', 0, 'medium', 0, 'low', 0
      ) ORDER BY index)
      FROM generate_series(0, 29) AS index
    ),
    'risk_distribution', jsonb_build_object('high', 0, 'medium', 0, 'low', 0),
    'top_risks', '[]'::jsonb,
    'technical_findings', '[]'::jsonb,
    'recommendations', '[]'::jsonb,
    'coverage', '管理员可见性夹具。',
    'conclusion', '仅用于权限验证。'
  ) AS data, completed_at, generated_at
  FROM constants
), e2e_admin AS (
  SELECT id FROM identity_users WHERE username = 'e2e-admin'
)
INSERT INTO report_snapshots (
  id, task_id, owner_user_id, task_type, completed_at, created_at,
  raw_result, risk_summary, render_data, brand_snapshot
)
SELECT
  'e2e-report-admin-only', 'e2e-task-admin-only', e2e_admin.id, 'mcp_scan', render.completed_at, render.generated_at,
  '{"score":100,"results":[]}'::jsonb,
  '{"mapping_version":"risk-v2","high":0,"medium":0,"low":0,"score":100}'::jsonb,
  render.data,
  '{"product_name":"E2E 企业安全平台","primary_color":"#1677ff","logo":null,"logo_mime":"","watermark":"E2E","updated_by":"e2e"}'::jsonb
FROM render CROSS JOIN e2e_admin
ON CONFLICT (id) DO NOTHING;

COMMIT;
