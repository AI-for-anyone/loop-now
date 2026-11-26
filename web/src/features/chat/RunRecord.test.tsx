import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, makeAgent, makeMessage, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'

type Entry = Schemas['TimelineEntry']

const at = (seconds: number) => new Date(Date.UTC(2026, 9, 12, 8, 0, 0) + seconds * 1000).toISOString()
const usage = (input: number, output: number, cacheRead = 0, cacheWrite = 0, calls = 1): Schemas['UsageTotals'] => ({
  calls,
  input,
  output,
  cacheRead,
  cacheWrite,
  total: input + output + cacheRead + cacheWrite,
})

const modelCall = (seconds: number, input: number, output: number): Entry => ({
  type: 'model_call',
  at: at(seconds),
  kind: 'assistant',
  provider: 'anthropic',
  model: 'claude-sonnet-5-5',
  responseModel: 'claude-sonnet-5-5-20261001',
  stopReason: 'toolUse',
  input,
  output,
  cacheRead: 0,
  cacheWrite: 0,
  reasoning: null,
  total: input + output,
  durationMs: 800,
})

const tool = (seconds: number, id: string, state: Schemas['ToolState'], durationMs: number | null, command?: string): Entry => ({
  type: 'tool',
  at: at(seconds),
  toolCallId: id,
  toolName: 'bash',
  state,
  endedAt: durationMs === null ? null : at(seconds + durationMs / 1000),
  durationMs,
  command,
})

function timelineHandler(agentId: string, build: () => Schemas['RunTimeline']) {
  const calls: string[] = []
  const handler = http.get(apiUrl(`/agents/${agentId}/runs/:runId/timeline`), ({ params }) => {
    calls.push(String(params.runId))
    return HttpResponse.json(build())
  })
  return { handler, calls }
}

// The expandable run record inside a reply.
const recordToggle = () => screen.getByRole('button', { name: /^Run log/ })

