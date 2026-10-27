import { avatarPair } from '@/lib/avatar'
import { cn } from '@/lib/utils'

// 左侧栏是孔版蓝底：头像里的孔版蓝换成浅色版，叠印只在头像内部混色，不与底色相乘。
const onBlue = (color: string) => (color === 'var(--riso-blue)' ? 'var(--riso-blue-soft)' : color)

// 两个叠印的圆，只作装饰；名称在旁边照常显示。sm 是左侧栏一行里的小号（两个 16px 的圆）。
export function AgentAvatar({ id, size = 'md', className }: { id: string; size?: 'sm' | 'md'; className?: string }) {
  if (size === 'sm') {
    const [back, front] = avatarPair(id).map(onBlue)
    return (
      <span aria-hidden="true" className={cn('relative isolate inline-block h-4 w-[23px] shrink-0', className)}>
        <span className="absolute top-0 left-0 size-4 rounded-full" style={{ background: back }} />
        <span className="absolute top-0 left-[7px] size-4 rounded-full mix-blend-multiply" style={{ background: front }} />
      </span>
    )
  }
  const [back, front] = avatarPair(id)
  return (
    <span aria-hidden="true" className={cn('relative inline-block h-[34px] w-10 shrink-0', className)}>
      <span className="absolute top-0.5 left-0 size-7 rounded-full" style={{ background: back }} />
      <span className="absolute top-1.5 left-3 size-7 rounded-full mix-blend-multiply" style={{ background: front }} />
    </span>
  )
}
