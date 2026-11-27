import { Fragment, useLayoutEffect, useRef, useState } from 'react'
import { CheckIcon, Undo2Icon } from 'lucide-react'
import type { Schemas } from '@/api/client'
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

const stateText: Record<StepState, string> = {
  done: 'text-ink-700',
  current: 'text-ink',
  visited: 'text-ink-700',
  todo: 'text-ink-500',
}

// Each step's compact status label: a visited step shows the status of its latest visit; one not reached yet says "not yet".
function StepStatus({ view }: { view: StepView }) {
  if (!view.latest) return <span className="text-[11px] font-medium text-ink-500">· {t.tasks.ruler.todo}</span>
  const status = view.latest.status
  return (
    <span
      className={cn(
        'text-[11px] font-semibold',
        status === 'blocked'
          ? 'text-red-text'
          : status === 'completed'
            ? 'text-green-text'
            : status === 'running' || status === 'submitted'
              ? 'text-pink-text'
              : 'text-ink-700',
      )}
    >
      · {t.tasks.nodeStatuses[status]}
    </span>
  )
}

// Compact 16px status indicator aligned on the 1.5px vertical rail.
function StepDot({ view }: { view: StepView }) {
  if (view.state === 'current') {
    const blocked = view.latest?.status === 'blocked'
    return (
      <span
        data-step-dot={view.index}
        aria-hidden="true"
        className={cn(
          'absolute top-[9px] left-1 flex size-4 items-center justify-center rounded-full border-[1.5px] bg-tint-yellow',
          blocked ? 'border-red-text bg-tint-red' : 'border-ink',
        )}
      >
        <span className={cn('size-1.5 rounded-full', blocked ? 'bg-red-text' : 'bg-ink')} />
      </span>
    )
  }
  return (
    <span
      data-step-dot={view.index}
      aria-hidden="true"
      className={cn(
        'absolute top-[9px] left-1 flex size-4 items-center justify-center rounded-full border',
        view.state === 'done'
          ? 'border-transparent bg-cta-bg text-cta-fg'
          : view.state === 'visited'
            ? 'border-hairline-strong bg-surface-elevated'
            : 'border-hairline-strong bg-surface-canvas',
      )}
    >
      {view.state === 'done' && <CheckIcon className="size-2.5" strokeWidth={3} />}
      {view.state === 'visited' && <span className="size-1.5 rounded-full bg-ink-500" />}
    </span>
  )
}

interface Geometry {
  height: number
  dots: number[]
}

