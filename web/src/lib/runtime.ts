import type { Schemas } from '@/api/client'

export type AgentRuntime = Schemas['AgentRuntime']

// Claude Code 运行时的模型提供方 id。运行时不单独设置，由模型提供方决定（契约 AgentRuntime）：
// 提供方为 claude-code 时是 claude-code，其余都是 pi。
export const CLAUDE_CODE_PROVIDER = 'claude-code'

// 选用某个模型提供方的 Agent 由哪个运行时执行：目录里有这个提供方时用它给的 runtime，没有（目录未加载或不在目录里）时按契约的规则推出。
export function runtimeOfProvider(providerId: string, providers?: readonly Schemas['ModelProvider'][]): AgentRuntime {
  return providers?.find((provider) => provider.id === providerId)?.runtime ?? (providerId === CLAUDE_CODE_PROVIDER ? 'claude-code' : 'pi')
}
