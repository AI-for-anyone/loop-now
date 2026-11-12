import type { Schemas } from '@/api/client'

type Command = Schemas['AgentCommand']

// 与服务端的平台命令表一致（internal/server/conversation/console_commands.go）：用法与中英两种说明。
const specs: [string, string, string, string][] = [
  ['session', '/session', '查看当前会话、模型和进程状态', 'Show the current session, model, and process'],
  ['new', '/new', '重开会话，保留页面里的对话历史', "Start a new session and keep the page's history"],
  ['compact', '/compact [instructions]', '压缩当前会话的上下文；instructions 是给这次压缩的附加说明，可省略', 'Compact the current context; instructions are optional guidance for this compaction'],
  ['reload', '/reload', '停止当前进程，下一条消息时重新加载', 'Stop the process; it reloads on the next message'],
  ['copy', '/copy', '复制最近一条有文字的成功回复', 'Copy the latest successful reply that has text'],
  ['export', '/export [html|jsonl]', '导出当前会话，格式为 html（默认）或 jsonl', 'Export the current session as html (default) or jsonl'],
  ['resume', '/resume [session-id]', '不带 session-id 时列出可恢复的会话；带上完整的 session-id 时恢复那个会话', 'Without session-id, list resumable sessions; with a full session-id, resume that session'],
]

// GET /agents/{id}/commands 开头的 7 个平台内置命令（契约的固定顺序）。默认都可用；overrides 按命令名改，例如标成不可用。
export function platformCommands(overrides: Partial<Record<string, Partial<Command>>> = {}): Command[] {
  return specs.map(([name, usage, zh, en]) => ({
    name,
    source: 'platform',
    piSource: '',
    description: { 'zh-CN': zh, 'en-US': en },
    pluginId: null,
    pluginDisplayName: null,
    usage,
    available: true,
    unavailableReason: null,
    ...overrides[name],
  }))
}

// Pi 的命令（扩展命令、提示模板、技能）：没有用法，恒可用；说明是扩展给的原文。
export function piCommand(name: string, overrides: Partial<Command> = {}): Command {
  return { name, source: 'pi', piSource: 'extension', description: null, pluginId: null, pluginDisplayName: null, usage: null, available: true, unavailableReason: null, ...overrides }
}

// 命令列表（契约 AgentCommandList）：默认是 Pi 的 Agent、Pi 还没启动过、只有平台命令。
export function commandList(commands: Command[] = platformCommands(), overrides: Partial<Schemas['AgentCommandList']> = {}): Schemas['AgentCommandList'] {
  return { runtime: 'pi', capturedAt: null, commands, ...overrides }
}

// Claude Code 的 Agent：没有 Pi 会话，/compact、/export、/resume 不可用，也没有 Pi 命令。
export const claudeCodeCommands = () =>
  platformCommands(Object.fromEntries(['compact', 'export', 'resume'].map((name) => [name, { available: false, unavailableReason: 'runtime_unsupported' as const }])))
export const claudeCodeList = () => commandList(claudeCodeCommands(), { runtime: 'claude-code' })
