import { useMemo } from 'react'
import type { Schemas } from '@/api/client'
import { useRuntimeDefinitions } from '@/api/queries'
import { t } from '@/i18n'

export type AgentRuntime = Schemas['AgentRuntime']
export type RuntimeDefinition = Schemas['RuntimeDefinition']

// The console's one place that knows runtimes by id. Everything else asks this lookup for a runtime's name or capabilities (GET
// /runtime-definitions) instead of comparing runtime ids, so adding a runtime adds no branches to the console;
// scripts/check-runtime-ids.mjs enforces it.
export interface RuntimeLookup {
  // The definition of a runtime; undefined while the definitions are being read, when they cannot be read, or for an id the server
  // does not define.
  get: (id: AgentRuntime | null | undefined) => RuntimeDefinition | undefined
  // The runtime that runs a model provider (contract RuntimeDefinition): the one that lists it, otherwise the default runtime.
  forProvider: (providerId: string) => RuntimeDefinition | undefined
  // The product name, shown as is in every language; the id until the definitions have been read.
  name: (id: AgentRuntime) => string
  // Why the definitions could not be read; null when they could, or are still being read.
  error: unknown
}

export function runtimeLookup(definitions: readonly RuntimeDefinition[] | undefined, error: unknown = null): RuntimeLookup {
  const list = definitions ?? []
  const get = (id: AgentRuntime | null | undefined) => (id == null ? undefined : list.find((definition) => definition.id === id))
  return {
    get,
    forProvider: (providerId) => list.find((definition) => definition.providers.includes(providerId)) ?? list.find((definition) => definition.isDefault),
    name: (id) => get(id)?.name ?? id,
    error,
  }
}

export function useRuntimeLookup(): RuntimeLookup {
  const { data, error } = useRuntimeDefinitions()
  return useMemo(() => runtimeLookup(data?.runtimes, error), [data, error])
}

// The model provider id of the Claude Code runtime. The runtime is not set separately; the model provider decides it (contract
// AgentRuntime): claude-code when the provider is claude-code, pi for everything else.
export const CLAUDE_CODE_PROVIDER = 'claude-code'

// Which runtime runs an Agent that uses a given model provider: the runtime the catalog gives when the provider is in it, otherwise
// (catalog not loaded, or provider not in it) derived by the contract's rule.
export function runtimeOfProvider(providerId: string, providers?: readonly Schemas['ModelProvider'][]): AgentRuntime {
  return providers?.find((provider) => provider.id === providerId)?.runtime ?? (providerId === CLAUDE_CODE_PROVIDER ? 'claude-code' : 'pi')
}

export type RuntimeProblem = 'not_found' | 'version_failed' | 'unavailable' | 'logged_out'

// Why a runtime cannot be used on this machine: not found, version unreadable, not logged in. An undeterminable login state (unknown)
// does not count; if a run fails, the server reports the error as it is.
export function runtimeProblem(status: Schemas['RuntimeStatus'] | undefined): RuntimeProblem | null {
  if (!status) return null
  if (!status.available) return status.unavailableReason ?? 'unavailable'
  return status.login?.state === 'logged_out' ? 'logged_out' : null
}

// What is wrong and how to fix it: the runtime's own wording when the copy has one (its command, how to log in), otherwise the wording
// for any runtime.
export function runtimeProblemText(runtime: Pick<RuntimeDefinition, 'id' | 'name'>, problem: RuntimeProblem): string {
  const copy = t.runtime.status
  return copy.problemsByRuntime[runtime.id]?.[problem] ?? copy.problems[problem](runtime.name)
}
