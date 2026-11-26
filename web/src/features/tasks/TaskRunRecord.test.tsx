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
it('the task record reuses the command and target display and does not switch to the regular API', async () => {
  const command = 'printf "task"\npwd'
  const entries: Schemas['TimelineEntry'][] = [
    { ...tool('succeeded'), toolCallId: 'bash-call', toolName: 'Bash', command },
    { ...tool('succeeded'), toolCallId: 'read-call', toolName: 'Read', target: 'web/src/features/chat/TimelineBody.tsx' },
    { ...tool('succeeded'), toolCallId: 'grep-call', toolName: 'Grep', target: 'StepCode' },
    { ...tool('succeeded'), toolCallId: 'old-edit', toolName: 'Edit' },
  ]
  server.use(http.get(endpoint, () => HttpResponse.json(taskRunTimeline({ entries }))))
  const user = userEvent.setup()
  renderRecord()
  await screen.findByText('Bash')
  await user.click(screen.getByRole('button', { name: /^Run log/ }))
  const bashRow = screen.getByText('Tool Bash').closest('li')!
  expect(bashRow.querySelector('code')?.textContent).toBe(command)
  expect(bashRow).toHaveAttribute('data-tone', 'ok')
  const toggle = within(bashRow).getByRole('button')
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await user.click(toggle)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByText('Tool Read').closest('li')?.querySelector('code')?.textContent).toBe('web/src/features/chat/TimelineBody.tsx')
  expect(screen.getByText('Tool Grep').closest('li')?.querySelector('code')?.textContent).toBe('StepCode')
  const oldEdit = screen.getByText('Tool Edit').closest('li')!
  expect(oldEdit.querySelector('code')).toBeNull()
  expect(within(oldEdit).queryByText(/Command not recorded/)).toBeNull()
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
it('a task turn whose runtime reported no usage says so instead of "0 tokens", closed and open', async () => {
  const zeros = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0, calls: 0 }
  server.use(http.get(endpoint, () => HttpResponse.json(taskRunTimeline({ run: { ...dto.run, usageReported: false }, usage: zeros }))))
  const user = userEvent.setup()
  renderRecord()
  const toggle = await screen.findByRole('button', { name: 'Run log · 3.0 s · no usage data' })
  await user.click(toggle)
  expect(await screen.findByText("The runtime didn't report token usage for this turn.")).toBeInTheDocument()
  expect(screen.queryByText(/\b0 tokens?\b/)).toBeNull()
})

it.each([
  ['completed', 'No result'],
  ['failed', 'No result'],
  ['interrupted', 'No result'],
  ['running', 'Running'],
] as const)('task run %s: a tool without its end event shows “%s”', async (status, label) => {
  server.use(http.get(endpoint, () => HttpResponse.json(taskRunTimeline({ run: { ...dto.run, status, endedAt: status === 'running' ? null : dto.run.endedAt }, entries: [tool('started')] }))))
  renderRecord()
  const list = await screen.findByRole('list', { name: 'Tool calls' })
  expect(within(list).getByRole('listitem')).toHaveTextContent(`fixture_tool${label}`)
})

it('a task tool the server closed as no_result shows “No result”', async () => {
  server.use(http.get(endpoint, () => HttpResponse.json(taskRunTimeline({ entries: [tool('started'), tool('no_result')] }))))
  renderRecord()
  const list = await screen.findByRole('list', { name: 'Tool calls' })
  expect(within(list).getByRole('listitem')).toHaveTextContent('fixture_toolNo result')
})
