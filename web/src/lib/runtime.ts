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

// The glyph inside a runtime's mark (components/runner-picker/RuntimeMark.tsx), as strokes on its 32×32 grid: a terminal prompt for Claude
// Code, a pair of braces for Codex, a lowercase g for Grok, rising chevrons for Antigravity, π for Pi. The mark's shape and the glyph's color follow the runtime's capabilities; a runtime without a glyph here is marked with
// its initial.
const glyphs: Partial<Record<AgentRuntime, readonly string[]>> = {
  'claude-code': ['M9.5 14.5l4.2 3.6-4.2 3.6', 'M16.4 22.4h6.4'],
  grok: ['M19.4 16.5a3.4 3.4 0 1 1-6.8 0a3.4 3.4 0 1 1 6.8 0', 'M19.4 13.6v7c0 2-1.3 3-3.3 3-1.3 0-2.3-.4-3-1.2'],
  antigravity: ['M11 21.6l5-4.4 5 4.4', 'M11 16.8l5-4.4 5 4.4'],
  codex: ['M12.8 12.6c-1.5 0-2.2.7-2.2 2v1.5c0 .9-.5 1.5-1.5 1.6 1 .1 1.5.7 1.5 1.6v1.5c0 1.3.7 2 2.2 2', 'M17.4 12.6c1.5 0 2.2.7 2.2 2v1.5c0 .9.5 1.5 1.5 1.6-1 .1-1.5.7-1.5 1.6v1.5c0 1.3-.7 2-2.2 2'],
  pi: ['M9.6 12.2c1.3-.9 2.6-1.2 4.2-1.2h8.8', 'M13.6 11.4c-.2 4-.9 7.4-2.4 10.4', 'M19.4 11.4c-.4 3.4-.3 6.3.4 8.2.5 1.3 1.6 1.9 2.8 1.3'],
}

export const runtimeGlyph = (id: AgentRuntime): readonly string[] | null => glyphs[id] ?? null
