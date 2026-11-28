import { Fragment, useLayoutEffect, useRef, useState } from 'react'
import { CheckIcon, GitForkIcon, GitMergeIcon, MessagesSquareIcon, Undo2Icon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { WithConversationBadge } from './StepPrompt'
import {
  currentNodesOf,
  edgeUses,
  stepNumber,
  taskEnded,
  workflowLayout,
  workflowPosition,
  type BranchLane,
  type ForkBlock,
  type ForkJump,
  type StepJump,
  type StepState,
  type StepView,
} from './workflow-steps'

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

// Compact 16px status indicator aligned on the 1.5px vertical rail (or inside a branch lane).
function StepDot({ view, compact = false, trunk = false }: { view: StepView; compact?: boolean; trunk?: boolean }) {
  const pos = compact ? 'top-[8px] left-1' : 'top-[9px] left-1'
  if (view.state === 'current') {
    const blocked = view.latest?.status === 'blocked'
    return (
      <span
        data-step-dot={view.index}
        data-trunk-dot={trunk ? view.index : undefined}
        aria-hidden="true"
        className={cn(
          'absolute flex size-4 items-center justify-center rounded-full border-[1.5px] bg-tint-yellow',
          pos,
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
      data-trunk-dot={trunk ? view.index : undefined}
      aria-hidden="true"
      className={cn(
        'absolute flex size-4 items-center justify-center rounded-full border',
        pos,
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

function StepVisitList({
  view,
  record,
  onRecord,
  compact = false,
}: {
  view: StepView
  record: string | undefined
  onRecord: (stepId: string, recordId: string) => void
  compact?: boolean
}) {
  if (view.records.length === 0) return null
  return (
    <ol aria-label={t.tasks.selectRecord} className={cn('relative mt-0.5 mb-1 flex flex-col gap-0.5', compact ? 'pl-5' : 'pl-7')}>
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
  )
}

function StepJumpBadges({ jumps, compact = false }: { jumps: StepJump[]; compact?: boolean }) {
  return (
    <>
      {jumps.map((jump) => {
        const jumpText = `${t.tasks.ruler.jump(jump.edge.label, stepNumber(jump.to))}${jump.edge.maxTraversals ? ` · ${t.tasks.ruler.used(jump.used, jump.edge.maxTraversals)}` : ''}`
        return (
          <p
            key={jump.edge.id}
            title={jumpText}
            className={cn(
              'relative my-0.5 inline-flex items-center gap-1 rounded-stamp px-1.5 py-0.5 text-[10.5px] font-semibold',
              compact ? 'ml-5 max-w-[calc(100%-1.25rem)]' : 'ml-7 max-w-[calc(100%-1.75rem)]',
              jump.back ? 'bg-tint-pink/45 text-pink-text' : 'bg-surface-subtle text-ink-500',
            )}
          >
            <Undo2Icon aria-hidden="true" className="size-3 shrink-0" strokeWidth={2.2} />
            <span className="min-w-0 truncate">{jumpText}</span>
          </p>
        )
      })}
    </>
  )
}

function ForkJumpBadges({ jumps }: { jumps: ForkJump[] }) {
  return (
    <>
      {jumps.map((jump) => {
        const label = jump.edges[0]?.label || jump.fork.id
        const targets = jump.targets.map(stepNumber).join(', ')
        const text = `${t.tasks.ruler.forkJump(label, targets)}${jump.maxTraversals ? ` · ${t.tasks.ruler.used(jump.used, jump.maxTraversals)}` : ''}`
        return (
          <p
            key={jump.fork.id}
            title={text}
            className={cn(
              'relative my-0.5 ml-7 inline-flex max-w-[calc(100%-1.75rem)] items-center gap-1 rounded-stamp px-1.5 py-0.5 text-[10.5px] font-semibold',
              jump.back ? 'bg-tint-pink/45 text-pink-text' : 'bg-surface-subtle text-ink-500',
            )}
          >
            <Undo2Icon aria-hidden="true" className="size-3 shrink-0" strokeWidth={2.2} />
            <span className="min-w-0 truncate">{text}</span>
          </p>
        )
      })}
    </>
  )
}

function StepButton({
  view,
  isSelected,
  onSelect,
  agentName,
  compact = false,
  trunk = false,
  joinsForks = [],
}: {
  view: StepView
  isSelected: boolean
  onSelect: (stepId: string) => void
  agentName: (agentId: string) => string
  compact?: boolean
  trunk?: boolean
  joinsForks?: ForkBlock[]
}) {
  const name = view.latest?.agentName ?? agentName(view.step.agentId)
  const title = view.step.title || view.step.id
  const activeJoin = joinsForks.find((b) => b.active)
  return (
    <div
      data-ruler-step={view.step.id}
      data-join-step={joinsForks.length > 0 ? view.step.id : undefined}
      className="relative"
    >
      <button
        type="button"
        title={view.step.id}
        aria-pressed={isSelected}
        aria-current={view.state === 'current' ? 'step' : undefined}
        onClick={() => onSelect(view.step.id)}
        className={cn(
          'relative flex w-full cursor-pointer flex-col gap-0.5 rounded-ticket py-1.5 text-left transition-colors outline-none focus-visible:shadow-focus',
          compact ? 'pr-1.5 pl-6' : 'pr-2 pl-7',
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
          {joinsForks.length > 0 && (
            <span
              data-join-badge
              className="ml-auto inline-flex shrink-0 items-center gap-0.5 rounded-stamp border border-hairline bg-surface-subtle px-1 py-0.5 text-[10px] leading-none font-medium text-ink-500"
            >
              <GitMergeIcon aria-hidden="true" className="size-2.5 shrink-0" strokeWidth={2.2} />
              <span>{activeJoin ? t.tasks.ruler.arrived(activeJoin.arrivedCount, activeJoin.lanes.length) : t.tasks.ruler.join}</span>
            </span>
          )}
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
        {view.step.withConversation && <WithConversationBadge compact />}
      </button>
      <StepDot view={view} compact={compact} trunk={trunk} />
    </div>
  )
}

interface LaneGeometry {
  width: number
  centers: number[]
}

// Orthogonal SVG connector splitting from the trunk rail (x = 12) to each parallel lane center (kind="fork")
// or merging from each lane center back to the trunk rail (kind="join").
function BranchConnector({
  kind,
  lanes,
  geometry,
}: {
  kind: 'fork' | 'join'
  lanes: BranchLane[]
  geometry: LaneGeometry
}) {
  if (geometry.width <= 0 || geometry.centers.length !== lanes.length) return null
  const h = 20
  const mid = kind === 'fork' ? 8 : 12
  const trunkX = 12
  const laneSolid = (lane: BranchLane) =>
    kind === 'fork' ? lane.active || lane.arrived || lane.steps.some((s) => s.state !== 'todo') : lane.arrived
  const anySolid = lanes.some(laneSolid)
  const sorted = lanes.map((lane, i) => ({ x: geometry.centers[i], solid: laneSolid(lane) }))
  return (
    <svg aria-hidden="true" className="pointer-events-none block w-full shrink-0 overflow-visible" width={geometry.width} height={h}>
      {/* Trunk vertical stem */}
      <line
        x1={trunkX}
        x2={trunkX}
        y1={kind === 'fork' ? 0 : mid}
        y2={kind === 'fork' ? mid : h}
        strokeDasharray={anySolid ? undefined : '3 3'}
        className={cn('[stroke-width:1.5]', anySolid ? 'stroke-ink/45' : 'stroke-hairline-strong')}
      />
      {/* Draw dashed legs first so solid active/arrived branches cleanly sit on top along shared horizontal segments. */}
      {sorted
        .filter((leg) => !leg.solid)
        .map((leg, i) => (
          <path
            key={`dashed-${i}`}
            d={kind === 'fork' ? `M ${trunkX} ${mid} H ${leg.x} V ${h}` : `M ${leg.x} 0 V ${mid} H ${trunkX}`}
            fill="none"
            strokeDasharray="3 3"
            className="stroke-hairline-strong [stroke-width:1.5]"
          />
        ))}
      {sorted
        .filter((leg) => leg.solid)
        .map((leg, i) => (
          <path
            key={`solid-${i}`}
            d={kind === 'fork' ? `M ${trunkX} ${mid} H ${leg.x} V ${h}` : `M ${leg.x} 0 V ${mid} H ${trunkX}`}
            fill="none"
            className="stroke-ink/45 [stroke-width:1.5]"
          />
        ))}
    </svg>
  )
}

function ForkBlockView({
  block,
  detail,
  selected,
  record,
  onSelect,
  onRecord,
  agentName,
}: {
  block: ForkBlock
  detail: Schemas['TaskDetail']
  selected: string | null
  record: string | undefined
  onSelect: (stepId: string) => void
  onRecord: (stepId: string, recordId: string) => void
  agentName: (agentId: string) => string
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [geometry, setGeometry] = useState<LaneGeometry>({ width: 0, centers: [] })
  const layoutKey = `${block.fork.id}:${block.lanes.map((l) => `${l.branch}:${l.steps.length}`).join('|')}`

  useLayoutEffect(() => {
    const el = containerRef.current
    if (!el) return
    const measure = () => {
      const box = el.getBoundingClientRect()
      const centers = [...el.querySelectorAll<HTMLElement>('[data-branch-lane]')].map((lane) => {
        const rect = lane.getBoundingClientRect()
        return rect.left - box.left + rect.width / 2
      })
      setGeometry((old) =>
        Math.abs(old.width - box.width) < 0.5 &&
        old.centers.length === centers.length &&
        old.centers.every((x, i) => Math.abs(x - centers[i]) < 0.5)
          ? old
          : { width: box.width, centers },
      )
    }
    measure()
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure)
    observer?.observe(el)
    return () => observer?.disconnect()
  }, [layoutKey])

  const laneGridClass =
    block.lanes.length === 2
      ? 'grid-cols-2'
      : block.lanes.length === 3
        ? 'grid-cols-3'
        : 'grid-flow-col auto-cols-[minmax(140px,1fr)] overflow-x-auto pb-1'

  return (
    <section
      aria-label={t.tasks.ruler.fork}
      data-fork-block={block.fork.id}
      className="relative my-0.5 flex flex-col"
    >
      <div className="flex items-center justify-between gap-1.5 pl-6 pr-1 pt-0.5 text-[10.5px] font-medium text-ink-500">
        <span className="inline-flex min-w-0 items-center gap-1">
          <GitForkIcon aria-hidden="true" className="size-3 shrink-0" strokeWidth={2.2} />
          <span className="truncate font-semibold text-ink-700">{t.tasks.ruler.fork}</span>
          {block.maxParallelBranches && (
            <span className="shrink-0 rounded-stamp border border-hairline bg-surface-subtle px-1 py-0.5 font-mono text-[10px] leading-none text-ink-500">
              {t.tasks.ruler.maxParallel(block.maxParallelBranches)}
            </span>
          )}
        </span>
        {block.active && (
          <span className="shrink-0 font-mono text-[10.5px] text-ink-500 tabular-nums">
            {t.tasks.ruler.arrived(block.arrivedCount, block.lanes.length)}
          </span>
        )}
      </div>
      <BranchConnector kind="fork" lanes={block.lanes} geometry={geometry} />
      <div ref={containerRef} data-fork-lanes className={cn('grid gap-1.5', laneGridClass)}>
        {block.lanes.map((lane) => {
          const merge = lane.workspace?.merge ?? null
          const laneState = lane.blocked ? 'blocked' : lane.active ? 'active' : lane.arrived ? 'arrived' : 'todo'
          const entryLabel = lane.entryEdge.label || lane.branch
          const entryLimit = lane.entryEdge.maxTraversals
            ? ` · ${t.tasks.ruler.used(edgeUses(detail, lane.entryEdge.id), lane.entryEdge.maxTraversals)}`
            : ''
          return (
            <div
              key={lane.branch}
              data-branch-lane={lane.branch}
              data-branch-state={laneState}
              className={cn(
                'flex min-w-0 flex-col gap-0.5 rounded-card border p-1.5 transition-colors',
                lane.blocked
                  ? 'border-red-text/45 bg-tint-red/20'
                  : lane.active
                    ? 'border-ink/35 bg-surface-canvas shadow-surface'
                    : lane.arrived
                      ? 'border-hairline bg-surface-subtle/50'
                      : 'border-dashed border-hairline-strong/70 bg-surface-subtle/20',
              )}
            >
              <div className="flex min-w-0 items-center justify-between gap-1 px-1 pb-0.5">
                <span
                  title={`${entryLabel}${entryLimit}`}
                  className="min-w-0 truncate text-[10.5px] font-semibold text-ink-500"
                >
                  {entryLabel}
                </span>
                {merge ? (
                  <span
                    data-branch-merge={merge}
                    className={cn(
                      'shrink-0 rounded-stamp px-1 py-0.5 text-[9.5px] leading-none font-semibold',
                      merge === 'conflict'
                        ? 'bg-tint-red text-red-text'
                        : merge === 'merged'
                          ? 'bg-tint-green text-green-text'
                          : 'bg-surface-subtle text-ink-500',
                    )}
                  >
                    {t.tasks.workspace.mergeStates[merge]}
                  </span>
                ) : (
                  lane.arrived &&
                  block.active && (
                    <span className="shrink-0 rounded-stamp bg-tint-green px-1 py-0.5 text-[9.5px] leading-none font-semibold text-green-text">
                      {t.tasks.ruler.branchArrived}
                    </span>
                  )
                )}
              </div>
              {lane.steps.map((view, idx) => {
                const isSelected = selected === view.step.id
                return (
                  <Fragment key={view.step.id}>
                    <StepButton
                      view={view}
                      isSelected={isSelected}
                      onSelect={onSelect}
                      agentName={agentName}
                      compact
                    />
                    {isSelected && <StepVisitList view={view} record={record} onRecord={onRecord} compact />}
                    <StepJumpBadges jumps={view.jumps} compact />
                    {idx < lane.steps.length - 1 && view.next.length > 0 && (
                      <p
                        title={view.next.map((edge) => edge.label + (edge.maxTraversals ? ` · ${t.tasks.ruler.used(edgeUses(detail, edge.id), edge.maxTraversals)}` : '')).join(' / ')}
                        className="relative ml-5 truncate py-0.5 pr-1 text-[10px] font-medium text-ink-500"
                      >
                        ↓ {view.next.map((edge) => edge.label + (edge.maxTraversals ? ` · ${t.tasks.ruler.used(edgeUses(detail, edge.id), edge.maxTraversals)}` : '')).join(' / ')}
                      </p>
                    )}
                  </Fragment>
                )
              })}
              {lane.toJoin.length > 0 && (
                <p
                  title={lane.toJoin.map((e) => e.label).join(' / ')}
                  className="mt-auto truncate pt-0.5 pl-5 text-[10px] font-medium text-ink-500"
                >
                  ↓ {lane.toJoin.map((e) => e.label).join(' / ')}
                </p>
              )}
            </div>
          )
        })}
      </div>
      <BranchConnector kind="join" lanes={block.lanes} geometry={geometry} />
    </section>
  )
}

interface Geometry {
  height: number
  dots: number[]
}

// Left step rail: 216px on lg+ for sequential workflows (compact 16px status indicators on a 1.5px vertical rail, 1-line
// step title + muted agent/status subline, header step counter, nested visit/attempt rows under the selected step, and
// inline rework/jump badges). For workflows with forks, expands to draw parallel branches side by side with SVG fork/join
// connectors and marks every active branch step. On < lg screens the rail is hidden and WorkflowChips renders above the pane.
export function WorkflowRuler({ detail, selected, record, onSelect, onRecord, agentName }: Props) {
  const layout = workflowLayout(detail)
  const views = layout.views
  const position = workflowPosition(detail)
  const activeNodes = taskEnded(detail.task) ? [] : currentNodesOf(detail.task)
  const scroller = useRef<HTMLDivElement>(null)
  const content = useRef<HTMLDivElement>(null)
  const [geometry, setGeometry] = useState<Geometry>({ height: 0, dots: [] })
  const currentKey = activeNodes.map((c) => c.stepId).join(',')
  const layoutKey = `${views.map((v) => `${v.step.id}:${v.state}:${v.records.length}:${v.jumps.length}:${v.next.length}:${v.step.withConversation ? 1 : 0}`).join('|')}|${selected ?? ''}|${record ?? ''}`

  // Measure each dot's vertical center after layout so the 1.5px rail connects cleanly across expanded visit lists and edge labels.
  useLayoutEffect(() => {
    if (layout.hasForks) return
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
  }, [layout.hasForks, layoutKey])

  // Keep the active step visible when it changes without overriding manual scrolling afterwards.
  useLayoutEffect(() => {
    const box = scroller.current
    const target = box?.querySelector<HTMLElement>('[aria-current="step"]')
    if (!box || !target) return
    const top = target.offsetTop
    if (top < box.scrollTop || top + target.offsetHeight > box.scrollTop + box.clientHeight) {
      box.scrollTop = Math.max(0, top - box.clientHeight / 3)
    }
  }, [currentKey])

  const reached = views.reduce((last, view) => (view.state === 'todo' ? last : view.index), 0)
  const multiActive = activeNodes.length > 1
  const activeStepNames = activeNodes.map((c) => c.stepTitle || c.stepId).join(', ')
  const positionLabel = multiActive
    ? t.tasks.ruler.activeList(activeStepNames)
    : position.done
      ? t.tasks.ruler.done
      : position.at
        ? t.tasks.ruler.at(position.at)
        : t.tasks.ruler.notStarted
  const lineX = 12
  const railWidth = !layout.hasForks
    ? 'w-[216px]'
    : layout.maxLanes === 2
      ? 'w-[344px] xl:w-[368px]'
      : 'w-[396px] xl:w-[440px]'

  return (
    <nav
      aria-label={t.tasks.workflow}
      data-workflow-rail
      data-has-forks={layout.hasForks ? 'true' : undefined}
      className={cn('flex shrink-0 flex-col border-r border-hairline bg-surface-subtle/30 max-lg:hidden', railWidth)}
    >
      <div className="flex h-11 shrink-0 items-center justify-between gap-2 border-b border-hairline px-3">
        <span aria-hidden="true" className="text-[11px] font-semibold tracking-[0.06em] uppercase text-ink-500">
          {t.tasks.workflow}
        </span>
        <span className="flex items-baseline gap-1.5">
          {multiActive ? (
            <span
              data-step-counter
              title={positionLabel}
              className="inline-flex items-center gap-1 rounded-full border border-hairline-strong bg-tint-yellow px-2 py-0.5 font-mono text-[11px] leading-none font-semibold text-ink tabular-nums"
            >
              <GitForkIcon aria-hidden="true" className="size-3" strokeWidth={2.2} />
              {t.tasks.ruler.activeCount(activeNodes.length)}
            </span>
          ) : (
            <span
              data-step-counter
              title={positionLabel}
              className="font-mono text-[11.5px] font-semibold text-ink tabular-nums"
            >
              {position.at} / {position.total}
            </span>
          )}
          <span className="sr-only">{positionLabel}</span>
        </span>
      </div>
      <div ref={scroller} className="relative min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 py-2.5 no-scrollbar">
        <div ref={content} className="relative flex flex-col gap-0.5 pb-2">
          {!layout.hasForks && geometry.dots.length === views.length && views.length > 1 && (
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
          {layout.items.map((item, itemIdx) => {
            if (item.kind === 'fork') {
              return (
                <ForkBlockView
                  key={`fork:${item.block.fork.id}`}
                  block={item.block}
                  detail={detail}
                  selected={selected}
                  record={record}
                  onSelect={onSelect}
                  onRecord={onRecord}
                  agentName={agentName}
                />
              )
            }
            const { view, joinsForks, forkJumps } = item
            const isSelected = selected === view.step.id
            const nextItem = layout.items[itemIdx + 1]
            const showNextArrow = nextItem?.kind === 'step' && view.next.length > 0
            return (
              <Fragment key={view.step.id}>
                <StepButton
                  view={view}
                  isSelected={isSelected}
                  onSelect={onSelect}
                  agentName={agentName}
                  trunk={layout.hasForks}
                  joinsForks={joinsForks}
                />
                {isSelected && <StepVisitList view={view} record={record} onRecord={onRecord} />}
                <StepJumpBadges jumps={view.jumps} />
                <ForkJumpBadges jumps={forkJumps} />
                {showNextArrow && (
                  <div className="relative flex items-center py-0.5">
                    {layout.hasForks && (
                      <span
                        aria-hidden="true"
                        className={cn(
                          'pointer-events-none absolute -top-1 -bottom-1 left-[11.25px]',
                          nextItem.view.state !== 'todo'
                            ? 'w-[1.5px] bg-ink/45'
                            : 'w-0 border-l-[1.5px] border-dashed border-hairline-strong',
                        )}
                      />
                    )}
                    <p
                      title={view.next.map((edge) => edge.label + (edge.maxTraversals ? ` · ${t.tasks.ruler.used(edgeUses(detail, edge.id), edge.maxTraversals)}` : '')).join(' / ')}
                      className="relative ml-7 truncate pr-2 text-[10.5px] font-medium text-ink-500"
                    >
                      ↓ {view.next.map((edge) => edge.label + (edge.maxTraversals ? ` · ${t.tasks.ruler.used(edgeUses(detail, edge.id), edge.maxTraversals)}` : '')).join(' / ')}
                    </p>
                  </div>
                )}
                {layout.hasForks && nextItem?.kind === 'step' && !showNextArrow && (
                  <div aria-hidden="true" className="relative h-2.5">
                    <span
                      className={cn(
                        'pointer-events-none absolute -top-1 -bottom-1 left-[11.25px]',
                        nextItem.view.state !== 'todo'
                          ? 'w-[1.5px] bg-ink/45'
                          : 'w-0 border-l-[1.5px] border-dashed border-hairline-strong',
                      )}
                    />
                  </div>
                )}
              </Fragment>
            )
          })}
        </div>
      </div>
    </nav>
  )
}

function StepChip({
  view,
  selected,
  onSelect,
}: {
  view: StepView
  selected: string | null
  onSelect: (stepId: string) => void
}) {
  const isSelected = selected === view.step.id
  const blocked = view.state === 'current' && view.latest?.status === 'blocked'
  return (
    <button
      type="button"
      title={view.step.id}
      aria-pressed={isSelected}
      aria-current={view.state === 'current' ? 'step' : undefined}
      onClick={() => onSelect(view.step.id)}
      className={cn(
        'inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-full border px-2.5 text-[12px] font-medium whitespace-nowrap outline-none focus-visible:shadow-focus',
        view.state === 'done' && 'border-transparent bg-cta-bg text-cta-fg',
        view.state === 'current' && 'border-hairline-strong bg-tint-yellow font-semibold text-ink',
        blocked && 'border-red-text/60',
        view.state === 'visited' && 'border-hairline-strong bg-surface-elevated text-ink',
        view.state === 'todo' && 'border-hairline text-ink-500',
      )}
    >
      {isSelected && <span aria-hidden="true" className={cn('size-1.5 rounded-full', blocked ? 'bg-red-text' : 'bg-current')} />}
      {!isSelected && blocked && <span aria-hidden="true" className="size-1.5 rounded-full bg-red-text" />}
      {view.state === 'done' && <CheckIcon aria-hidden="true" className="size-3" strokeWidth={3} />}
      <span className="font-mono text-[11px]">{view.number}</span>
      {view.step.title || view.step.id}
      {view.step.withConversation && (
        <span data-step-with-conversation className="inline-flex items-center" title={t.tasks.withConversationHint}>
          <MessagesSquareIcon aria-hidden="true" className="size-3 shrink-0" strokeWidth={2.2} />
          <span className="sr-only"> · {t.tasks.withConversation}</span>
        </span>
      )}
      {view.records.length > 1 && <span className="font-mono text-[10.5px] opacity-70">×{view.records.length}</span>}
    </button>
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
  const layout = workflowLayout(detail)
  const selectedView = layout.views.find((view) => view.step.id === selected)
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
        {layout.items.map((item) => {
          if (item.kind === 'step') {
            return <StepChip key={item.view.step.id} view={item.view} selected={selected} onSelect={onSelect} />
          }
          return (
            <span
              key={`chips-fork:${item.block.fork.id}`}
              data-chips-fork={item.block.fork.id}
              className="inline-flex shrink-0 items-center gap-1 rounded-full border border-dashed border-hairline-strong bg-surface-subtle/60 px-1.5 py-0.5"
            >
              <GitForkIcon aria-hidden="true" className="size-3 shrink-0 text-ink-500" strokeWidth={2.2} />
              {item.block.lanes.map((lane, laneIdx) => (
                <Fragment key={lane.branch}>
                  {laneIdx > 0 && <span aria-hidden="true" className="h-3.5 w-px bg-hairline-strong" />}
                  {lane.steps.map((view) => (
                    <StepChip key={view.step.id} view={view} selected={selected} onSelect={onSelect} />
                  ))}
                </Fragment>
              ))}
            </span>
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
