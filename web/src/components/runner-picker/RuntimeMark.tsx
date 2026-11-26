import antigravity from '@/assets/runtime-logos/antigravity.svg?raw'
import claude from '@/assets/runtime-logos/claude.svg?raw'
import grok from '@/assets/runtime-logos/grok.svg?raw'
import openai from '@/assets/runtime-logos/openai.svg?raw'
import pi from '@/assets/runtime-logos/pi.svg?raw'
import { runtimeLogo, type RuntimeDefinition, type RuntimeLogo } from '@/lib/runtime'
import { cn } from '@/lib/utils'

const FRAME = 32
const INSET = 6
const INNER = FRAME - INSET * 2

interface LogoShape {
  transform: string
  paths: readonly string[]
}

// Each official file in src/assets/runtime-logos/ keeps its source comment and holds a one-color silhouette (<svg viewBox="..."> with
// one or more <path d="..."/>). Fit its viewBox inside the frame's clear-space box, centered and preserving aspect ratio.
function parseShape(svg: string, name: RuntimeLogo): LogoShape {
  const viewBox = svg.match(/\bviewBox="([^"]+)"/)?.[1]?.trim().split(/[\s,]+/).map(Number)
  const paths = [...svg.matchAll(/<path\b[^>]*\bd="([^"]+)"/g)].map((match) => match[1])
  if (!viewBox || viewBox.length !== 4 || viewBox.some((n) => !Number.isFinite(n)) || viewBox[2] <= 0 || viewBox[3] <= 0 || paths.length === 0) {
    throw new Error(`runtime logo ${name} has no valid viewBox or path`)
  }
  const [vx, vy, vw, vh] = viewBox
  const scale = INNER / Math.max(vw, vh)
  const tx = INSET + (INNER - vw * scale) / 2 - vx * scale
  const ty = INSET + (INNER - vh * scale) / 2 - vy * scale
  return { transform: `translate(${tx} ${ty}) scale(${scale})`, paths }
}

const shapes: Record<RuntimeLogo, LogoShape> = {
  claude: parseShape(claude, 'claude'),
  openai: parseShape(openai, 'openai'),
  grok: parseShape(grok, 'grok'),
  antigravity: parseShape(antigravity, 'antigravity'),
  pi: parseShape(pi, 'pi'),
}

// A runtime's mark: one ink-colored rounded square frame with the product's one-color shape inside in paper color (lib/runtime.ts says
// which shape). Nothing else varies between runtimes; a runtime without a shape of its own (a local ACP command a person added, or an
// unknown id) shows its initial in the same frame.
export function RuntimeMark({ runtime, className }: { runtime: RuntimeDefinition; className?: string }) {
  const logo = runtimeLogo(runtime.id)
  const shape = logo ? shapes[logo] : null
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" data-logo={logo ?? undefined} className={cn('size-8 shrink-0', className)}>
      <rect x="0" y="0" width="32" height="32" rx="8" fill="var(--ink)" />
      {shape ? (
        <g transform={shape.transform}>
          {shape.paths.map((d, index) => (
            <path key={index} d={d} fill="var(--paper-raised)" />
          ))}
        </g>
      ) : (
        <text x="16" y="22.8" textAnchor="middle" fontSize="19" fontWeight="800" fontFamily="var(--font-display-latin)" fill="var(--paper-raised)">
          {initial(runtime)}
        </text>
      )}
    </svg>
  )
}

// The same mark small, for tight places (a sidebar row, the Agent header's runtime badge, the prompt writer's line): same ink frame, scaled.
export function RuntimeGlyph({ runtime, className }: { runtime: RuntimeDefinition; className?: string }) {
  return <RuntimeMark runtime={runtime} className={cn('size-4', className)} />
}

const initial = (runtime: RuntimeDefinition) => runtime.name.trim().slice(0, 1).toUpperCase() || '?'
