import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { renderWithQuery } from '@/test/render'
import { apiUrl } from '@/test/api'
import { taskRunTimeline } from '@/test/task-inbox'
import { server } from '@/test/server'
import { TaskRunRecord } from './TaskRunRecord'

const dto = taskRunTimeline(), endpoint = apiUrl(`/tasks/${dto.taskId}/nodes/${dto.nodeRunId}/runs/${dto.run.id}/timeline`)
const tool = (state: Schemas['ToolState']): Schemas['TimelineTool'] => ({ type: 'tool', at: '2026-10-18T00:00:02Z', toolCallId: 'actual-call', toolName: 'fixture_tool', state, endedAt: state === 'started' ? null : '2026-10-18T00:00:03Z', durationMs: null })
function renderRecord() { return renderWithQuery(<TaskRunRecord taskId={dto.taskId} nodeRunId={dto.nodeRunId} runId={dto.run.id} agentId={dto.agentId} runtimeId={dto.runtimeId} />) }
it('任务记录复用命令文本显示，不改走常态接口', async () => {
  const command = 'printf "task"\npwd'
  server.use(http.get(endpoint, () => HttpResponse.json(taskRunTimeline({ entries: [{ ...tool('succeeded'), toolName: 'bash', command }] }))))
  const user = userEvent.setup()
  renderRecord()
  await screen.findByText('bash')
  await user.click(screen.getByRole('button', { name: /^运行记录/ }))
  const row = screen.getByText('工具 bash').closest('li')!
  expect(row.querySelector('code')?.textContent).toBe(command)
  expect(row).toHaveAttribute('data-tone', 'ok')
  expect(screen.queryByText(/tools\[/)).toBeNull()
})
it('任务工具按独立cursor加载且不累加每页全量usage，不调用常态API', async () => {
  const user = userEvent.setup(), cursors: (string | null)[] = []
  server.use(http.get(endpoint, ({ request }) => { const cursor = new URL(request.url).searchParams.get('cursor'); cursors.push(cursor); return HttpResponse.json(taskRunTimeline({ entries: [tool(cursor ? 'succeeded' : 'started')], nextCursor: cursor ? null : 'page-two' })) }))
  renderRecord(); await screen.findByText('fixture_tool'); expect(screen.getByText('运行记录尚未加载完整；当前仅显示已加载部分。')).toBeVisible()
  await user.click(screen.getByRole('button', { name: /^运行记录/ })); await user.click(screen.getByRole('button', { name: '加载后续运行记录' }))
  expect(cursors).toEqual([null, 'page-two']); expect(screen.getByRole('list', { name: '工具调用' }).children).toHaveLength(1)
  expect(within(screen.getByRole('list', { name: '工具调用' })).getByText('成功')).toBeVisible()
  expect(screen.getByRole('button', { name: /^运行记录/ })).toHaveTextContent('3 token')
  expect(screen.queryByText('运行记录尚未加载完整；当前仅显示已加载部分。')).toBeNull()
})
it.each(['taskId', 'nodeRunId', 'agentId', 'runtimeId'] as const)('timeline %s跨scope响应拒绝，不把缺少数据伪报无工具', async field => {
  server.use(http.get(endpoint, () => HttpResponse.json({ ...dto, [field]: 'foreign-id' })))
  renderRecord(); expect(await screen.findByRole('alert')).toHaveTextContent('原任务、执行、Agent 或实例不匹配'); expect(screen.queryByRole('list', { name: '工具调用' })).toBeNull()
})
