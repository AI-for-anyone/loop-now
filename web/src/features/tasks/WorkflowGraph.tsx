import { useLayoutEffect, useRef, useState, useId } from 'react'
import type { Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

type Props = { detail: Schemas['TaskDetail']; selected: string | null; onSelect: (stepId: string) => void }
const CARD = 208, GAP = 112, HEIGHT = 136
export function WorkflowGraph({ detail, selected, onSelect }: Props) {
  const container = useRef<HTMLDivElement>(null)
  const marker = useId().replace(/:/g, '')
  const camera = useRef({ x: 0, y: 0, scale: 1 })
  const [view, setView] = useState({ x: 0, y: 0, scale: 1 })
  const initialized = useRef('')
  const drag = useRef<{ x: number; y: number; left: number; top: number } | null>(null)
  const { steps, edges } = detail.workflow
  const upper = edges.filter(e => steps.findIndex(s => s.id === e.to) > steps.findIndex(s => s.id === e.from) + 1)
  const lower = edges.filter(e => steps.findIndex(s => s.id === e.to) <= steps.findIndex(s => s.id === e.from))
  const top = 76 + upper.length * 56
  const width = Math.max(320, steps.length * (CARD + GAP) - GAP + 96)
  const height = top + HEIGHT + 76 + lower.length * 56
  const x = (id: string) => 48 + steps.findIndex(s => s.id === id) * (CARD + GAP)
  const commit = (next: typeof view) => { camera.current = next; setView(next) }
  const locate = (id: string | null) => {
    if (!id || !container.current) return
    commit({ x: container.current.clientWidth / 2 - x(id) - CARD / 2, y: container.current.clientHeight / 2 - top - HEIGHT / 2, scale: 1 })
  }
  useLayoutEffect(() => {
    const el = container.current
    if (!el) return
    const init = () => {
      if (!el.clientWidth || initialized.current === detail.task.id) return
      initialized.current = detail.task.id
      const id = selected ?? detail.task.currentStepId ?? steps[0]?.id
      if (id) commit({ x: el.clientWidth / 2 - x(id) - CARD / 2, y: el.clientHeight / 2 - top - HEIGHT / 2, scale: 1 })
    }
    init()
    const observer = new ResizeObserver(init)
    observer.observe(el)
    return () => observer.disconnect()
    // Initialize once per task. Selection, SSE and resize preserve the person's camera.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [detail.task.id, steps.length])
  const zoom = (factor: number) => {
    const el = container.current
    if (!el) return
    const old = camera.current, scale = Math.max(.12, Math.min(2, old.scale * factor)), ratio = scale / old.scale
    commit({ scale, x: el.clientWidth / 2 - (el.clientWidth / 2 - old.x) * ratio, y: el.clientHeight / 2 - (el.clientHeight / 2 - old.y) * ratio })
  }
  return <section className="flex h-[430px] min-w-0 flex-col overflow-hidden rounded-panel bg-paper-raised p-3 shadow-print-rest lg:h-full">
    <div className="mb-3 flex flex-wrap items-center justify-between gap-2"><h2 className="font-display-cn text-[26px]">{t.tasks.workflow}</h2><div className="flex flex-wrap gap-2">
      <Button variant="secondary" size="sm" aria-label={t.tasks.zoomOut} onClick={() => zoom(1 / 1.2)}>−</Button><Button variant="secondary" size="sm" aria-label={t.tasks.zoomIn} onClick={() => zoom(1.2)}>+</Button>
      <Button variant="secondary" size="sm" onClick={() => { const el = container.current; if (!el) return; const scale = Math.min(1, (el.clientWidth - 24) / width, (el.clientHeight - 24) / height); commit({ scale, x: (el.clientWidth - width * scale) / 2, y: (el.clientHeight - height * scale) / 2 }) }}>{t.tasks.fit}</Button>
      <Button variant="secondary" size="sm" disabled={!detail.task.currentStepId} onClick={() => locate(detail.task.currentStepId)}>{t.tasks.locateCurrent}</Button>
    </div></div>
    <div ref={container} tabIndex={0} aria-label={t.tasks.canvas} className="relative min-h-0 flex-1 touch-none overflow-hidden rounded-card bg-tint-blue outline-none focus-visible:shadow-focus" onKeyDown={event => {
      const offsets: Record<string, [number, number]> = { ArrowLeft: [40, 0], ArrowRight: [-40, 0], ArrowUp: [0, 40], ArrowDown: [0, -40] }
      if (event.target !== event.currentTarget || !offsets[event.key]) return
      event.preventDefault(); const [dx, dy] = offsets[event.key]; commit({ ...camera.current, x: camera.current.x + dx, y: camera.current.y + dy })
    }} onPointerDown={event => {
      if (event.button !== 0 || (event.target as HTMLElement).closest('button')) return
      drag.current = { x: event.clientX, y: event.clientY, left: camera.current.x, top: camera.current.y }; event.currentTarget.setPointerCapture(event.pointerId)
    }} onPointerMove={event => { const d = drag.current; if (d) commit({ ...camera.current, x: d.left + event.clientX - d.x, y: d.top + event.clientY - d.y }) }} onPointerUp={() => { drag.current = null }} onPointerCancel={() => { drag.current = null }}>
      <div className="absolute origin-top-left" data-task-camera style={{ width, height, transform: `translate(${view.x}px, ${view.y}px) scale(${view.scale})` }}>
        <svg aria-hidden="true" width={width} height={height} className="absolute inset-0 text-ink-500"><defs><marker id={marker} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto"><path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" /></marker></defs>
          {edges.map(edge => {
            const start = x(edge.from) + CARD, end = x(edge.to), back = end < start, lane = back ? lower.indexOf(edge) : upper.indexOf(edge)
            const direct = !back && lane < 0
            const y = direct ? top + HEIGHT / 2 : back ? top + HEIGHT + 42 + lane * 56 : top - 42 - lane * 56
            const self = edge.from === edge.to
            const startX = direct ? start : x(edge.from) + CARD * (self ? .75 : .5), endX = direct ? end : x(edge.to) + CARD * (self ? .25 : .5)
            const startY = direct ? y : back ? top + HEIGHT : top
            const labelX = direct ? (start + end) / 2 - 52 : (startX + endX) / 2 - 90
            return <g key={edge.id} data-edge-id={edge.id}><path d={direct ? `M ${startX} ${y} H ${endX - 4}` : `M ${startX} ${startY} V ${y} H ${endX} V ${startY + (back ? 4 : -4)}`} fill="none" stroke="currentColor" strokeWidth="2" strokeDasharray={back ? '6 4' : undefined} markerEnd={`url(#${marker})`} />
              <foreignObject x={labelX} y={y - 44} width={direct ? 104 : 180} height="40"><div className="rounded-input bg-white px-2 py-1 text-center text-xs font-bold break-words text-ink" title={edge.label}>{edge.label}{edge.maxTraversals ? ` · ${t.tasks.loop(edge.maxTraversals)}` : ''}</div></foreignObject></g>
          })}
        </svg>
        {steps.map(step => { const record = detail.nodes.filter(n => n.stepId === step.id).sort((a, b) => a.visit - b.visit || a.attempt - b.attempt).at(-1); return <button key={step.id} type="button" aria-pressed={selected === step.id} onClick={() => onSelect(step.id)} className={cn('absolute rounded-card bg-white p-3 text-left outline-none focus-visible:shadow-focus', selected === step.id ? 'shadow-[inset_0_0_0_2px_var(--ink)]' : 'shadow-print-rest')} style={{ left: x(step.id), top, width: CARD, height: HEIGHT }}>
          <span className="line-clamp-2 font-bold" title={step.title}>{step.title}</span>{record && <span className="mt-1 block truncate text-xs">{record.agentName}</span>}<span className="block text-sm">{record ? t.tasks.nodeStatuses[record.status] : t.tasks.unvisited}</span><span className="mt-1 flex flex-wrap gap-2 text-xs font-black">{detail.task.currentStepId === step.id && <span className="text-riso-blue">{t.tasks.currentNode}</span>}{selected === step.id && <span>{t.tasks.viewing}</span>}</span>
        </button> })}
      </div>
    </div>
    <p className="mt-2 text-xs text-ink-500">{t.tasks.panHint}</p>
  </section>
}
