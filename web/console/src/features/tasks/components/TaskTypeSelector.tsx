/** 功能：共用创建入口的任务类型选择，切换后由上层挂载相应表单。 */
import { Field, Select } from '@fluentui/react-components'

import type { TaskCreateRequest } from '../../../shared/api/types'
import { taskTypeLabels } from '../TaskListPage'

export function TaskTypeSelector({ value, onChange, disabled = false }: {
  value: TaskCreateRequest['task_type']
  onChange: (value: TaskCreateRequest['task_type']) => void
  disabled?: boolean
}) {
  return (
    <Field label="扫描类型">
      <Select value={value} disabled={disabled} onChange={(_, data) => onChange(data.value as TaskCreateRequest['task_type'])}>
        {Object.entries(taskTypeLabels).filter(([type]) => type !== 'unknown' && type !== 'mcp_scan').map(([type, label]) => <option key={type} value={type}>{label}</option>)}
      </Select>
    </Field>
  )
}
