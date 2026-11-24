import { Badge } from '@/components/ui/badge'
import { RuntimeGlyph } from '@/components/runner-picker/RuntimeMark'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { useRuntimeLookup } from '@/lib/runtime'

// The Agent's runtime marker: only Agents on a runtime installed on this computer are marked; a runtime that ships with the platform
// (Pi) is not, so the existing UI stays unchanged. Nothing is shown until the runtime definitions have been read.
// The Agent header uses a pill with the runtime's glyph and name; a sidebar row is narrow, so it gets only the glyph (hover and screen
// readers give the full name) in the row's text color. Each runtime has its own glyph (lib/runtime.ts), so Agents on different CLIs tell
// apart in the sidebar.
function useInstalledRuntime(runtime: Schemas['AgentRuntime']) {
  const definition = useRuntimeLookup().get(runtime)
  return definition && !definition.capabilities.bundled ? definition : undefined
}

export function RuntimeBadge({ runtime }: { runtime: Schemas['AgentRuntime'] }) {
  const definition = useInstalledRuntime(runtime)
  if (!definition) return null
  return (
    <Badge tone="info" title={t.runtime.badgeTitle(definition.name)} className="gap-1 px-[9px] text-[12px] [&>svg]:size-3.5!">
      <RuntimeGlyph runtime={definition} />
      {definition.name}
    </Badge>
  )
}

export function RuntimeMark({ runtime }: { runtime: Schemas['AgentRuntime'] }) {
  const definition = useInstalledRuntime(runtime)
  if (!definition) return null
  return (
    <span title={t.runtime.badgeTitle(definition.name)} className="inline-flex shrink-0 opacity-75">
      <RuntimeGlyph runtime={definition} className="size-4" />
      <span className="sr-only">{t.runtime.badgeTitle(definition.name)}</span>
    </span>
  )
}
