import { t } from '@/i18n'
import type { RuntimeDefinition, RuntimeReadiness } from '@/lib/runtime'

// One runtime in the picker's rail: its definition, whether it can run here, and the version found on this computer.
export interface RailEntry {
  definition: RuntimeDefinition
  readiness: RuntimeReadiness
  version: string | null
}

// What a screen reader hears of a runtime's state.
export function stateText(entry: RailEntry) {
  const copy = t.runtime.picker
  const { readiness, definition } = entry
  if (readiness.state === 'problem') return copy.problem[readiness.problem]
  if (readiness.state === 'checking') return copy.checking
  if (readiness.state === 'unknown') return copy.unknown
  return definition.capabilities.bundled ? `${copy.bundled}, ${copy.bundledNote}` : copy.ready
}
