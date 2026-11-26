import { Badge } from '@/components/ui/badge'
import { RuntimeGlyph } from '@/components/runner-picker/RuntimeMark'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { useRuntimeLookup } from '@/lib/runtime'

// The Agent's runtime marker: only Agents on a runtime installed on this computer are marked; a runtime that ships with the platform
// (Pi) is not, so the existing UI stays unchanged. Nothing is shown until the runtime definitions have been read.
// The Agent header uses a pill with the runtime's logo and name; a sidebar row is narrow, so it gets only the logo (hover and screen
// readers give the full name). Each runtime shows its product's own logo (lib/runtime.ts), so Agents on different CLIs tell apart in the
// sidebar. The sidebar is blue, and logos keep their own colors (black for OpenAI and Grok), so the logo sits on a small paper tile.
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
    <span title={t.runtime.badgeTitle(definition.name)} className="grid size-5 shrink-0 place-items-center rounded-[5px] bg-paper-raised text-ink">
      <RuntimeGlyph runtime={definition} className="size-3.5" />
      <span className="sr-only">{t.runtime.badgeTitle(definition.name)}</span>
    </span>
  )
}
