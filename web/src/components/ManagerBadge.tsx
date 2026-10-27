import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// 项目 Manager 的标记：藏青底白字加旗子图标，不倾斜。列表卡片用 12px，详情头部用 13px。
export function ManagerBadge({ size = 'sm', className }: { size?: 'sm' | 'md'; className?: string }) {
  return (
    <Badge
      tone="role"
      title={t.agents.managerTitle}
      className={cn(size === 'md' ? 'gap-[5px] px-[11px] text-[13px] leading-[26px] [&>svg]:size-3!' : 'gap-1 px-[9px] text-[12px] [&>svg]:size-[11px]!', className)}
    >
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={3} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M5 21V4h11l-2 4 2 4H5" />
      </svg>
      {t.agents.manager}
    </Badge>
  )
}
