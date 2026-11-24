import type { Schemas } from '@/api/client'

type Command = Schemas['AcpCommand']

// One environment variable in the form. A saved one (saved) shows only its name, since the server never returns values; it keeps its value
// unless the person refills it (refill, with value) or removes the row. A new one has a name and a value.
export interface EnvRow {
  key: string
  name: string
  value: string
  saved: boolean
  refill: boolean
}

// The form as typed: the arguments one per line.
export interface AcpCommandDraft {
  name: string
  command: string
  args: string
  env: EnvRow[]
}

export type EnvProblem = 'invalid' | 'taken' | 'value'
export interface FormProblems {
  name?: 'required'
  command?: 'required'
  // By row key.
  env: Record<string, EnvProblem>
}

// The contract's rule for environment names (AcpCommandCreate.env): letters, digits and _, not starting with a digit.
const ENV_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/

let rows = 0
export const newEnvRow = (): EnvRow => ({ key: `new-${++rows}`, name: '', value: '', saved: false, refill: false })

export function draftOf(command?: Command): AcpCommandDraft {
  if (!command) return { name: '', command: '', args: '', env: [] }
  return {
    name: command.name,
    command: command.command,
    args: command.args.join('\n'),
    env: command.envNames.map((name) => ({ key: `saved-${name}`, name, value: '', saved: true, refill: false })),
  }
}

// One argument per line, exactly as written (the program gets each one as is, without a shell): quotes and spaces are part of it. Only
// a line with nothing visible on it is dropped.
export const argsOf = (text: string): string[] =>
  text
    .split('\n')
    .map((line) => line.replace(/\r$/, ''))
    .filter((line) => line.trim() !== '')

// A new row with neither name nor value is ignored.
const blank = (row: EnvRow) => !row.saved && row.name.trim() === '' && row.value === ''
const sends = (row: EnvRow) => !blank(row) && (!row.saved || row.refill)

export function formProblems(draft: AcpCommandDraft): FormProblems {
  const problems: FormProblems = { env: {} }
  if (draft.name.trim() === '') problems.name = 'required'
  if (draft.command.trim() === '') problems.command = 'required'
  const seen = new Set<string>()
  for (const row of draft.env) {
    if (blank(row)) continue
    const name = row.name.trim()
    if (!row.saved && !ENV_NAME.test(name)) problems.env[row.key] = 'invalid'
    else if (seen.has(name)) problems.env[row.key] = 'taken'
    else if (sends(row) && row.value === '') problems.env[row.key] = 'value'
    seen.add(name)
  }
  return problems
}

export const hasProblems = (problems: FormProblems) => Boolean(problems.name || problems.command || Object.keys(problems.env).length)

export function createBody(draft: AcpCommandDraft): Schemas['AcpCommandCreate'] {
  const env = Object.fromEntries(draft.env.filter(sends).map((row) => [row.name.trim(), row.value]))
  return { name: draft.name.trim(), command: draft.command.trim(), args: argsOf(draft.args), env }
}

// Only what changed. The server merges env: a value sets a variable, null removes it, and a saved variable left out keeps its value.
export function updateBody(draft: AcpCommandDraft, saved: Command): Schemas['AcpCommandUpdate'] {
  const body: Schemas['AcpCommandUpdate'] = {}
  const name = draft.name.trim(), command = draft.command.trim(), args = argsOf(draft.args)
  if (name !== saved.name) body.name = name
  if (command !== saved.command) body.command = command
  if (args.length !== saved.args.length || args.some((arg, index) => arg !== saved.args[index])) body.args = args
  const kept = new Set(draft.env.filter((row) => row.saved).map((row) => row.name))
  const env: Record<string, string | null> = Object.fromEntries(draft.env.filter(sends).map((row) => [row.name.trim(), row.value]))
  for (const gone of saved.envNames.filter((envName) => !kept.has(envName))) env[gone] = null
  if (Object.keys(env).length > 0) body.env = env
  return body
}
