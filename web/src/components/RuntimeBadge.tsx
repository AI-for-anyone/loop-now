import { TerminalIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

// The Agent's runtime marker: only Claude Code Agents are marked; Pi is the default runtime and is not marked, so the existing UI stays
// unchanged.
// The Agent header uses a pill with text; a sidebar row is narrow, so it gets only a small icon (hover and screen readers give the full
// name) in the row's text color.
export function RuntimeBadge({ runtime }: { runtime: Schemas['AgentRuntime'] }) {
  if (runtime !== 'claude-code') return null
  return (
    <Badge tone="info" title={t.runtime.badgeTitle} className="gap-1 px-[9px] text-[12px] [&>svg]:size-3!">
      <TerminalIcon aria-hidden="true" />
      {t.runtime.badge}
    </Badge>
  )
}

export function RuntimeMark({ runtime }: { runtime: Schemas['AgentRuntime'] }) {
  if (runtime !== 'claude-code') return null
  return (
    <span title={t.runtime.badgeTitle} className="inline-flex shrink-0 opacity-75">
      <TerminalIcon aria-hidden="true" className="size-3.5" />
      <span className="sr-only">{t.runtime.badgeTitle}</span>
    </span>
  )
}
