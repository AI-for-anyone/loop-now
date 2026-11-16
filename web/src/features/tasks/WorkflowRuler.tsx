import { Fragment, useLayoutEffect, useRef, useState } from 'react'
import { CheckIcon, Undo2Icon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { AgentAvatar } from '@/components/AgentAvatar'
import { RulerMark, RulerTicks, Stub } from '@/components/Sheet'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { edgeUses, stepNumber, workflowPosition, workflowSteps, type StepState, type StepView } from './workflow-steps'

type Props = {
  detail: Schemas['TaskDetail']
  // The selected step (step in the URL) and the visit being viewed (record).
  selected: string | null
  record: string | undefined
  onSelect: (stepId: string) => void
  onRecord: (stepId: string, recordId: string) => void
  agentName: (agentId: string) => string
}

const stateText: Record<StepState, string> = { done: 'text-green-text', current: 'text-ink', visited: 'text-ink-700', todo: 'text-ink-500' }

// Each step's status label: a visited step shows the status of its latest visit (blocked in red, completed in green); one not reached
// yet says “还没到” (not yet).
function StepStatus({ view }: { view: StepView }) {
  if (!view.latest) return <span className="text-[11.5px] font-bold text-ink-500">· {t.tasks.ruler.todo}</span>
  const status = view.latest.status
  return (
    <span className={cn('text-[11.5px] font-black', status === 'blocked' ? 'text-red-text' : status === 'completed' ? 'text-green-text' : status === 'running' || status === 'submitted' ? 'text-pink-text' : 'text-ink-700')}>
      · {t.tasks.nodeStatuses[status]}
    </span>
  )
}

// Dots on the ruler: done is a navy dot with a check mark, the current step a big yellow dot with a fluorescent-pink offset shadow (showing
// the step number), visited but not completed a hollow dot with a navy outline, and not reached a dashed circle.
function StepDot({ view }: { view: StepView }) {
  if (view.state === 'current') {
    return (
      <span data-step-dot={view.index} aria-hidden="true" className="absolute top-1.5 left-[25px] flex size-[38px] items-center justify-center rounded-full border-[2.5px] border-ink bg-riso-yellow font-display-latin text-[14px] font-extrabold shadow-[3px_3px_0_var(--riso-pink)]">
        {view.number}
      </span>
    )
  }
  return (
    <span
      data-step-dot={view.index}
      aria-hidden="true"
      className={cn(
        'absolute top-3 left-[31px] flex size-[26px] items-center justify-center rounded-full border-2',
        view.state === 'done' ? 'border-ink bg-ink text-white' : view.state === 'visited' ? 'border-ink bg-paper-raised' : 'border-dashed border-ink-500 bg-paper-raised',
      )}
    >
      {view.state === 'done' && <CheckIcon className="size-[13px]" strokeWidth={3.2} />}
    </span>
  )
}

interface Geometry {
  height: number
  dots: { x: number; y: number }[]
}

// Workflow diagram (redesign step 5): a vertical ruler in the stub on the right, replacing the old draggable, zoomable canvas. Each step
// is a dot on the ruler,
// with the edge conditions written between steps; edges that don't lead to the next step, such as rework, are written under the step
// they start from, and rework also gets a pink dashed arc.
// Clicking a step selects it; the selected step lists its visits below it, and clicking one jumps to that visit. The step being viewed
// gets a stroke of yellow highlighter and a small pink arrow on the left.
// With many steps the ruler scrolls up and down on its own, keeping the current step in view. The big number at the bottom is which step
// has been reached. On narrow screens the stub is hidden and WorkflowChips takes its place.
export function WorkflowRuler({ detail, selected, record, onSelect, onRecord, agentName }: Props) {
  const views = workflowSteps(detail)
  const position = workflowPosition(detail)
  const scroller = useRef<HTMLDivElement>(null)
  const content = useRef<HTMLDivElement>(null)
  const [geometry, setGeometry] = useState<Geometry>({ height: 0, dots: [] })
  const currentStep = detail.task.currentStepId

  // The vertical line and the arcs must connect to each step's dot: measure each dot's center after layout, and measure again when the
  // window or content changes.
  useLayoutEffect(() => {
    const el = content.current
    if (!el) return
    const measure = () => {
      const box = el.getBoundingClientRect()
      const dots = [...el.querySelectorAll<HTMLElement>('[data-step-dot]')].map((dot) => {
        const rect = dot.getBoundingClientRect()
        return { x: rect.left - box.left, y: rect.top - box.top + rect.height / 2 }
      })
      setGeometry((old) => (old.height === el.offsetHeight && JSON.stringify(old.dots) === JSON.stringify(dots) ? old : { height: el.offsetHeight, dots }))
    }
    measure()
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure)
    observer?.observe(el)
    return () => observer?.disconnect()
  })

  // When the current step changes (or the page just opened), scroll it into the ruler's visible area; once the user scrolls on their own,
  // don't interfere.
  useLayoutEffect(() => {
    const box = scroller.current
    const target = box?.querySelector<HTMLElement>('[aria-current="step"]')
    if (!box || !target) return
    const top = target.offsetTop
    if (top < box.scrollTop || top + target.offsetHeight > box.scrollTop + box.clientHeight) box.scrollTop = Math.max(0, top - box.clientHeight / 3)
  }, [currentStep])

  const reached = views.reduce((last, view) => (view.state === 'todo' ? last : view.index), 0)
  const dot = (index: number) => geometry.dots[index]
  const lineX = 44
  return (
    <Stub as="nav" aria-label={t.tasks.workflow} label={t.tasks.workflow} className="w-[256px] gap-2.5 pb-4">
      <div ref={scroller} className="relative min-h-0 flex-1 overflow-y-auto overscroll-contain no-scrollbar">
        <div ref={content} className="relative flex flex-col pb-3">
          <RulerTicks className="bg-[repeating-linear-gradient(to_bottom,transparent_0,transparent_9px,var(--ink)_9px,var(--ink)_11px)]" />
          {geometry.dots.length === views.length && views.length > 0 && (
            <svg aria-hidden="true" className="pointer-events-none absolute top-0 left-0 overflow-visible" width={60} height={geometry.height}>
              <defs>
                <marker id="ruler-arrow-back" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto">
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--riso-pink)" />
                </marker>
                <marker id="ruler-arrow-skip" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto">
                  <path d="M 0 0 L 10 5 L 0 10 z" fill="var(--ink-500)" />
                </marker>
              </defs>
              {/* The part already traveled is a solid navy line; the part not reached yet is a light-ink dashed line. */}
              <line x1={lineX} x2={lineX} y1={dot(0).y} y2={dot(reached).y} stroke="var(--ink)" strokeWidth={3} />
              {reached < views.length - 1 && <line x1={lineX} x2={lineX} y1={dot(reached).y} y2={dot(views.length - 1).y} stroke="var(--ink-500)" strokeWidth={3} strokeDasharray="7 5" />}
              {views.flatMap((view) =>
                view.jumps.map((jump, lane) => {
                  const from = dot(view.index), to = dot(jump.to)
                  const bend = Math.max(2, 10 - lane * 5)
                  const path =
                    jump.to === view.index
                      ? `M ${from.x} ${from.y - 6} C ${bend} ${from.y - 26}, ${bend} ${from.y + 26}, ${from.x} ${from.y + 6}`
                      : `M ${from.x} ${from.y} C ${bend} ${from.y}, ${bend} ${to.y}, ${to.x - 1} ${to.y}`
                  return (
                    <path
                      key={jump.edge.id}
                      data-jump-edge={jump.edge.id}
                      d={path}
                      fill="none"
                      stroke={jump.back ? 'var(--riso-pink)' : 'var(--ink-500)'}
                      strokeWidth={2}
                      strokeDasharray="6 5"
                      markerEnd={`url(#${jump.back ? 'ruler-arrow-back' : 'ruler-arrow-skip'})`}
                    />
                  )
                }),
              )}
            </svg>
          )}
          {views.map((view) => {
            const isSelected = selected === view.step.id
            const name = view.latest?.agentName ?? agentName(view.step.agentId)
            return (
              <Fragment key={view.step.id}>
                <div data-ruler-step={view.step.id} className="relative">
                  {/* The highlighter mark covers the whole step (even when English text wraps to three lines). */}
                  {isSelected && <RulerMark className="top-0.5 right-1 bottom-0.5 left-[62px]" />}
                  <button
                    type="button"
                    aria-pressed={isSelected}
                    aria-current={view.state === 'current' ? 'step' : undefined}
                    onClick={() => onSelect(view.step.id)}
                    className="relative flex min-h-[50px] w-full cursor-pointer flex-col justify-center py-1 pr-[18px] pl-[78px] text-left outline-none focus-visible:shadow-focus"
                  >
                    <span className="flex min-w-0 items-baseline gap-[7px]">
                      <span className="font-mono text-[11px] font-bold text-ink-500">{view.number}</span>
                      <span className={cn('min-w-0 text-[14.5px] leading-[1.3] font-black [overflow-wrap:anywhere]', stateText[view.state === 'todo' ? 'todo' : 'current'])}>{view.step.title}</span>
                    </span>
                    <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[12px] font-bold text-ink-700">
                      <AgentAvatar size="sm" id={view.step.agentId} />
                      <span className="min-w-0 truncate">{name}</span>
                      {/* Status and visit count are one unit: when they don't fit, they wrap to the next line together, so "×2" is never
                          left dangling on its own. */}
                      <span data-step-state className="inline-flex items-baseline gap-x-1.5 whitespace-nowrap">
                        <StepStatus view={view} />
                        {view.records.length > 1 && <span className="font-mono text-[11px] text-ink-500">×{view.records.length}</span>}
                      </span>
                    </span>
                  </button>
                  <StepDot view={view} />
                </div>
                {isSelected && view.records.length > 0 && (
                  <ol aria-label={t.tasks.selectRecord} className="relative mt-0.5 mb-1 ml-[78px] flex flex-col">
                    {view.records.map((item) => {
                      const here = item.id === record
                      return (
                        <li key={item.id}>
                          <button
                            type="button"
                            aria-current={here ? 'true' : undefined}
                            onClick={() => onRecord(view.step.id, item.id)}
                            className={cn('flex min-h-[26px] cursor-pointer items-center gap-2 text-left text-[12.5px] text-ink outline-none focus-visible:shadow-focus', here ? 'font-black' : 'font-bold')}
                          >
                            <span aria-hidden="true" className="mt-[11px] h-0.5 w-2.5 shrink-0 self-start bg-ink" />
                            {/* “在看” (viewing) wraps along with the text (so longer English text doesn't push it aside). */}
                            <span className="min-w-0 py-0.5 leading-[1.6]">
                              <span className={cn(here && 'underline decoration-2 underline-offset-4')}>
                                {t.tasks.ruler.visit(item.visit, item.attempt)} · {t.tasks.nodeStatuses[item.status]}
                              </span>
                              {here && <span className="text-[11.5px] font-black whitespace-nowrap text-pink-text"> · {t.tasks.ruler.here}</span>}
                            </span>
                          </button>
                        </li>
                      )
                    })}
                  </ol>
                )}
                {view.jumps.map((jump) => (
                  <p key={jump.edge.id} className={cn('relative mt-0.5 ml-[78px] flex items-center gap-1.5 pr-4 text-[11.5px] font-black', jump.back ? 'text-pink-text' : 'text-ink-500')}>
                    <Undo2Icon aria-hidden="true" className="size-[13px] shrink-0" strokeWidth={2.6} />
                    <span className="min-w-0 [overflow-wrap:anywhere]">
                      {t.tasks.ruler.jump(jump.edge.label, stepNumber(jump.to))}
                      {jump.edge.maxTraversals ? ` · ${t.tasks.ruler.used(jump.used, jump.edge.maxTraversals)}` : ''}
                    </span>
                  </p>
                ))}
                {view.index < views.length - 1 &&
                  (view.next.length > 0 ? (
                    <p className="relative ml-[78px] flex min-h-[30px] items-center pr-4 text-[11.5px] font-bold text-ink-500 [overflow-wrap:anywhere]">
                      ↓ {view.next.map((edge) => edge.label + (edge.maxTraversals ? ` · ${t.tasks.ruler.used(edgeUses(detail, edge.id), edge.maxTraversals)}` : '')).join(' / ')}
                    </p>
                  ) : (
                    <span aria-hidden="true" className="h-3.5" />
                  ))}
              </Fragment>
            )
          })}
        </div>
      </div>
      <div className="flex shrink-0 flex-col items-end gap-0.5 pr-[22px]">
        <span className="font-display-latin text-[40px] leading-none font-extrabold text-riso-blue">
          {position.at}
          <span className="text-[22px] text-ink-500"> / {position.total}</span>
        </span>
        <span className="text-[12.5px] font-black text-ink-700">{position.done ? t.tasks.ruler.done : position.at ? t.tasks.ruler.at(position.at) : t.tasks.ruler.notStarted}</span>
      </div>
    </Stub>
  )
}

