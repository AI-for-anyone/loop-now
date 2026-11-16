import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'

// 裁切线：图标加两段粉色虚线夹着一行说明。对话页的“会话已重开”“打断”，任务详情里同一节点各次访问之间都用它。
// 说明可以是一段文字，也可以是一个按钮或链接（children）；有 children 时分隔线不再用 label 命名，由里面的控件自己说明。
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
