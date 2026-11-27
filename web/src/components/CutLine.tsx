import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'

// Cut line: clean 1px horizontal hairline divider in mono skin; icon plus two pink dashed segments in Plate skin.
// Used for session switch and interrupt dividers in chat, and between visits to the same node in task details.
// The text can be plain text or a button or link (children); with children the divider is no longer named by label, and the control
// inside describes itself.
export function CutLine({ icon: Icon, label, children }: { icon: LucideIcon; label: string; children?: ReactNode }) {
  return (
    <div role={children ? undefined : 'separator'} aria-label={children ? undefined : label} className="flex items-center gap-2.5">
      <Icon aria-hidden="true" className="size-4 shrink-0 text-ink-500" strokeWidth={2} />
      <span
        aria-hidden="true"
        data-cut-line
        className="h-0 flex-1 border-t border-hairline"
      />
      {children ?? <span className="text-[12px] font-medium text-ink-500">{label}</span>}
      <span
        aria-hidden="true"
        data-cut-line
        className="h-0 flex-1 border-t border-hairline"
      />
    </div>
  )
}
