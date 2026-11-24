import { t } from '@/i18n'
import type { RuntimeDefinition, RuntimeProblem, RuntimeReadiness } from '@/lib/runtime'

// One runtime in the picker's rail: its definition, whether it can run here, and the version found on this computer.
export interface RailEntry {
  definition: RuntimeDefinition
  readiness: RuntimeReadiness
  version: string | null
}

// The short stamp for a runtime that can't run here. A command a person added whose program isn't found was never "installed" by
// anyone: it says the program can't be found.
export function problemStamp(definition: RuntimeDefinition, problem: RuntimeProblem) {
  const copy = t.runtime.picker
  return definition.acpCommandId && problem === 'not_found' ? copy.addedNotFound : copy.problem[problem]
}

// What a screen reader hears of a runtime's state.
export function stateText(entry: RailEntry) {
  const copy = t.runtime.picker
  const { readiness, definition } = entry
  if (readiness.state === 'problem') return problemStamp(definition, readiness.problem)
  if (readiness.state === 'checking') return copy.checking
  if (readiness.state === 'unknown') return copy.unknown
  return definition.capabilities.bundled ? `${copy.bundled}, ${copy.bundledNote}` : copy.ready
}
