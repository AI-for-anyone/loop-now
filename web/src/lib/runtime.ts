import { useMemo } from 'react'
import type { Schemas } from '@/api/client'
import { useRuntimeDefinitions } from '@/api/queries'
import { t } from '@/i18n'

export type AgentRuntime = Schemas['AgentRuntime']
export type RuntimeDefinition = Schemas['RuntimeDefinition']

// The runtimes that come with the server. Any other id is a runtime a person added (a local ACP command, contract AgentRuntime),
// known only by what GET /runtime-definitions says about it. scripts/check-runtime-ids.mjs reads the ids from this list.
export const BUILT_IN_RUNTIMES = ['pi', 'claude-code', 'codex', 'grok', 'antigravity'] as const
export type BuiltInRuntime = (typeof BUILT_IN_RUNTIMES)[number]

// The console's one place that knows runtimes by id. Everything else asks this lookup for a runtime's name or capabilities (GET
// /runtime-definitions) instead of comparing runtime ids, so adding a runtime adds no branches to the console;
// scripts/check-runtime-ids.mjs enforces it.
export interface RuntimeLookup {
  // Every definition, in the server's order (the default runtime first).
  all: readonly RuntimeDefinition[]
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
    all: list,
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

export type RuntimeProblem = 'not_found' | 'version_failed' | 'version_unsupported' | 'unavailable' | 'logged_out'

// Why a runtime cannot be used on this machine: not found, version unreadable, older than the runtime supports, not logged in. An
// undeterminable login state (unknown) does not count; if a run fails, the server reports the error as it is.
export function runtimeProblem(status: Schemas['RuntimeStatus'] | undefined): RuntimeProblem | null {
  if (!status) return null
  if (!status.available) return status.unavailableReason ?? 'unavailable'
  return status.login?.state === 'logged_out' ? 'logged_out' : null
}

// What is wrong and how to fix it: the runtime's own wording when the copy has one (its command, how to log in), otherwise the wording
// for any runtime. A CLI that is too old is said with the version it needs (minVersion) and the one found (version, from GET /runtimes).
export function runtimeProblemText(runtime: Pick<RuntimeDefinition, 'id' | 'name' | 'minVersion'>, problem: RuntimeProblem, version: string | null = null): string {
  const copy = t.runtime.status
  if (problem === 'version_unsupported') return copy.versionUnsupported(runtime.name, runtime.minVersion, version)
  return copy.problemsByRuntime[runtime.id]?.[problem] ?? copy.problems[problem](runtime.name)
}

// Whether a runtime can run an Agent on this computer right now, for the runner picker. A bundled runtime is always ready. One installed
// on this computer is as GET /runtimes reports it: checking while that is being read, unknown when it cannot be read, and otherwise ready
// or one of the problems above (an undeterminable login state counts as ready, as in runtimeProblem).
export type RuntimeReadiness = { state: 'ready' } | { state: 'checking' } | { state: 'unknown' } | { state: 'problem'; problem: RuntimeProblem }

export interface RuntimeStatusRead {
  status: Schemas['RuntimeStatus'] | undefined
  checking: boolean
  failed: boolean
}

export function runtimeReadiness(definition: RuntimeDefinition, read: RuntimeStatusRead): RuntimeReadiness {
  if (definition.capabilities.bundled) return { state: 'ready' }
  if (read.status) {
    const problem = runtimeProblem(read.status)
    return problem ? { state: 'problem', problem } : { state: 'ready' }
  }
  return read.checking ? { state: 'checking' } : { state: 'unknown' }
}

// The status of one runtime in GET /runtimes.
export const statusOf = (statuses: readonly Schemas['RuntimeStatus'][] | undefined, definition: RuntimeDefinition) => statuses?.find((status) => status.id === definition.id)

// Only a known problem keeps a runtime from being chosen: while its status is being read, or when it cannot be read, the choice is left to
// the person, and a run that fails says why.
export const canRun = (readiness: RuntimeReadiness) => readiness.state !== 'problem'

// The order of the picker's runtime rail: runtimes installed on this computer first, those that can run on top, those with a problem
// after them; bundled runtimes last. Within each group, the server's order.
export function railOrder(definitions: readonly RuntimeDefinition[], readiness: (definition: RuntimeDefinition) => RuntimeReadiness): RuntimeDefinition[] {
  const rank = (definition: RuntimeDefinition) => {
    if (definition.capabilities.bundled) return 3
    const state = readiness(definition).state
    return state === 'ready' ? 0 : state === 'problem' ? 2 : 1
  }
  return definitions.map((definition, index) => ({ definition, index, rank: rank(definition) })).sort((a, b) => a.rank - b.rank || a.index - b.index).map(({ definition }) => definition)
}

// The product mark each built-in runtime shows: the product's own logo (Claude for Claude Code, OpenAI's for Codex, Grok's, Google
// Antigravity's, Pi's own pixel mark), drawn by components/runner-picker/RuntimeMark.tsx from the official files in
// src/assets/runtime-logos. Any other runtime (a local ACP command a person added) has no product logo of its own and gets a neutral mark.
export type RuntimeLogo = 'claude' | 'openai' | 'grok' | 'antigravity' | 'pi'
const logos: Record<BuiltInRuntime, RuntimeLogo> = { 'claude-code': 'claude', codex: 'openai', grok: 'grok', antigravity: 'antigravity', pi: 'pi' }

export const runtimeLogo = (id: AgentRuntime): RuntimeLogo | null => (Object.hasOwn(logos, id) ? logos[id as BuiltInRuntime] : null)