// Narrow screens (when the stub is hidden): a horizontally scrollable row of small step tags above the index tabs, with status colors
// matching the ruler; a click likewise selects the step;
// the step being viewed has a small pink arrow in front of it.
export function WorkflowChips({ detail, selected, onSelect }: Pick<Props, 'detail' | 'selected' | 'onSelect'>) {
  const views = workflowSteps(detail)
  const row = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    const el = row.current
    const chip = el?.querySelector<HTMLElement>('[aria-pressed="true"]')
    if (el && chip && (chip.offsetLeft < el.scrollLeft || chip.offsetLeft + chip.offsetWidth > el.scrollLeft + el.clientWidth)) el.scrollLeft = chip.offsetLeft - 12
  }, [selected])
  return (
    <nav ref={row} aria-label={t.tasks.workflow} data-workflow-chips className="flex shrink-0 gap-2 overflow-x-auto px-0.5 pt-1 pb-3 no-scrollbar">
      {views.map((view) => {
        const isSelected = selected === view.step.id
        return (
          <button
            key={view.step.id}
            type="button"
            aria-pressed={isSelected}
            aria-current={view.state === 'current' ? 'step' : undefined}
            onClick={() => onSelect(view.step.id)}
            className={cn(
              'inline-flex h-8 shrink-0 cursor-pointer items-center gap-1.5 rounded-full border-2 px-3 text-[12.5px] font-black whitespace-nowrap outline-none focus-visible:shadow-focus',
              view.state === 'done' && 'border-ink bg-ink text-white',
              view.state === 'current' && 'mr-[3px] border-ink bg-riso-yellow text-ink shadow-[2px_2px_0_var(--riso-pink)]',
              view.state === 'visited' && 'border-ink bg-paper-raised text-ink',
              view.state === 'todo' && 'border-dashed border-ink-500 text-ink-500',
            )}
          >
            {isSelected && <span aria-hidden="true" className="size-0 border-y-[5px] border-l-[7px] border-y-transparent border-l-riso-pink" />}
            {view.state === 'done' && <CheckIcon aria-hidden="true" className="size-3" strokeWidth={3.2} />}
            <span className="font-mono text-[11px]">{view.number}</span>
            {view.step.title}
          </button>
        )
      })}
    </nav>
  )
}