describe('run log timeline', () => {
  it('collapsed by default; expanded it shows the two bash calls in order with the actual command, duration and result, no output, ending with completion and total', async () => {
    const agent = makeAgent()
    const run = makeRun({
      queuedAt: at(0),
      startedAt: at(0),
      runningAt: at(1),
      endedAt: at(9.4),
      usage: usage(1200, 34),
      model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' },
      configVersion: 7,
      tools: [
        { toolCallId: 't1', toolName: 'bash', state: 'started', at: at(2) },
        { toolCallId: 't1', toolName: 'bash', state: 'succeeded', at: at(2.4) },
        { toolCallId: 't2', toolName: 'bash', state: 'started', at: at(4) },
        { toolCallId: 't2', toolName: 'bash', state: 'failed', at: at(6) },
      ],
    })
    const entries: Entry[] = [
      { type: 'dispatched', at: at(0) },
      { type: 'phase', at: at(0), phase: 'waiting_slot', runtime: 'pi' },
      { type: 'phase', at: at(0.064), phase: 'preparing', runtime: 'pi' },
      { type: 'workspace_sync', at: at(0.1), result: 'succeeded', durationMs: 500, error: null, sources: [] },
      { type: 'phase', at: at(0.664), phase: 'launching', runtime: 'pi' },
      { type: 'pi_reused', at: at(0.7), pid: 48213, processStartedAt: at(-600), runtime: 'pi' },
      { type: 'running', at: at(1) },
      modelCall(1.5, 1000, 20),
      tool(2, 't1', 'succeeded', 400, 'pwd'),
      modelCall(3, 200, 14),
      tool(4, 't2', 'failed', 2000, 'printf "line one\\n"\nexit 1'),
      { type: 'ended', at: at(9.4), status: 'completed', endReason: null, errorCode: null },
    ]
    const timeline = timelineHandler(agent.id, () => ({ run, entries, usage: run.usage }))
    const { user } = await renderChat({
      agent,
      messages: [makeMessage({ text: 'Run the tests', run }), makeMessage({ role: 'assistant', text: 'The tests are done', run })],
      handlers: [timeline.handler],
    })
    const toggle = await waitFor(recordToggle)
    expect(toggle).toHaveTextContent('Run log · 9.4 s · 1,234 tokens')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(timeline.calls).toEqual([])

    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const panel = document.getElementById(toggle.getAttribute('aria-controls')!)!
    await within(panel).findByText('Run log')
    expect(timeline.calls).toEqual([run.id])
    expect(panel).toHaveTextContent(run.id)
    expect(panel).toHaveTextContent('model=anthropic / claude-sonnet-5-5 · Config v7 · Sent while idle')
    expect(panel).toHaveTextContent('Total 9.4 s')

    const labels = within(panel)
      .getAllByRole('listitem')
      .filter((item) => item.hasAttribute('data-tone'))
      .map((item) => item.querySelector('.font-black')!.textContent)
    expect(labels).toEqual(['Queued', 'Dispatched', 'Waiting for a run slot', 'Preparing the environment', 'Sync config and folders', 'Pi start', 'Started replying', 'Model call', 'Tool bash', 'Model call', 'Tool bash', 'Completed'])
    const bashSteps = within(panel).getAllByText('Tool bash').map((label) => label.closest('li')!)
    expect(bashSteps[0]).toHaveTextContent('400 ms')
    expect(bashSteps[0]).toHaveAttribute('data-tone', 'ok')
    expect(bashSteps[0].querySelector('code')?.textContent).toBe('pwd')
    expect(bashSteps[1]).toHaveTextContent('2.0 s')
    expect(bashSteps[1]).toHaveTextContent('state=failed')
    expect(bashSteps[1]).toHaveAttribute('data-tone', 'fail')
    expect(bashSteps[1].querySelector('code')?.textContent).toBe('printf "line one\\n"\nexit 1')
    expect(within(panel).getByText('Pi start').closest('li')).toHaveTextContent('Reused the resident Pi process, PID 48213.')
    expect(within(panel).getByText('Queued').closest('li')).toHaveTextContent('The Agent was idle, so this was dispatched right away.')
    expect(panel).toHaveTextContent('Token usage')
    expect(panel).toHaveTextContent('Input 1,200')
    expect(panel).toHaveTextContent('1 model call')
    expect(panel).toHaveTextContent('The reply finished normally.')
    expect(panel).not.toHaveTextContent(/args|arguments|output=|tools\[/i)
  })

  it('a reply whose sync failed: shows the failed source and the reason, no “Started replying”', async () => {
    const agent = makeAgent()
    const run = makeRun({
      status: 'failed',
      errorCode: 'workspace_sync_failed',
      errorMessage: 'Failed to clone repo',
      queuedAt: at(0),
      startedAt: at(0),
      runningAt: null,
      endedAt: at(2),
    })
    const entries: Entry[] = [
      { type: 'dispatched', at: at(0) },
      { type: 'phase', at: at(0.1), phase: 'preparing', runtime: 'pi' },
      {
        type: 'workspace_sync',
        at: at(0.2),
        result: 'failed',
        durationMs: 1700,
        error: 'Failed to clone repo',
        sources: [
          { path: 'docs', kind: 'files', outcome: 'files_synced', filesWritten: 2, filesDeleted: 1, error: null },
          { path: 'repo', kind: 'git', outcome: 'failed', filesWritten: null, filesDeleted: null, error: 'Authentication failed' },
        ],
      },
      { type: 'ended', at: at(2), status: 'failed', endReason: null, errorCode: 'workspace_sync_failed' },
    ]
    const timeline = timelineHandler(agent.id, () => ({ run, entries, usage: run.usage }))
    const { user } = await renderChat({ agent, messages: [makeMessage({ text: 'Hello', run })], handlers: [timeline.handler] })
    await user.click(await waitFor(recordToggle))
    const panel = document.getElementById(recordToggle().getAttribute('aria-controls')!)!
    const sync = (await within(panel).findByText('Sync config and folders')).closest('li')!
    expect(sync).toHaveTextContent('Sync failed: Failed to clone repo')
    expect(sync).toHaveTextContent('docs: files synced (2 written, 1 deleted)')
    expect(sync).toHaveTextContent('repo: failed (Authentication failed)')
    expect(sync).toHaveAttribute('data-tone', 'fail')
    expect(within(panel).queryByText('Started replying')).toBeNull()
    expect(panel).toHaveTextContent('errorCode=workspace_sync_failed · errorMessage="Failed to clone repo"')
    expect(panel).toHaveTextContent('Failed: Workspace sync failed (Failed to clone repo)')
  })

  it('expanded while the reply is in progress, another tool call appears in the timeline without expanding again', async () => {
    const agent = makeAgent()
    const run = makeRun({ status: 'running', queuedAt: at(0), startedAt: at(0), runningAt: at(1), endedAt: null })
    let entries: Entry[] = [{ type: 'dispatched', at: at(0) }, { type: 'running', at: at(1) }, tool(2, 't1', 'succeeded', 300)]
    const timeline = timelineHandler(agent.id, () => ({ run, entries, usage: run.usage }))
    const { user, emit } = await renderChat({
      agent,
      messages: [makeMessage({ text: 'Hello', run })],
      status: { busy: true, currentRun: run },
      handlers: [timeline.handler],
    })
    await emit('snapshot', { status: makeStatus(agent.id, { busy: true, currentRun: run, processState: 'busy' }), currentText: '', queuedRuns: [], queueItems: [] })
    const toggle = await waitFor(recordToggle)
    expect(toggle).toHaveTextContent(/^Run log · .+ so far/)
    await user.click(toggle)
    const panel = document.getElementById(toggle.getAttribute('aria-controls')!)!
    await within(panel).findByText('Tool bash')
    expect(within(panel).queryByText('Completed')).toBeNull()

    entries = [...entries, tool(5, 't2', 'started', null, 'sleep 1')]
    await emit('run.tool', { runId: run.id, toolCallId: 't2', toolName: 'bash', state: 'started', at: at(5) })
    await waitFor(() => expect(within(panel).getAllByText('Tool bash')).toHaveLength(2))
    const running = within(panel).getAllByText('Tool bash')[1].closest('li')!
    expect(running).toHaveTextContent('Running')
    expect(running.querySelector('code')?.textContent).toBe('sleep 1')
    expect(running).toHaveAttribute('data-tone', 'warn')
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
  })

  it('replies from before this feature have only some time points; shows only what exists', async () => {
    const agent = makeAgent()
    const run = makeRun({ status: 'interrupted', endReason: 'user_interrupt', queuedAt: at(0), startedAt: at(0), runningAt: null, endedAt: at(3) })
    const entries: Entry[] = [
      { type: 'dispatched', at: at(0) },
      { type: 'ended', at: at(3), status: 'interrupted', endReason: 'user_interrupt', errorCode: null },
    ]
    const timeline = timelineHandler(agent.id, () => ({ run, entries, usage: run.usage }))
    const { user } = await renderChat({ agent, messages: [makeMessage({ text: 'Hello', run })], handlers: [timeline.handler] })
    await user.click(await waitFor(recordToggle))
    const panel = document.getElementById(recordToggle().getAttribute('aria-controls')!)!
    expect(await within(panel).findByText('Dispatched')).toBeInTheDocument()
    expect(within(panel).getAllByText('Interrupted').length).toBeGreaterThan(0)
    expect(panel).toHaveTextContent('endReason=user_interrupt')
    expect(within(panel).queryByRole('alert')).toBeNull()
  })

  it('shows commands for non-Pi shell tools and targets for file and search tools in the expanded run log', async () => {
    const agent = makeAgent({ runtime: 'claude-code' })
    const run = makeRun({ queuedAt: at(0), startedAt: at(0), runningAt: at(1), endedAt: at(5), usage: usage(500, 20) })
    const entries: Entry[] = [
      { type: 'running', at: at(1) },
      { type: 'tool', at: at(1.5), toolCallId: 'c1', toolName: 'Bash', command: 'go test ./internal/server/runrecord/...', state: 'succeeded', endedAt: at(2.368), durationMs: 868 },
      { type: 'tool', at: at(2.5), toolCallId: 'f1', toolName: 'Read', target: 'internal/server/runrecord/tool_command.go', state: 'succeeded', endedAt: at(2.52), durationMs: 20 },
      { type: 'tool', at: at(3), toolCallId: 's1', toolName: 'Grep', target: 'withToolCommand', state: 'succeeded', endedAt: at(3.04), durationMs: 40 },
      { type: 'tool', at: at(3.5), toolCallId: 'o1', toolName: 'Edit', state: 'succeeded', endedAt: at(3.55), durationMs: 50 },
      { type: 'ended', at: at(5), status: 'completed', endReason: null, errorCode: null },
    ]
    const timeline = timelineHandler(agent.id, () => ({ run, entries, usage: run.usage }))
    const { user } = await renderChat({
      agent,
      messages: [makeMessage({ text: 'Check the record', run }), makeMessage({ role: 'assistant', text: 'Done', run })],
      handlers: [timeline.handler],
    })
    await user.click(await waitFor(recordToggle))
    const panel = document.getElementById(recordToggle().getAttribute('aria-controls')!)!
    const bashRow = (await within(panel).findByText('Tool Bash')).closest('li')!
    expect(bashRow.querySelector('code')?.textContent).toBe('go test ./internal/server/runrecord/...')
    const readRow = within(panel).getByText('Tool Read').closest('li')!
    expect(readRow.querySelector('code')?.textContent).toBe('internal/server/runrecord/tool_command.go')
    const grepRow = within(panel).getByText('Tool Grep').closest('li')!
    expect(grepRow.querySelector('code')?.textContent).toBe('withToolCommand')
    const editRow = within(panel).getByText('Tool Edit').closest('li')!
    expect(editRow.querySelector('code')).toBeNull()
    expect(within(editRow).queryByText(/Command not recorded/)).toBeNull()
  })
})

// "Printed sheet" redesign: the toggle bar is styled as a segmented receipt.
describe('run log toggle bar', () => {
  const parts = (toggle: HTMLElement) => [...toggle.querySelectorAll('[data-receipt-part]')].map((part) => part.textContent)

  it('once ended splits into three parts: name | duration | tokens, numbers in monospace; the button name is unchanged', async () => {
    const agent = makeAgent()
    const run = makeRun({ queuedAt: at(0), startedAt: at(0), runningAt: at(1), endedAt: at(9.4), usage: usage(1200, 34) })
    await renderChat({ agent, messages: [makeMessage({ text: 'Run the tests', run }), makeMessage({ role: 'assistant', text: 'The tests are done', run })] })
    const toggle = await waitFor(recordToggle)
    expect(toggle).toHaveAccessibleName('Run log · 9.4 s · 1,234 tokens')
    expect(parts(toggle)).toEqual(['Run log', '9.4 s', '1,234 tokens'])
    const [name, duration, tokens] = toggle.querySelectorAll('[data-receipt-part]')
    for (const part of [name, duration, tokens]) expect(part).toHaveAttribute('aria-hidden', 'true')
    expect(name).not.toHaveClass('font-mono')
    expect(duration).toHaveClass('font-mono', 'border-l-[1.5px]', 'border-dashed')
    expect(tokens).toHaveClass('font-mono', 'border-l-[1.5px]', 'border-dashed')
    // When it doesn't fit on a narrow screen, the segments wrap instead of extending past the background.
    expect(toggle).toHaveClass('flex-wrap', 'max-w-full')
  })

  it('a turn whose runtime reported no usage (Grok, Antigravity, or a turn that never reached a model) says so instead of “0 tokens”', async () => {
    const agent = makeAgent()
    const run = makeRun({ status: 'completed', queuedAt: at(0), startedAt: at(0), runningAt: at(1), endedAt: at(9.4), usage: usage(0, 0, 0, 0, 0), usageReported: false })
    const { handler } = timelineHandler(agent.id, () => ({ run, entries: [{ type: 'ended', at: at(9.4), status: 'completed', endReason: null, errorCode: null }], usage: run.usage }))
    const { user } = await renderChat({ agent, messages: [makeMessage({ text: 'Run the tests', run }), makeMessage({ role: 'assistant', text: 'The tests are done', run })], handlers: [handler] })
    const toggle = await waitFor(recordToggle)
    expect(toggle).toHaveAccessibleName('Run log · 9.4 s · no usage data')
    expect(parts(toggle)).toEqual(['Run log', '9.4 s', 'no usage data'])
    await user.click(toggle)
    const usageBlock = await screen.findByText("The runtime didn't report token usage for this turn.")
    expect(usageBlock.closest('div')).not.toHaveTextContent(/\b0 tokens/)
  })

  it('in progress has only two parts: name | elapsed time', async () => {
    const agent = makeAgent()
    const run = makeRun({ status: 'running', queuedAt: new Date(Date.now() - 5000).toISOString(), endedAt: null })
    const { emit } = await renderChat({ agent, messages: [makeMessage({ text: 'Hello', run })], status: { busy: true, currentRun: run } })
    await emit('snapshot', { status: makeStatus(agent.id, { busy: true, currentRun: run, processState: 'busy' }), currentText: '', queuedRuns: [], queueItems: [] })
    const toggle = await waitFor(recordToggle)
    expect(parts(toggle)).toHaveLength(2)
    expect(parts(toggle)[0]).toBe('Run log')
    expect(parts(toggle)[1]).toMatch(/^.+ so far$/)
  })
})
