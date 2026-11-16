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
it('the task record reuses the command text display and does not switch to the regular API', async () => {
  const command = 'printf "task"\npwd'
  server.use(http.get(endpoint, () => HttpResponse.json(taskRunTimeline({ entries: [{ ...tool('succeeded'), toolName: 'bash', command }] }))))
  const user = userEvent.setup()
  renderRecord()
  await screen.findByText('bash')
  await user.click(screen.getByRole('button', { name: /^Run log/ }))
  const row = screen.getByText('Tool bash').closest('li')!
  expect(row.querySelector('code')?.textContent).toBe(command)
  expect(row).toHaveAttribute('data-tone', 'ok')
  expect(screen.queryByText(/tools\[/)).toBeNull()
})
it('task tools load with their own cursor without adding up the full usage of each page, and the regular API is not called', async () => {
  const user = userEvent.setup(), cursors: (string | null)[] = []
  server.use(http.get(endpoint, ({ request }) => { const cursor = new URL(request.url).searchParams.get('cursor'); cursors.push(cursor); return HttpResponse.json(taskRunTimeline({ entries: [tool(cursor ? 'succeeded' : 'started')], nextCursor: cursor ? null : 'page-two' })) }))
  renderRecord(); await screen.findByText('fixture_tool'); expect(screen.getByText('The run log hasn\'t fully loaded; showing what has loaded so far.')).toBeVisible()
  await user.click(screen.getByRole('button', { name: /^Run log/ })); await user.click(screen.getByRole('button', { name: 'Load more of the run log' }))
  expect(cursors).toEqual([null, 'page-two']); expect(screen.getByRole('list', { name: 'Tool calls' }).children).toHaveLength(1)
  expect(within(screen.getByRole('list', { name: 'Tool calls' })).getByText('Succeeded')).toBeVisible()
  expect(screen.getByRole('button', { name: /^Run log/ })).toHaveTextContent('3 tokens')
  expect(screen.queryByText('The run log hasn\'t fully loaded; showing what has loaded so far.')).toBeNull()
})
it.each(['taskId', 'nodeRunId', 'agentId', 'runtimeId'] as const)('a timeline whose %s is from another scope is rejected, and missing data is not misreported as no tools', async field => {
  server.use(http.get(endpoint, () => HttpResponse.json({ ...dto, [field]: 'foreign-id' })))
  renderRecord(); expect(await screen.findByRole('alert')).toHaveTextContent("The task, run, Agent, or instance returned by the Inbox or run log didn't match"); expect(screen.queryByRole('list', { name: 'Tool calls' })).toBeNull()
})
// The no-code case with an old Chinese errorMessage is in src/zh-CN/features/tasks/TaskRunRecord.zh.test.tsx.
it.each([
  ['pi_exited', 'The Pi process exited while replying.', 'Agent process exited unexpectedly'],
  ['user_stopped', 'Stopped by a person.', 'Stopped'],
  ['some_new_code', 'The task turn failed.', 'Run error · The task turn failed.'],
])('the run summary error (%s) shows the code copy, the server text only as a fallback', async (errorCode, errorMessage, text) => {
  server.use(http.get(endpoint, () => HttpResponse.json(taskRunTimeline({ run: { ...dto.run, status: errorCode === 'user_stopped' ? 'interrupted' : 'failed', errorCode, errorMessage } }))))
  const user = userEvent.setup()
  renderRecord()
  await user.click(await screen.findByRole('button', { name: /^Run log/ }))
  expect(await screen.findByText(text)).toBeInTheDocument()
})
