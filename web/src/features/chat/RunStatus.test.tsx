import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { makeRun } from '@/test/api'
import { FailureNote, RunStatusBadge, ToolList } from './RunStatus'

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
    render(
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
    ['runtime_unavailable', 'claude: command not found', 'Failed: claude wasn\'t found on this computer: install Claude Code and make sure claude runs in a terminal (claude: command not found)'],
    ['runtime_login_required', 'Not logged in', 'Failed: Claude Code isn\'t logged in on this computer: run claude in a terminal there and log in (Not logged in)'],
    ['runtime_start_failed', 'claude 2.1.0: exit status 1', 'Failed: Claude Code couldn\'t start (claude 2.1.0: exit status 1)'],
    ['runtime_exited', 'signal: killed', 'Failed: The Claude Code process exited unexpectedly during this turn (signal: killed)'],
    ['runtime_error', 'usage limit reached', 'Failed: Claude Code reported an error for this turn (usage limit reached)'],
  ] as const)('Claude Code runtime failure %s: says what to do or what happened, with the server\'s message', (errorCode, errorMessage, text) => {
    render(<FailureNote run={makeRun({ status: 'failed', errorCode, errorMessage })} />)
    expect(screen.getByRole('status')).toHaveTextContent(text)
  })

  it('a normal completion shows no stamp and no note block', () => {
    const run = makeRun({ status: 'completed' })
    const { container } = render(
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
