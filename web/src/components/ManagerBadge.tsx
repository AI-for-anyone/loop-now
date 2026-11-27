import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// Flag glyph for the project Manager, shared by the header's ManagerBadge and the sidebar's compact status symbol.
export function ManagerFlag({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={3} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" className={className}>
      <path d="M5 21V4h11l-2 4 2 4H5" />
    </svg>
  )
}

// The project Manager marker: white text on navy with a flag icon, not tilted. 12px on list cards, 13px in the details header.
export function ManagerBadge({ size = 'sm', className }: { size?: 'sm' | 'md'; className?: string }) {
  return (
    <Badge
      tone="role"
      title={t.agents.managerTitle}
      className={cn(size === 'md' ? 'gap-[5px] px-[11px] text-[13px] leading-[26px] [&>svg]:size-3!' : 'gap-1 px-[9px] text-[12px] [&>svg]:size-[11px]!', className)}
    >
      <ManagerFlag />
      {t.agents.manager}
    </Badge>
  )
}
