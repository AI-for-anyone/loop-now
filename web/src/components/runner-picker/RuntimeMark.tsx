import antigravity from '@/assets/runtime-logos/antigravity.svg'
import claude from '@/assets/runtime-logos/claude.svg'
import grok from '@/assets/runtime-logos/grok.svg'
import openai from '@/assets/runtime-logos/openai.svg'
import pi from '@/assets/runtime-logos/pi.svg'
import { runtimeLogo, type RuntimeDefinition, type RuntimeLogo } from '@/lib/runtime'
import { cn } from '@/lib/utils'

// The official files, each with a comment saying where and when it was taken. They are shown as they are: their own colors and
// proportions, never recolored into the console's inks or outlined. The files of Claude, OpenAI and Grok are already their brands'
// one-color marks; Google Antigravity and Pi have no one-color version, so their full-color marks are used at every size.
const files: Record<RuntimeLogo, string> = { claude, openai, grok, antigravity, pi }

// A runtime's mark: its product's own logo inside a square that keeps clear space around it (lib/runtime.ts says which logo). A runtime
// without a logo of its own, such as a local ACP command a person added, gets a neutral mark drawn in the console's riso language: a blue
// window with its initial.
export function RuntimeMark({ runtime, className }: { runtime: RuntimeDefinition; className?: string }) {
  const logo = runtimeLogo(runtime.id)
  if (logo) {
    return (
      <span aria-hidden="true" className={cn('grid size-8 shrink-0 place-items-center', className)}>
        <img data-logo={logo} src={files[logo]} alt="" draggable={false} className="size-[78%] object-contain" />
      </span>
    )
  }
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" className={cn('size-8 shrink-0 overflow-visible', className)}>
      <rect x="4.2" y="5.6" width="25" height="22" rx="5" fill="var(--riso-pink)" />
      <rect x="2.6" y="4" width="25" height="22" rx="5" fill="var(--riso-blue)" style={{ mixBlendMode: 'multiply' }} />
      {/* The window's title bar: a rule and two dots in paper color. */}
      <path d="M2.6 10h25" stroke="var(--paper-raised)" strokeWidth="1.4" opacity=".55" />
      <circle cx="6.4" cy="7.1" r="1.1" fill="var(--paper-raised)" />
      <circle cx="9.8" cy="7.1" r="1.1" fill="var(--paper-raised)" opacity=".7" />
      <text x="15.1" y="21.6" textAnchor="middle" fontSize="12" fontWeight="800" fontFamily="var(--font-display-latin)" fill="var(--paper-raised)">
        {initial(runtime)}
      </text>
    </svg>
  )
}

// The mark small, for tight places (the Agent header's runtime badge, a sidebar row, the prompt writer's line): the logo itself, filling
// the box, on whatever light ground the place gives it; a runtime without a logo shows its initial in the text color.
export function RuntimeGlyph({ runtime, className }: { runtime: RuntimeDefinition; className?: string }) {
  const logo = runtimeLogo(runtime.id)
  if (logo) return <img data-logo={logo} src={files[logo]} alt="" aria-hidden="true" draggable={false} className={cn('size-3.5 shrink-0 object-contain', className)} />
  return (
    <svg viewBox="7 9.5 18 15" aria-hidden="true" className={cn('size-3.5 shrink-0', className)}>
      <text x="16" y="22" textAnchor="middle" fontSize="14" fontWeight="800" fontFamily="var(--font-display-latin)" fill="currentColor">
        {initial(runtime)}
      </text>
    </svg>
  )
}

const initial = (runtime: RuntimeDefinition) => runtime.name.slice(0, 1).toUpperCase()
