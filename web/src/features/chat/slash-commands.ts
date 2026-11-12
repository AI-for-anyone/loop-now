import type { Schemas } from '@/api/client'

export type Command = Schemas['AgentCommand'] | (Omit<Schemas['AgentCommand'], 'source'> & { source: 'platform'; usage: string })

// 只有消息开头严格是 "/pi:<name>" 才交给 Pi 执行（服务端改写成 "/<name>"）；其余 "/" 开头的消息照常发出。
export const PI_PREFIX = '/pi:'

// 菜单里显示、补全时写进输入框的写法：Pi 的命令是 /pi:<name>。以后若有别的来源（例如平台自己的命令），写成 /<name>。
export function commandText(command: Command): string {
  return command.source === 'pi' ? `${PI_PREFIX}${command.name}` : `/${command.name}`
}

// 光标还在第一个词里、这个词以 "/" 开头时，返回这个词（菜单按它筛选）；否则为 null。开头有空格不算。
export function slashToken(text: string, caret: number): string | null {
  if (!text.startsWith('/')) return null
  const space = text.search(/\s/)
  const end = space === -1 ? text.length : space
  return caret <= end ? text.slice(0, end) : null
}

// 按输入的词筛选并排序：补全后的写法（如 pi:<name>）以它开头的最前，其次是命令名以它开头的（没写 pi:），
// 再次是名称或说明里包含它的。同一档里保持服务端给的顺序。
export function matchCommands(commands: Command[], token: string): Command[] {
  const query = token.slice(1).toLowerCase()
  const rank = (command: Command) => {
    const name = command.name.toLowerCase()
    if (commandText(command).slice(1).toLowerCase().startsWith(query)) return 0
    if (name.startsWith(query)) return 1
    if (name.includes(query) || (command.description ?? '').toLowerCase().includes(query)) return 2
    return null
  }
  return commands
    .map((command, index) => ({ command, index, rank: rank(command) }))
    .filter((item): item is { command: Command; index: number; rank: number } => item.rank !== null)
    .sort((a, b) => a.rank - b.rank || a.index - b.index)
    .map((item) => item.command)
}

// 把第一个词换成命令的写法（如 "/pi:<name>"），其余文字不动；后面没有空白时补一个空格，光标停在空白之后，接着写参数。
export function applyCommand(text: string, command: Command): { text: string; caret: number } {
  const space = text.search(/\s/)
  const rest = space === -1 ? '' : text.slice(space)
  const head = commandText(command)
  return { text: rest ? head + rest : `${head} `, caret: head.length + 1 }
}
