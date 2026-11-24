import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { makeMessage, makeRun } from '@/test/api'
import { renderChat } from '@/test/chat'
import { runtimeName } from '@/test/runtimes'
import { buildSteps } from './timeline-steps'

type Selection = Schemas['RunSelection']
const pi = (modelId: string, provider = 'anthropic'): Selection => ({ runtime: 'pi', provider, modelId, thinkingLevel: null })
const claude = (modelId: string): Selection => ({ runtime: 'claude-code', provider: 'claude-code', modelId, thinkingLevel: 'high' })
const handoff = (overrides: Partial<Schemas['ContextHandoff']> = {}): Schemas['ContextHandoff'] => ({ reason: 'runtime_changed', fromRuntime: 'pi', toRuntime: 'claude-code', includedMessages: 12, omittedMessages: 30, images: 2, bytes: 9812, budgetBytes: 16000, ...overrides })

// One turn: the user's message and the reply, on a run with this selection (and handoff).
let clock = 0
const turn = (text: string, selection: Selection | null, contextHandoff?: Schemas['ContextHandoff']) => {
  clock += 1
  const at = `2026-11-23T08:${String(clock).padStart(2, '0')}:00Z`
  const run = makeRun({ selection, ...(contextHandoff ? { contextHandoff } : {}), queuedAt: at, startedAt: at, runningAt: at, endedAt: at })
  return [makeMessage({ text, run, createdAt: at }), makeMessage({ role: 'assistant', text: `Reply to ${text}`, run, createdAt: at })]
}
const follows = (first: Element, second: Element) => Boolean(first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING)

describe('changes of who runs it, in the conversation', () => {
  it('a change of runtime shows where the conversation was handed over, once, before the turn that carried it', async () => {
    await renderChat({
      messages: [
        ...turn('First question', pi('claude-sonnet-4-5')),
        // The first try after the switch failed and the block went again with the next message: one row, before the first.
        ...turn('Now on Claude Code', claude('haiku'), handoff()),
        ...turn('Try again', claude('haiku'), handoff()),
      ],
    })
    const rows = screen.getAllByRole('separator', { name: /^Handed over from Pi to Claude Code/ })
    expect(rows).toHaveLength(1)
    expect(rows[0]).toHaveAccessibleName('Handed over from Pi to Claude Code: 12 earlier messages went along, 30 left out, 2 images not carried')
    expect(rows[0]).toHaveTextContent('Pi → Claude Code')
    expect(rows[0]).toHaveTextContent('12 earlier messages went along')
    expect(rows[0]).toHaveTextContent('30 left out')
    expect(follows(screen.getByText('First question'), rows[0])).toBe(true)
    expect(follows(rows[0], screen.getByText('Now on Claude Code'))).toBe(true)
    // No model-change divider where the runtime changed.
    expect(screen.queryByRole('separator', { name: /^Model changed/ })).toBeNull()
  })

  it('a handover into a new session of the same runtime (the change was undone after the other one had run) says so', async () => {
    await renderChat({ messages: [...turn('On Claude', claude('haiku')), ...turn('Back on Pi', pi('claude-sonnet-4-5'), handoff({ fromRuntime: 'pi', toRuntime: 'pi', omittedMessages: 0, includedMessages: 4, images: 0 }))] })
    const row = screen.getByRole('separator', { name: /^Handed over/ })
    expect(row).toHaveAccessibleName('Handed over to a new Pi session: all 4 earlier messages went along')
    expect(row).toHaveTextContent('Pi · new session')
  })

  it('a session that was lost says the earlier conversation went into a new session of the same runtime', async () => {
    await renderChat({ messages: [...turn('Hello', claude('haiku')), ...turn('Still there?', claude('haiku'), handoff({ reason: 'session_lost', fromRuntime: 'claude-code', toRuntime: 'claude-code', includedMessages: 2, omittedMessages: 0, images: 0 }))] })
    const row = screen.getByRole('separator', { name: /session/ })
    expect(row).toHaveAccessibleName("Claude Code's previous session couldn't be found, so a new one started with the earlier conversation: all 2 earlier messages went along")
    expect(row).toHaveTextContent('Claude Code · new session')
    expect(row).toHaveTextContent("The previous session couldn't be found; a new one started with the earlier conversation")
  })

  it('a conversation that went whole says so', async () => {
    await renderChat({ messages: [...turn('Hello', pi('claude-sonnet-4-5')), ...turn('On Claude', claude('haiku'), handoff({ includedMessages: 2, omittedMessages: 0, images: 0 }))] })
    expect(screen.getByRole('separator', { name: /^Handed over/ })).toHaveAccessibleName('Handed over from Pi to Claude Code: all 2 earlier messages went along')
  })

  it('a change of model on the same runtime shows a small divider from one to the other', async () => {
    await renderChat({
      messages: [
        ...turn('On Sonnet', pi('claude-sonnet-4-5')),
        ...turn('Still Sonnet', pi('claude-sonnet-4-5')),
        ...turn('On GPT', pi('openai/gpt-5', 'openrouter')),
        // An older record has no selection: nothing is drawn next to it.
        ...turn('Older record', null),
        ...turn('After it', pi('claude-haiku-4-5')),
      ],
    })
    const dividers = screen.getAllByRole('separator', { name: /^Model changed/ })
    expect(dividers).toHaveLength(1)
    expect(dividers[0]).toHaveAccessibleName('Model changed from anthropic · claude-sonnet-4-5 to openrouter · openai/gpt-5')
    expect(follows(screen.getByText('Still Sonnet'), dividers[0])).toBe(true)
    expect(follows(dividers[0], screen.getByText('On GPT'))).toBe(true)
  })

  it('the run record lists the handover as a step, named by why it happened', () => {
    const steps = buildSteps(null, [{ type: 'context_handoff', at: '2026-11-23T08:00:00Z', ...handoff() }], runtimeName)
    expect(steps.map((step) => [step.label, step.detail])).toEqual([['Conversation handed over', 'Pi → Claude Code · 12 messages went along, 30 left out, 2 images not carried · 9.8K of a 16K budget']])
    const same = { fromRuntime: 'claude-code', toRuntime: 'claude-code', omittedMessages: 0, images: 0 } as const
    const lost = buildSteps(null, [{ type: 'context_handoff', at: '2026-11-23T08:00:00Z', ...handoff({ reason: 'session_lost', ...same }) }], runtimeName)
    expect(lost.map((step) => [step.label, step.detail])).toEqual([['Session not found; restarted with the conversation', 'Claude Code · new session · 12 messages went along · 9.8K of a 16K budget']])
    const task = buildSteps(null, [{ type: 'context_handoff', at: '2026-11-23T08:00:00Z', ...handoff({ reason: 'task_start', ...same }) }], runtimeName)
    expect(task[0].label).toBe('Started with a selection of the regular conversation')
  })
})
