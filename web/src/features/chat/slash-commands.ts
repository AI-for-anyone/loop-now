import type { Schemas } from '@/api/client'
import { localizedText } from '@/lib/localized-text'

export type Command = Schemas['AgentCommand']

// Only a message that starts with exactly "/pi:<name>" is handed to Pi to run (the server rewrites it to "/<name>"); other messages
// starting with "/" are sent as usual.
export const PI_PREFIX = '/pi:'

// What gets written into the input box on completion: Pi commands are /pi:<name>, built-in platform commands are /<name> (in the menu
// a platform command shows its usage instead).
export function commandText(command: Command): string {
  return command.source === 'pi' ? `${PI_PREFIX}${command.name}` : `/${command.name}`
}

// When the caret is still in the first word and that word starts with "/", return the word (the menu filters by it); otherwise null.
// Leading whitespace doesn't count.
export function slashToken(text: string, caret: number): string | null {
  if (!text.startsWith('/')) return null
  const space = text.search(/\s/)
  const end = space === -1 ? text.length : space
  return caret <= end ? text.slice(0, end) : null
}

// A command's description (contract AgentCommand.description): for Pi commands it's the extension's original text; platform commands
// have a Chinese and an English description, picked by UI language; an empty string when there is none.
export const commandDescription = (command: Command): string => (command.description === null ? '' : localizedText(command.description))

// Filter and sort by the typed word: first those whose completed form (e.g. pi:<name>) starts with it, then those whose command name
// starts with it (pi: not typed),
// then those whose name or description contains it. Within the same tier, keep the order the server gave.
export function matchCommands(commands: Command[], token: string): Command[] {
  const query = token.slice(1).toLowerCase()
  const rank = (command: Command) => {
    const name = command.name.toLowerCase()
    if (commandText(command).slice(1).toLowerCase().startsWith(query)) return 0
    if (name.startsWith(query)) return 1
    if (name.includes(query) || commandDescription(command).toLowerCase().includes(query)) return 2
    return null
  }
  return commands
    .map((command, index) => ({ command, index, rank: rank(command) }))
    .filter((item): item is { command: Command; index: number; rank: number } => item.rank !== null)
    .sort((a, b) => a.rank - b.rank || a.index - b.index)
    .map((item) => item.command)
}

// Replace the first word with the command's form (e.g. "/pi:<name>") and leave the rest of the text alone; if no whitespace follows,
// add a space; the caret lands after the whitespace, ready for arguments.
export function applyCommand(text: string, command: Command): { text: string; caret: number } {
  const space = text.search(/\s/)
  const rest = space === -1 ? '' : text.slice(space)
  const head = commandText(command)
  return { text: rest ? head + rest : `${head} `, caret: head.length + 1 }
}
