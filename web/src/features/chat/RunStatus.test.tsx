import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { withCodex } from '@/test/runtimes'
import { server } from '@/test/server'
import { FailureNote, RunStatusBadge, ToolList } from './RunStatus'

const onClaudeCode: Schemas['RunSelection'] = { runtime: 'claude-code', provider: 'claude-code', modelId: 'haiku', thinkingLevel: null }

describe('ToolList', () => {
  it('shows only the latest state of a tool call that changed state several times', () => {
    render(
      <ToolList
        tools={[
          { toolCallId: 'call_1', toolName: 'bash', state: 'started', at: '2026-10-12T00:00:00Z' },
          { toolCallId: 'call_2', toolName: 'read', state: 'started', at: '2026-10-12T00:00:01Z' },
          { toolCallId: 'call_1', toolName: 'bash', state: 'succeeded', at: '2026-10-12T00:00:02Z' },
          { toolCallId: 'call_2', toolName: 'read', state: 'failed', at: '2026-10-12T00:00:03Z' },
        ]}
      />,
    )
    const items = screen.getAllByRole('listitem').map((li) => li.textContent)
    expect(items).toEqual(['bashSucceeded', 'readFailed'])
  })

  it('in a turn a person interrupted, tools left open read “Interrupted”, whether still on started or closed by the server as no_result', () => {
    render(
      <ToolList
        unfinished="interrupted"
        tools={[
          { toolCallId: 'call_1', toolName: 'bash', state: 'started', at: '2026-10-12T00:00:00Z' },
          { toolCallId: 'call_2', toolName: 'read', state: 'no_result', at: '2026-10-12T00:00:01Z' },
          { toolCallId: 'call_3', toolName: 'grep', state: 'succeeded', at: '2026-10-12T00:00:02Z' },
        ]}
      />,
    )
    expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['bashInterrupted', 'readInterrupted', 'grepSucceeded'])
  })

  it('spins the icon of a running tool; with reduced motion stops spinning and shows a static “…” instead', () => {
    const { container, rerender } = render(
      <ToolList tools={[{ toolCallId: 'call_1', toolName: 'bash', state: 'started', at: '2026-10-12T00:00:00Z' }]} />,
    )
    const spinner = container.querySelector('[data-slot="tool-spinner"]')!
    expect(spinner.getAttribute('class')).toContain('riso-spin')
    expect(spinner.getAttribute('class')).toContain('motion-reduce:animate-none')
    expect(spinner.getAttribute('class')).toContain('motion-reduce:hidden')
    const mark = container.querySelector('[data-slot="tool-static-mark"]')!
    expect(mark).toHaveTextContent('…')
    expect(mark.className).toContain('hidden')
    expect(mark.className).toContain('motion-reduce:inline')

    rerender(<ToolList tools={[{ toolCallId: 'call_1', toolName: 'bash', state: 'succeeded', at: '2026-10-12T00:00:01Z' }]} />)
    expect(container.querySelector('[data-slot="tool-spinner"]')).toBeNull()
    expect(container.querySelector('[data-slot="tool-static-mark"]')).toBeNull()
  })
})

describe('outcome stamp of a reply', () => {
  it('interrupted and cut off by a server restart use an outlined stamp, not red', () => {
    const { rerender } = render(<RunStatusBadge run={makeRun({ status: 'interrupted', endReason: 'user_interrupt' })} />)
    let stamp = screen.getByRole('status')
    expect(stamp).toHaveTextContent('Interrupted')
    expect(stamp).toHaveAttribute('data-variant', 'stamp')
    expect(stamp).toHaveAttribute('data-tone', 'interrupted')
    expect(stamp.className).not.toMatch(/destructive|red/)

    rerender(<RunStatusBadge run={makeRun({ status: 'interrupted', endReason: 'server_restart' })} />)
    stamp = screen.getByRole('status')
    expect(stamp).toHaveTextContent('Cut off by a server restart')
    expect(stamp).toHaveAttribute('data-variant', 'stamp')
    expect(stamp).toHaveAttribute('data-tone', 'restart')
    expect(stamp.className).not.toMatch(/destructive|red/)
  })

  it('failed uses a red stamp plus a light red note block giving the reason', () => {
    const run = makeRun({ status: 'failed', errorCode: 'pi_exited', errorMessage: 'exit status 137' })
    renderWithQuery(
      <>
        <RunStatusBadge run={run} />
        <FailureNote run={run} />
      </>,
    )
    const [stamp, note] = screen.getAllByRole('status')
    expect(stamp).toHaveTextContent(/^Failed$/)
    expect(stamp).toHaveAttribute('data-variant', 'stamp')
    expect(stamp).toHaveAttribute('data-tone', 'danger')
    expect(note).toHaveTextContent('Failed: Agent process exited unexpectedly (exit status 137)')
  })

  it.each([
    ['runtime_unavailable', 'claude: command not found', "Failed: Claude Code can't be used on this computer: it wasn't found, its version couldn't be read, or it's too old (claude: command not found)"],
    ['runtime_login_required', 'Not logged in', "Failed: Claude Code isn't logged in on this computer, or its login has expired: log in again in a terminal there, then try again (Not logged in)"],
    ['runtime_start_failed', 'claude 2.1.0: exit status 1', "Failed: Claude Code couldn't start (claude 2.1.0: exit status 1)"],
    ['runtime_exited', 'signal: killed', 'Failed: The Claude Code process exited unexpectedly during this turn (signal: killed)'],
    ['runtime_error', 'usage limit reached', 'Failed: Claude Code reported an error for this turn (usage limit reached)'],
  ] as const)('a runtime failure %s names the runtime the turn was handed to, with the server\'s message', async (errorCode, errorMessage, text) => {
    renderWithQuery(<FailureNote run={makeRun({ status: 'failed', errorCode, errorMessage, selection: onClaudeCode })} />)
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(text))
  })

  it("a turn that failed before it began names the Agent's runtime; with no runtime known it says “the runtime”", async () => {
    server.use(http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(withCodex())))
    const run = makeRun({ status: 'failed', errorCode: 'runtime_login_required', errorMessage: 'unauthorized', selection: null })
    const { unmount } = renderWithQuery(<FailureNote run={run} runtime="codex" />)
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent("Failed: Codex isn't logged in on this computer, or its login has expired"))
    unmount()
    renderWithQuery(<FailureNote run={run} />)
    expect(screen.getByRole('status')).toHaveTextContent("Failed: The runtime isn't logged in on this computer, or its login has expired")
  })

  it('a normal completion shows no stamp and no note block', () => {
    const run = makeRun({ status: 'completed' })
    const { container } = renderWithQuery(
      <>
        <RunStatusBadge run={run} />
        <FailureNote run={run} />
      </>,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('in progress shows only static phase text, without a spinning icon', () => {
    const { container } = render(<RunStatusBadge run={makeRun({ status: 'starting', phase: 'waiting_slot' })} />)
    expect(screen.getByRole('status')).toHaveTextContent('Waiting for a run slot')
    expect(container.querySelector('svg')).toBeNull()
    expect(container.innerHTML).not.toMatch(/animate-|riso-spin/)
  })
})
