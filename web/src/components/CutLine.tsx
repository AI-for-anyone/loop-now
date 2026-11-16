import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'

// Cut line: an icon plus two pink dashed segments around a line of text. Used for the “会话已切换，页面历史保留” (Session switched; page
// history is kept) and “已打断，先处理这条” (Interrupted; handling this first) dividers on the chat page, and between visits to the same
// node in task details.
// The text can be plain text or a button or link (children); with children the divider is no longer named by label, and the control
// inside describes itself.
export function CutLine({ icon: Icon, label, children }: { icon: LucideIcon; label: string; children?: ReactNode }) {
  return (
    <div role={children ? undefined : 'separator'} aria-label={children ? undefined : label} className="flex items-center gap-2.5">
      <Icon aria-hidden="true" className="size-4 shrink-0 text-pink-text" strokeWidth={2.2} />
      <span aria-hidden="true" data-cut-line className="h-0 flex-1 border-t-2 border-dashed border-riso-pink" />
      {children ?? <span className="text-[12px] font-black text-pink-text">{label}</span>}
      <span aria-hidden="true" data-cut-line className="h-0 flex-1 border-t-2 border-dashed border-riso-pink" />
    </div>
  )
}
