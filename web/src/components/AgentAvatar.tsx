import { avatarPair } from '@/lib/avatar'
import { cn } from '@/lib/utils'

// The sidebar has a riso-blue background: the riso blue in the avatar is swapped for its light version, and the overprint only blends
// colors inside the avatar, without multiplying with the background.
const onBlue = (color: string) => (color === 'var(--riso-blue)' ? 'var(--riso-blue-soft)' : color)

// Two overprinted circles, purely decorative; the name is shown next to them as usual. sm is the small size for a sidebar row (two 16px
// circles).
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
