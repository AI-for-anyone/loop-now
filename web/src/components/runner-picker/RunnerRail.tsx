import { useRef, type KeyboardEvent, type ReactNode } from 'react'
import { t } from '@/i18n'
import type { AgentRuntime } from '@/lib/runtime'
import { cn } from '@/lib/utils'
import { RuntimeMark } from './RuntimeMark'
import { problemStamp, stateText, type RailEntry } from './rail-entry'

// The rail's first entry when it has one that is not a runtime (RunnerPicker's lead).
export interface RailLead {
  name: string
  mark: ReactNode
  state: ReactNode
  label: string
  tabId: string
  selected: boolean
  inUse: boolean
  onBrowse: () => void
}

interface RunnerRailProps {
  entries: RailEntry[]
  lead?: RailLead
  browsed: AgentRuntime | undefined
  current: AgentRuntime | undefined
  wide: boolean
  tabId: (id: AgentRuntime) => string
  panelId: string
  onBrowse: (id: AgentRuntime) => void
  // Right arrow leaves the rail for the model search.
  onLeave: () => void
}

// The state under a runtime's name: a green dot for one that can run (with its version), a tilted outline stamp for one that cannot,
// a quiet line while it is being checked; the bundled runtime says it is built in and what it needs.
export function RunnerState({ entry, compact = false }: { entry: RailEntry; compact?: boolean }) {
  const copy = t.runtime.picker
  const { readiness, definition } = entry
  if (readiness.state === 'problem') {
    const stamp = readiness.problem === 'logged_out' ? 'text-pink-text [--tilt-shadow:inset_0_0_0_2px_var(--pink-text)]' : 'text-red-text [--tilt-shadow:inset_0_0_0_2px_var(--red-text)]'
    return <span className={cn('riso-tilt inline-flex min-h-5 w-fit items-center rounded-[4px] px-1.5 text-[11.5px] font-black tracking-[0.06em] whitespace-nowrap [--tilt-bg:transparent] [--tilt:-3deg]', stamp)}>{problemStamp(definition, readiness.problem)}</span>
  }
  if (readiness.state !== 'ready') {
    return <span className="text-[12px] font-bold text-ink-500">{readiness.state === 'checking' ? copy.checking : copy.unknown}</span>
  }
  if (definition.capabilities.bundled) {
    return (
      <span className="grid gap-0.5">
        <span className="w-fit rounded-full bg-ink px-2 text-[11px] leading-[18px] font-black text-white">{copy.bundled}</span>
        {!compact && <span className="text-[12px] leading-snug font-bold text-ink-700">{copy.bundledNote}</span>}
      </span>
    )
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-[12px] font-black text-green-text">
      <span aria-hidden="true" className="size-[7px] shrink-0 rounded-full bg-green shadow-[0_0_0_1.5px_var(--ink)]" />
      {copy.ready}
      {entry.version && !compact && <span className="font-mono text-[11px] font-medium text-ink-500">{entry.version}</span>}
    </span>
  )
}

