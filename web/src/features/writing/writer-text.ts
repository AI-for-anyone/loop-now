import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import type { RuntimeLookup } from '@/lib/runtime'

// Who writes, in one line: "Claude Code · Claude Haiku 4.5", with the thinking level when one is set.
export function writerName(writer: Schemas['TextGenerationWriter'], lookup: RuntimeLookup): string {
  const level = writer.thinkingLevel ? (t.config.basic.thinking.levels[writer.thinkingLevel] ?? writer.thinkingLevel) : null
  return [lookup.name(writer.runtime), writer.modelName || writer.modelId, level].filter(Boolean).join(' · ')
}

// The one chosen to write, when it cannot write now: the runtime that lists its provider (the provider id when none does any more) and the
// model id.
export function selectionName(selection: Schemas['TextGenerationSelection'], lookup: RuntimeLookup): string {
  const runtime = lookup.all.find((definition) => definition.providers.includes(selection.provider))
  return `${runtime?.name ?? selection.provider} · ${selection.modelId}`
}
