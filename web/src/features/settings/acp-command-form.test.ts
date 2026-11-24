import { describe, expect, it } from 'vitest'
import { acpCommand } from '@/test/runtimes'
import { argsOf, createBody, draftOf, formProblems, updateBody, type AcpCommandDraft } from './acp-command-form'

const filled = (overrides: Partial<AcpCommandDraft> = {}): AcpCommandDraft => ({ ...draftOf(), name: 'Hermes', command: 'hermes', ...overrides })
const newVar = (name: string, value: string) => ({ key: name, name, value, saved: false, refill: false })

describe('the form of a local ACP command', () => {
  it('arguments are one per line, kept exactly as written (quotes and spaces too); only blank lines are dropped', () => {
    expect(argsOf('acp\n--model "fast one"\n\n  --verbose \r\n   \n')).toEqual(['acp', '--model "fast one"', '  --verbose '])
    expect(argsOf('')).toEqual([])
  })

  it('needs a name and a command, environment names in letters, digits and _ (not starting with a digit), once each, and a value for each new one', () => {
    expect(formProblems(draftOf())).toMatchObject({ name: 'required', command: 'required' })
    const env = [newVar('OK_1', 'x'), { ...newVar('1BAD', 'x'), key: 'b' }, { ...newVar('OK_1', 'y'), key: 'c' }, { ...newVar('EMPTY', ''), key: 'd' }]
    expect(formProblems(filled({ env })).env).toEqual({ b: 'invalid', c: 'taken', d: 'value' })
    expect(formProblems(filled({ name: '  ', command: ' ' }))).toMatchObject({ name: 'required', command: 'required' })
    expect(formProblems(filled())).toEqual({ env: {} })
  })

  it('a new command sends its name, command, arguments and environment; a row left empty is ignored', () => {
    const draft = filled({ args: 'acp\n--stdio', env: [newVar('HERMES_API_KEY', 'sk-1'), { ...newVar('', ''), key: 'blank' }] })
    expect(formProblems(draft)).toEqual({ env: {} })
    expect(createBody(draft)).toEqual({ name: 'Hermes', command: 'hermes', args: ['acp', '--stdio'], env: { HERMES_API_KEY: 'sk-1' } })
  })

  it('a change sends only what changed; saved variables keep their values unless refilled or removed (the server merges env)', () => {
    const saved = acpCommand({ envNames: ['A_KEY', 'B_KEY'] })
    const draft = draftOf(saved)
    expect(draft).toMatchObject({ name: 'Hermes', command: 'hermes', args: 'acp\n--stdio' })
    expect(draft.env.map((row) => [row.name, row.saved, row.value])).toEqual([['A_KEY', true, ''], ['B_KEY', true, '']])
    expect(updateBody(draft, saved)).toEqual({})
    expect(formProblems(draft)).toEqual({ env: {} })

    // Refill one, remove the other, add a new one, rename the command: only those go.
    const [a] = draft.env
    const changed = { ...draft, name: 'Hermes 2', env: [{ ...a, refill: true, value: 'new' }, newVar('C_KEY', 'c')] }
    expect(updateBody(changed, saved)).toEqual({ name: 'Hermes 2', env: { A_KEY: 'new', C_KEY: 'c', B_KEY: null } })
    // A refill left empty is a value still to fill in.
    expect(formProblems({ ...draft, env: [{ ...a, refill: true, value: '' }] }).env).toEqual({ [a.key]: 'value' })
    // Arguments go as a whole when they change.
    expect(updateBody({ ...draft, args: 'acp' }, saved)).toEqual({ args: ['acp'] })
  })
})
