import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import { renderWithQuery } from '@/test/render'
import { apiUrl } from '@/test/api'
import { taskRunTimeline } from '@/test/task-inbox'
import { server } from '@/test/server'
import { TaskRunRecord } from '@/features/tasks/TaskRunRecord'

// A run stored before the switch to English: no error code and an old Chinese errorMessage, shown as is.
const dto = taskRunTimeline(), endpoint = apiUrl(`/tasks/${dto.taskId}/nodes/${dto.nodeRunId}/runs/${dto.run.id}/timeline`)
function renderRecord() { return renderWithQuery(<TaskRunRecord taskId={dto.taskId} nodeRunId={dto.nodeRunId} runId={dto.run.id} agentId={dto.agentId} runtimeId={dto.runtimeId} />) }
it.each([
  [null, '旧数据：任务回合执行失败', '执行错误 · 旧数据：任务回合执行失败'],
])('the run summary error (%s) shows the code copy, the server text only as a fallback', async (errorCode, errorMessage, text) => {
  server.use(http.get(endpoint, () => HttpResponse.json(taskRunTimeline({ run: { ...dto.run, status: errorCode === 'user_stopped' ? 'interrupted' : 'failed', errorCode, errorMessage } }))))
  const user = userEvent.setup()
  renderRecord()
  await user.click(await screen.findByRole('button', { name: /^运行记录/ }))
  expect(await screen.findByText(text)).toBeInTheDocument()
})
