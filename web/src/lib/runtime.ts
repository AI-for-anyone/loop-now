import type { Schemas } from '@/api/client'

export type AgentRuntime = Schemas['AgentRuntime']

// The model provider id of the Claude Code runtime. The runtime is not set separately; the model provider decides it (contract
// AgentRuntime): claude-code when the provider is claude-code, pi for everything else.
export const CLAUDE_CODE_PROVIDER = 'claude-code'

// Which runtime runs an Agent that uses a given model provider: the runtime the catalog gives when the provider is in it, otherwise
// (catalog not loaded, or provider not in it) derived by the contract's rule.
export function runtimeOfProvider(providerId: string, providers?: readonly Schemas['ModelProvider'][]): AgentRuntime {
  return providers?.find((provider) => provider.id === providerId)?.runtime ?? (providerId === CLAUDE_CODE_PROVIDER ? 'claude-code' : 'pi')
}

export type RuntimeProblem = 'not_found' | 'version_failed' | 'unavailable' | 'logged_out'

// Why Claude Code cannot be used on this machine: claude not found, version unreadable, not logged in. An undeterminable login state
// (unknown) does not count; if a run fails, the server reports the error as it is.
export function runtimeProblem(status: Schemas['RuntimeStatus'] | undefined): RuntimeProblem | null {
  if (!status) return null
  if (!status.available) return status.unavailableReason ?? 'unavailable'
  return status.login?.state === 'logged_out' ? 'logged_out' : null
}