// Left step rail (216px on lg+): compact 16px status indicators on a 1.5px vertical rail, 1-line step title + muted agent/status
// subline, header step counter (e.g. 2 / 4), nested visit/attempt rows under the selected step, and inline rework/jump badges.
// On < lg screens the rail is hidden and WorkflowChips renders steps + visit pills horizontally above the pane.
export function WorkflowRuler({ detail, selected, record, onSelect, onRecord, agentName }: Props) {
  const views = workflowSteps(detail)
  const position = workflowPosition(detail)
  const scroller = useRef<HTMLDivElement>(null)
  const content = useRef<HTMLDivElement>(null)
  const [geometry, setGeometry] = useState<Geometry>({ height: 0, dots: [] })
  const currentStep = detail.task.currentStepId
  const layoutKey = `${views.map((v) => `${v.step.id}:${v.state}:${v.records.length}:${v.jumps.length}:${v.next.length}`).join('|')}|${selected ?? ''}|${record ?? ''}`

  // Measure each dot's vertical center after layout so the 1.5px rail connects cleanly across expanded visit lists and edge labels.
  useLayoutEffect(() => {
    const el = content.current
    if (!el) return
    const measure = () => {
      const box = el.getBoundingClientRect()
      const dots = [...el.querySelectorAll<HTMLElement>('[data-step-dot]')].map((dot) => {
        const rect = dot.getBoundingClientRect()
        return rect.top - box.top + rect.height / 2
      })
      setGeometry((old) =>
        old.height === el.offsetHeight && old.dots.length === dots.length && old.dots.every((y, i) => Math.abs(y - dots[i]) < 0.5)
          ? old
          : { height: el.offsetHeight, dots },
      )
    }
    measure()
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure)
    observer?.observe(el)
    return () => observer?.disconnect()
  }, [layoutKey])

  // Keep the active step visible when it changes without overriding manual scrolling afterwards.
  useLayoutEffect(() => {
    const box = scroller.current
    const target = box?.querySelector<HTMLElement>('[aria-current="step"]')
    if (!box || !target) return
    const top = target.offsetTop
    if (top < box.scrollTop || top + target.offsetHeight > box.scrollTop + box.clientHeight) {
      box.scrollTop = Math.max(0, top - box.clientHeight / 3)
    }
  }, [currentStep])

  const reached = views.reduce((last, view) => (view.state === 'todo' ? last : view.index), 0)
  const positionLabel = position.done ? t.tasks.ruler.done : position.at ? t.tasks.ruler.at(position.at) : t.tasks.ruler.notStarted
  const lineX = 12

  return (
    <nav
      aria-label={t.tasks.workflow}
      data-workflow-rail
      className="flex w-[216px] shrink-0 flex-col border-r border-hairline bg-surface-subtle/30 max-lg:hidden"
    >
      <div className="flex h-11 shrink-0 items-center justify-between gap-2 border-b border-hairline px-3">
        <span aria-hidden="true" className="text-[11px] font-semibold tracking-[0.06em] uppercase text-ink-500">
          {t.tasks.workflow}
        </span>
        <span className="flex items-baseline gap-1.5">
          <span
            data-step-counter
            title={positionLabel}
            className="font-mono text-[11.5px] font-semibold text-ink tabular-nums"
          >
            {position.at} / {position.total}
          </span>
          <span className="sr-only">{positionLabel}</span>
        </span>
      </div>
      <div ref={scroller} className="relative min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 py-2.5 no-scrollbar">
        <div ref={content} className="relative flex flex-col gap-0.5 pb-2">
          {geometry.dots.length === views.length && views.length > 1 && (
            <svg aria-hidden="true" className="pointer-events-none absolute top-0 left-0 overflow-visible" width={24} height={geometry.height}>
              {reached > 0 && (
                <line
                  x1={lineX}
                  x2={lineX}
                  y1={geometry.dots[0]}
                  y2={geometry.dots[reached]}
                  className="stroke-ink/45 [stroke-width:1.5]"
                />
              )}
              {reached < views.length - 1 && (
                <line
                  x1={lineX}
                  x2={lineX}
                  y1={geometry.dots[reached]}
                  y2={geometry.dots[views.length - 1]}
                  strokeDasharray="3 3"
                  className="stroke-hairline-strong [stroke-width:1.5]"
                />
              )}
            </svg>
          )}
          {views.map((view) => {
            const isSelected = selected === view.step.id
            const name = view.latest?.agentName ?? agentName(view.step.agentId)
            const title = view.step.title || view.step.id
            return (
              <Fragment key={view.step.id}>
                <div data-ruler-step={view.step.id} className="relative">
                  <button
                    type="button"
                    title={view.step.id}
                    aria-pressed={isSelected}
                    aria-current={view.state === 'current' ? 'step' : undefined}
                    onClick={() => onSelect(view.step.id)}
                    className={cn(
                      'relative flex w-full cursor-pointer flex-col gap-0.5 rounded-ticket py-1.5 pr-2 pl-7 text-left transition-colors outline-none focus-visible:shadow-focus',
                      isSelected ? 'bg-surface-active text-ink' : 'hover:bg-surface-hover/70',
                    )}
                  >
                    <span className="flex min-w-0 items-baseline gap-1.5">
                      <span className="shrink-0 font-mono text-[10.5px] font-medium text-ink-500 tabular-nums">
                        {view.number}
                      </span>
                      <span
                        title={title}
                        className={cn(
                          'min-w-0 truncate text-[12.5px] leading-[1.35]',
                          isSelected ? 'font-semibold text-ink' : cn('font-medium', stateText[view.state]),
                        )}
                      >
                        {title}
                      </span>
                    </span>
                    <span className="flex min-w-0 items-center gap-1 text-[11px] leading-[1.35] text-ink-500">
                      <span className="min-w-0 truncate" title={name}>
                        {name}
                      </span>
                      <span data-step-state className="inline-flex shrink-0 items-baseline gap-1 whitespace-nowrap">
                        <StepStatus view={view} />
                        {view.records.length > 1 && <span className="font-mono text-[10.5px] text-ink-500">×{view.records.length}</span>}
                      </span>
                    </span>
                  </button>
                  <StepDot view={view} />
                </div>
                {isSelected && view.records.length > 0 && (
                  <ol aria-label={t.tasks.selectRecord} className="relative mt-0.5 mb-1 flex flex-col gap-0.5 pl-7">
                    {view.records.map((item) => {
                      const here = item.id === record
                      return (
                        <li key={item.id}>
                          <button
                            type="button"
                            aria-current={here ? 'true' : undefined}
                            onClick={() => onRecord(view.step.id, item.id)}
                            className={cn(
                              'flex w-full cursor-pointer items-center gap-1.5 rounded-stamp px-2 py-1 text-left text-[11.5px] leading-[1.35] transition-colors outline-none focus-visible:shadow-focus',
                              here
                                ? 'border border-hairline bg-surface-control font-semibold text-ink shadow-surface'
                                : 'font-medium text-ink-500 hover:bg-surface-hover/60 hover:text-ink',
                            )}
                          >
                            <span className="min-w-0 flex-1 truncate">
                              {t.tasks.ruler.visit(item.visit, item.attempt)} · {t.tasks.nodeStatuses[item.status]}
                              {here && (
                                <span className="text-[10.5px] font-semibold whitespace-nowrap text-pink-text">
                                  {' '}
                                  · {t.tasks.ruler.here}
                                </span>
                              )}
                            </span>
                          </button>
                        </li>
                      )
                    })}
                  </ol>
                )}
                {view.jumps.map((jump) => {
                  const jumpText = `${t.tasks.ruler.jump(jump.edge.label, stepNumber(jump.to))}${jump.edge.maxTraversals ? ` · ${t.tasks.ruler.used(jump.used, jump.edge.maxTraversals)}` : ''}`
                  return (
                    <p
                      key={jump.edge.id}
                      title={jumpText}
                      className={cn(
                        'relative my-0.5 ml-7 inline-flex max-w-[calc(100%-1.75rem)] items-center gap-1 rounded-stamp px-1.5 py-0.5 text-[10.5px] font-semibold',
                        jump.back ? 'bg-tint-pink/45 text-pink-text' : 'bg-surface-subtle text-ink-500',
                      )}
                    >
                      <Undo2Icon aria-hidden="true" className="size-3 shrink-0" strokeWidth={2.2} />
                      <span className="min-w-0 truncate">{jumpText}</span>
                    </p>
                  )
                })}
                {view.index < views.length - 1 && view.next.length > 0 && (
                  <p
                    title={view.next.map((edge) => edge.label + (edge.maxTraversals ? ` · ${t.tasks.ruler.used(edgeUses(detail, edge.id), edge.maxTraversals)}` : '')).join(' / ')}
                    className="relative ml-7 truncate py-0.5 pr-2 text-[10.5px] font-medium text-ink-500"
                  >
                    ↓ {view.next.map((edge) => edge.label + (edge.maxTraversals ? ` · ${t.tasks.ruler.used(edgeUses(detail, edge.id), edge.maxTraversals)}` : '')).join(' / ')}
                  </p>
                )}
              </Fragment>
            )
          })}
        </div>
      </div>
    </nav>
  )
}

