import { avatarPair, translucentAvatarPalette } from '@/lib/avatar'
import { cn } from '@/lib/utils'
import { useTheme } from '@/theme/store'

const MONO_SIZES = {
  sm: { box: 'h-4 w-[23px]', back: 'top-0 left-0 size-4', front: 'top-0 left-[7px] size-4' },
  md: { box: 'h-[30px] w-9', back: 'top-0.5 left-0 size-6', front: 'top-1 left-3 size-6' },
  lg: { box: 'h-9 w-11', back: 'top-0.5 left-0 size-7', front: 'top-1.5 left-3.5 size-7' },
} as const

// Two overlapping circles, purely decorative; the name is shown next to them as usual.
// In mono skin: curated translucent optical-lens color pairs that blend harmonically on both light and dark surfaces.
// In Plate skin: classic Risograph overprinted ink pairs.
export function AgentAvatar({
  id,
  size = 'md',
  busy = false,
  sleeping = false,
  className,
}: {
  id: string
  name?: string
  size?: 'sm' | 'md' | 'lg'
  busy?: boolean
  sleeping?: boolean
  className?: string
}) {
  const { skin, resolvedTheme } = useTheme()

  if (skin !== 'plate') {
    const palette = translucentAvatarPalette(id)
    const [back, front] = palette[resolvedTheme]
    const cfg = MONO_SIZES[size]
    const blend = resolvedTheme === 'dark' ? 'mix-blend-screen' : 'mix-blend-multiply'
    return (
      <span
        aria-hidden="true"
        data-avatar-palette={palette.name}
        className={cn(
          'relative isolate inline-block shrink-0 select-none',
          cfg.box,
          sleeping && 'opacity-55',
          className,
        )}
      >
        <span
          className={cn('absolute rounded-full', cfg.back, busy && 'motion-safe:animate-pulse')}
          style={{ backgroundColor: back }}
        />
        <span
          className={cn('absolute rounded-full', cfg.front, blend, busy && 'motion-safe:animate-pulse')}
          style={{ backgroundColor: front }}
        />
      </span>
    )
  }

  if (size === 'sm') {
    const [back, front] = avatarPair(id)
    return (
      <span aria-hidden="true" className={cn('relative isolate inline-block h-4 w-[23px] shrink-0', sleeping && 'opacity-55', className)}>
        <span className="absolute top-0 left-0 size-4 rounded-full" style={{ background: back }} />
        <span className="absolute top-0 left-[7px] size-4 rounded-full mix-blend-multiply" style={{ background: front }} />
      </span>
    )
  }
  const [back, front] = avatarPair(id)
  return (
    <span aria-hidden="true" className={cn('relative inline-block h-[34px] w-10 shrink-0', sleeping && 'opacity-55', className)}>
      <span className="absolute top-0.5 left-0 size-7 rounded-full" style={{ background: back }} />
      <span className="absolute top-1.5 left-3 size-7 rounded-full mix-blend-multiply" style={{ background: front }} />
    </span>
  )
}