// The column of runtimes on the left of the picker (a row of cards at narrow widths). Each is a tab: choosing one shows its models (or how
// to set it up) on the right; the model, not the tab, is what gets chosen. The runtime in use carries the pink arrow of the console's rulers.
export function RunnerRail({ entries, lead, browsed, current, wide, tabId, panelId, onBrowse, onLeave }: RunnerRailProps) {
  const refs = useRef(new Map<string, HTMLButtonElement>())
  // Every tab in order, keyed by its element id: the first entry, then the runtimes.
  const tabs = [...(lead ? [{ id: lead.tabId, browse: lead.onBrowse }] : []), ...entries.map((entry) => ({ id: tabId(entry.definition.id), browse: () => onBrowse(entry.definition.id) }))]
  const move = (event: KeyboardEvent, index: number) => {
    const back = wide ? 'ArrowUp' : 'ArrowLeft', next = wide ? 'ArrowDown' : 'ArrowRight'
    const target = event.key === back ? index - 1 : event.key === next ? index + 1 : event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : null
    if (target === null) {
      if (wide && event.key === 'ArrowRight') {
        event.preventDefault()
        onLeave()
      }
      return
    }
    event.preventDefault()
    const tab = tabs[(target + tabs.length) % tabs.length]
    tab.browse()
    refs.current.get(tab.id)?.focus()
  }
  const register = (id: string, node: HTMLButtonElement | null) => {
    if (node) refs.current.set(id, node)
    else refs.current.delete(id)
  }
  const offset = lead ? 1 : 0
  return (
    <div className={cn('flex min-w-0 flex-1 flex-col gap-2 bg-paper-tab', wide ? 'border-r-2 border-dashed border-ink/35 px-2.5 pt-3 pb-3' : 'border-b-2 border-dashed border-ink/35 px-2.5 pt-2.5 pb-3')}>
      {wide && <p aria-hidden="true" className="px-1.5 font-display-latin text-[11px] font-extrabold tracking-[0.14em] text-ink-500 uppercase">{t.runtime.picker.rail}</p>}
      <div role="tablist" aria-label={t.runtime.picker.rail} aria-orientation={wide ? 'vertical' : 'horizontal'} className={cn('flex gap-1.5', wide ? 'flex-col' : 'no-scrollbar -mx-2.5 overflow-x-auto px-2.5 pb-1')}>
        {lead && (
          <RailTab id={lead.tabId} register={register} label={lead.label} selected={lead.selected} inUse={lead.inUse} wide={wide} panelId={panelId} onClick={lead.onBrowse} onKeyDown={(event) => move(event, 0)} mark={lead.mark} name={lead.name} state={lead.state} />
        )}
        {entries.map((entry, index) => {
          const { definition } = entry
          const id = tabId(definition.id)
          return (
            <RailTab
              key={definition.id}
              id={id}
              register={register}
              label={[definition.name, stateText(entry), definition.id === current ? t.runtime.picker.current : null].filter(Boolean).join(', ')}
              selected={definition.id === browsed}
              inUse={definition.id === current}
              wide={wide}
              panelId={panelId}
              onClick={() => onBrowse(definition.id)}
              onKeyDown={(event) => move(event, index + offset)}
              mark={<RuntimeMark runtime={definition} />}
              name={definition.name}
              state={<RunnerState entry={entry} compact={!wide} />}
            />
          )
        })}
      </div>
    </div>
  )
}

interface RailTabProps {
  id: string
  // Keeps the tab's element by its id, for moving the focus with the arrow keys.
  register: (id: string, node: HTMLButtonElement | null) => void
  label: string
  selected: boolean
  inUse: boolean
  wide: boolean
  panelId: string
  onClick: () => void
  onKeyDown: (event: KeyboardEvent) => void
  mark: ReactNode
  name: string
  state: ReactNode
}

// One card in the rail: the mark, the name and the state under it; yellow while looked at.
function RailTab({ id, register, label, selected, inUse, wide, panelId, onClick, onKeyDown, mark, name, state }: RailTabProps) {
  return (
    <button
      ref={(node) => register(id, node)}
      type="button"
      role="tab"
      id={id}
      aria-selected={selected}
      aria-controls={panelId}
      aria-label={label}
      tabIndex={selected ? 0 : -1}
      onClick={onClick}
      onKeyDown={onKeyDown}
      className={cn(
        'relative grid shrink-0 cursor-pointer grid-cols-[32px_minmax(0,1fr)] items-center gap-x-2.5 rounded-input border-2 text-left outline-none focus-visible:shadow-focus',
        wide ? 'px-2.5 py-2' : 'min-w-[172px] px-2.5 py-1.5',
        selected ? 'border-ink bg-riso-yellow shadow-[3px_3px_0_var(--ink)]' : 'border-transparent hover:border-ink/25 hover:bg-paper-raised',
      )}
    >
      {/* The entry in use: the pink arrow the console's rulers use for "you are here". */}
      {inUse && <span aria-hidden="true" className={cn('absolute size-0 border-y-[6px] border-l-[8px] border-y-transparent border-l-riso-pink', wide ? 'top-1/2 -left-[13px] -translate-y-1/2' : '-top-[3px] left-3 rotate-90')} />}
      {mark}
      <span className="grid min-w-0 gap-1">
        <span className="truncate font-display-latin text-[15.5px] leading-tight font-extrabold text-ink">{name}</span>
        {state}
      </span>
    </button>
  )
}