// Narrow screens (< lg, when the left step rail is hidden): horizontally scrollable step chips plus inline visit/attempt pills
// for the selected step so narrow screens retain full visit switching.
export function WorkflowChips({
  detail,
  selected,
  record,
  onSelect,
  onRecord,
}: Pick<Props, 'detail' | 'selected' | 'onSelect'> & Partial<Pick<Props, 'record' | 'onRecord'>>) {
  const views = workflowSteps(detail)
  const selectedView = views.find((view) => view.step.id === selected)
  const row = useRef<HTMLElement>(null)
  useLayoutEffect(() => {
    const el = row.current
    const chip = el?.querySelector<HTMLElement>('[aria-pressed="true"]')
    if (el && chip && (chip.offsetLeft < el.scrollLeft || chip.offsetLeft + chip.offsetWidth > el.scrollLeft + el.clientWidth)) {
      el.scrollLeft = chip.offsetLeft - 12
    }
  }, [selected])

  return (
    <div className="flex shrink-0 flex-col gap-1.5 border-b border-hairline bg-surface-subtle/30 px-3 py-2 lg:hidden">
      <nav ref={row} aria-label={t.tasks.workflow} data-workflow-chips className="flex shrink-0 items-center gap-1.5 overflow-x-auto no-scrollbar">
        {views.map((view) => {
          const isSelected = selected === view.step.id
          return (
            <button
              key={view.step.id}
              type="button"
              title={view.step.id}
              aria-pressed={isSelected}
              aria-current={view.state === 'current' ? 'step' : undefined}
              onClick={() => onSelect(view.step.id)}
              className={cn(
                'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-full border px-2.5 text-[12px] font-medium whitespace-nowrap outline-none focus-visible:shadow-focus',
                view.state === 'done' && 'border-transparent bg-cta-bg text-cta-fg',
                view.state === 'current' && 'border-hairline-strong bg-tint-yellow font-semibold text-ink',
                view.state === 'visited' && 'border-hairline-strong bg-surface-elevated text-ink',
                view.state === 'todo' && 'border-hairline text-ink-500',
              )}
            >
              {isSelected && <span aria-hidden="true" className="size-1.5 rounded-full bg-current" />}
              {view.state === 'done' && <CheckIcon aria-hidden="true" className="size-3" strokeWidth={3} />}
              <span className="font-mono text-[11px]">{view.number}</span>
              {view.step.title || view.step.id}
              {view.records.length > 1 && <span className="font-mono text-[10.5px] opacity-70">×{view.records.length}</span>}
            </button>
          )
        })}
      </nav>
      {selectedView && selectedView.records.length > 1 && onRecord && (
        <ol aria-label={t.tasks.selectRecord} data-workflow-visits className="flex shrink-0 items-center gap-1.5 overflow-x-auto no-scrollbar">
          {selectedView.records.map((item) => {
            const here = item.id === record
            return (
              <li key={item.id} className="shrink-0">
                <button
                  type="button"
                  aria-current={here ? 'true' : undefined}
                  onClick={() => onRecord(selectedView.step.id, item.id)}
                  className={cn(
                    'inline-flex h-6 cursor-pointer items-center gap-1 rounded-stamp border px-2 text-[11.5px] whitespace-nowrap transition-colors outline-none focus-visible:shadow-focus',
                    here
                      ? 'border-hairline-strong bg-surface-control font-semibold text-ink shadow-surface'
                      : 'border-transparent bg-transparent font-medium text-ink-500 hover:bg-surface-hover hover:text-ink',
                  )}
                >
                  <span className="font-mono text-[10.5px]">{t.tasks.page.version(item.visit, item.attempt)}</span>
                  <span>· {t.tasks.nodeStatuses[item.status]}</span>
                </button>
              </li>
            )
          })}
        </ol>
      )}
    </div>
  )
}
