import { TerminalIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

// Agent 的运行时标记：只标 Claude Code 的 Agent，Pi 是默认运行时，不标，存量界面不变。
// Agent 头部用带文字的胶囊；左侧栏一行很窄，只放一个小图标（悬停与读屏给出全称），随行的文字颜色。
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
