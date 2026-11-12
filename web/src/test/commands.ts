import type { Schemas } from '@/api/client'

type Command = Schemas['AgentCommand']

const usages: [string, string][] = [
  ['session', '/session'],
  ['new', '/new'],
  ['compact', '/compact [instructions]'],
  ['reload', '/reload'],
  ['copy', '/copy'],
  ['export', '/export [html|jsonl]'],
  ['resume', '/resume [sessionId]'],
]

// GET /agents/{id}/commands 开头的 7 个平台内置命令（契约的固定顺序）。默认都可用；overrides 按命令名改，例如标成不可用。
export function platformCommands(overrides: Partial<Record<string, Partial<Command>>> = {}): Command[] {
  return usages.map(([name, usage]) => ({
    name,
    source: 'platform',
    piSource: '',
    description: null,
    pluginId: null,
    pluginDisplayName: null,
    usage,
    available: true,
    unavailableReason: null,
    ...overrides[name],
  }))
}

// Pi 的命令（扩展命令、提示模板、技能）：没有用法，恒可用。
export function piCommand(name: string, overrides: Partial<Command> = {}): Command {
  return { name, source: 'pi', piSource: 'extension', description: null, pluginId: null, pluginDisplayName: null, usage: null, available: true, unavailableReason: null, ...overrides }
}

// Claude Code 的 Agent：没有 Pi 会话，/compact、/export、/resume 不可用。
export const claudeCodeCommands = () =>
  platformCommands(Object.fromEntries(['compact', 'export', 'resume'].map((name) => [name, { available: false, unavailableReason: 'runtime_unsupported' as const }])))
