import { useId, useRef, useState, type ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { useAllModels, useModelProviders, useModelsOf, useRuntimes } from '@/api/queries'
import { t } from '@/i18n'
import { describeError } from '@/lib/errors'
import { canRun, railOrder, runtimeReadiness, statusOf, useRuntimeLookup, type AgentRuntime, type RuntimeDefinition } from '@/lib/runtime'
import type { ScopeKey } from '@/lib/scope'
import { choosableLevels } from '@/lib/thinking'
import { cn } from '@/lib/utils'
import { ModelList } from './ModelList'
import { blocks, unavailableNote, unavailableOf } from './provider-access'
import type { RailEntry } from './rail-entry'
import { RunnerRail } from './RunnerRail'
import { RunnerSetup } from './RunnerSetup'
import { buildRows, modelKey, orderProviders } from './runner-rows'
import { ThinkingRuler } from './ThinkingRuler'

type ModelRef = Schemas['ModelRef']
type ThinkingLevel = Schemas['ThinkingLevel']
type Provider = Schemas['ModelProvider']

export interface RunnerChoice {
  model: ModelRef
  thinkingLevel: ThinkingLevel | null
}

// A first entry in the rail that is not a runtime (who writes with AI: "Automatic"). It is in use while no model is chosen (value null),
// is looked at first then, and shows its own panel in place of models; its panel offers the way back to it.
export interface RunnerLead {
  name: string
  mark: ReactNode
  // Inline status in the rail tab, and the same said in words for its accessible name.
  state: ReactNode
  stateText: string
  panel: ReactNode
  // In place of the thinking levels while it is in use.
  thinkingNote: string
}

interface RunnerPickerProps {
  value: ModelRef | null
  thinkingLevel: ThinkingLevel | null
  // A model was chosen. The thinking level goes with it: the current one when the new model has it, otherwise its default (null).
  onChoose: (choice: RunnerChoice) => void
  onThinkingChange: (level: ThinkingLevel | null) => void
  // The Agent being edited: the catalog is read with it, so plugin providers say why this Agent cannot use them right now and every
  // provider whether it can run now. Not given when creating an Agent; plugin providers cannot be chosen then.
  agentId?: string
  // Creating an Agent in this project or the platform (a scope key): the catalog says which providers it could use right away.
  newIn?: ScopeKey
  // inline: a field in a form (New Agent, Writing settings). popover: opened from the message box or Basics; the search box takes focus.
  variant?: 'inline' | 'popover'
  labelledBy?: string
  invalid?: boolean
  // Only these runtimes are offered (who writes with AI: only CLIs with their own login on this computer); all of them when not given.
  offers?: (definition: RuntimeDefinition) => boolean
  lead?: RunnerLead
}

const MODEL_LIMIT = 200

// Single-column command selector: top horizontal runtime tab bar (local CLIs first, bundled runtime last), seamless search and model tree
// (or setup guidance when a runtime cannot run here), a compact single-line context strip, and a horizontal thinking-level pill bar.
// Runtimes, capabilities and status are read only through lib/runtime.ts.
export function RunnerPicker({ value, thinkingLevel, onChoose, onThinkingChange, agentId, newIn, variant = 'inline', labelledBy, invalid, offers, lead }: RunnerPickerProps) {
  const lookup = useRuntimeLookup()
  const catalog = useModelProviders(agentId ?? (newIn === undefined ? undefined : { newIn }))
  const statuses = useRuntimes(lookup.all.some((definition) => !definition.capabilities.bundled))
  const panelId = useId(), tabBase = useId()
  const tabId = (id: AgentRuntime) => `${tabBase}-${id}`
  const searchRef = useRef<HTMLInputElement>(null)
  const railRef = useRef<HTMLDivElement>(null)
  const thinkingRef = useRef<HTMLDivElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)

  const providers = catalog.data?.providers ?? []
  const valueProvider = value ? providers.find((provider) => provider.id === value.provider) : undefined
  const valueRuntime = value ? (valueProvider?.runtime ?? lookup.forProvider(value.provider)?.id) : undefined

  const read = { checking: statuses.isLoading, failed: statuses.isError }
  const statusFor = (definition: RuntimeDefinition) => statusOf(statuses.data?.runtimes, definition)
  const readinessOf = (definition: RuntimeDefinition) => runtimeReadiness(definition, { ...read, status: statusFor(definition) })
  const offered = offers ? lookup.all.filter(offers) : lookup.all
  const entries: RailEntry[] = railOrder(offered, readinessOf).map((definition) => ({
    definition,
    readiness: readinessOf(definition),
    version: statusFor(definition)?.version ?? null,
  }))

  // The runtime being looked at: the one picked in the rail, otherwise the one in use, otherwise the first in the rail that can run.
  const [picked, setPicked] = useState<AgentRuntime | null>(null)
  // The first entry is looked at instead of a runtime: at first while it is in use, then when picked in the rail.
  const [onLead, setOnLead] = useState(value === null)
  const leading = lead !== undefined && onLead
  const browsed = leading ? undefined : (entries.find((entry) => entry.definition.id === (picked ?? valueRuntime)) ?? entries.find((entry) => canRun(entry.readiness)) ?? entries[0])
  const browsedId = browsed?.definition.id
  const leadTab = `${tabBase}-lead`

  const reasonOf = (provider: Provider) => unavailableOf(provider, agentId)
  const blocked = (provider: Provider) => blocks(reasonOf(provider))
  // The provider in use when the picker opened leads its group; it stays there while models are chosen, so rows don't move under the pointer.
  const [first] = useState(value?.provider)
  const own = orderProviders(
    providers.filter((provider) => provider.runtime === browsedId),
    first,
    blocked,
  )
  const grouped = own.length > 1

  const [query, setQuery] = useState('')
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set(value ? [value.provider] : []))
  // When the chosen provider changes (chosen here, or from outside such as a server refresh of the form), look at its runtime and open it.
  const [seen, setSeen] = useState(value?.provider)
  if (value?.provider !== seen) {
    setSeen(value?.provider)
    setPicked(null)
    setOnLead(value === null)
    if (value) setExpanded((current) => new Set(current).add(value.provider))
  }
  // Models: a runtime with several providers reads them all at once (its groups and its search need them), and reads an open provider on
  // its own only when that read has no list for it; a runtime with one provider reads that provider's; the chosen model's provider is read
  // for its thinking levels.
  const all = useAllModels(grouped)
  const fromAll = (id: string) => all.data?.get(id)
  // The chosen model's provider is read only when the catalog has it (one that left the catalog, such as an uninstalled plugin's, has no
  // models to read).
  const chosenListed = value !== null && (catalog.data === undefined || valueProvider !== undefined)
  const wanted = [
    ...new Set([
      ...(grouped ? own.filter((provider) => expanded.has(provider.id) && !all.isLoading && !fromAll(provider.id)).map((provider) => provider.id) : own.map((provider) => provider.id)),
      ...(value && chosenListed ? [value.provider] : []),
    ]),
  ]
  const reads = useModelsOf(wanted)
  const models = new Map<string, readonly Schemas['Model'][] | undefined>(grouped ? own.map((provider) => [provider.id, fromAll(provider.id)]) : [])
  wanted.forEach((id, index) => models.set(id, reads[index]?.data?.models ?? models.get(id)))
  const reading = new Set([...(grouped && all.isLoading ? own.map((provider) => provider.id) : []), ...wanted.filter((_, index) => reads[index]?.isLoading)])
  // Why a provider's models could not be read (its own read failed and the read of all models has no list for it).
  const failures = new Map(wanted.flatMap((id, index) => (reads[index]?.isError && !models.get(id) ? [[id, reads[index].error] as const] : [])))
  // Only the failures of the providers listed here show in the list.
  const listedFailures = new Map([...failures].filter(([id]) => own.some((provider) => provider.id === id)))
  const usableNow = (provider: Provider) => provider.usable === true && !blocked(provider)
  const { rows, hidden } = buildRows({ providers: own, models, expanded, grouped, query, limit: MODEL_LIMIT, usableNow })
  const pickable = rows.filter((row) => row.kind !== 'section')

  const chosen = value ? modelKey(value.provider, value.modelId) : null
  const chosenModel = value ? models.get(value.provider)?.find((model) => model.id === value.modelId) : undefined
  const levels = choosableLevels(chosenModel)
  // A model with nothing to choose says why: a program a person added decides for itself; a model that thinks has its level built in.
  const noLevelsNote = !chosenModel ? null : lookup.get(valueRuntime)?.acpCommandId ? t.runtime.picker.programThinking : chosenModel.reasoning ? t.runtime.picker.fixedThinking : t.runtime.picker.noThinking

  // The keyboard's row: kept while it is still listed, otherwise the chosen model, otherwise the first row.
  const [active, setActive] = useState<string | null>(null)
  const activeKey = pickable.some((row) => row.key === active) ? active : (pickable.find((row) => row.key === chosen)?.key ?? pickable[0]?.key ?? null)

  const toggle = (providerId: string) =>
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(providerId)) next.delete(providerId)
      else next.add(providerId)
      return next
    })
  const choose = (provider: Provider, model: Schemas['Model']) => {
    if (blocked(provider)) return
    onChoose({ model: { provider: provider.id, modelId: model.id }, thinkingLevel: thinkingLevel !== null && choosableLevels(model).includes(thinkingLevel) ? thinkingLevel : null })
  }
  const browse = (id: AgentRuntime) => {
    setPicked(id)
    setOnLead(false)
    setQuery('')
  }

  const readiness = browsed?.readiness
  const note = valueProvider ? unavailableNote(valueProvider, agentId) : null
  const showCurrent = value && catalog.data && (!valueProvider || (models.get(value.provider) !== undefined && !chosenModel))
  // The model in use stays named when the catalog or its provider's models cannot be read.
  const showInUse = value && (catalog.isError || failures.has(value.provider))
  // A line at the foot of the model list on whose login the runtime runs: its own login on this computer (with the path that was found),
  // or the accounts and keys the platform keeps.
  const browsedStatus = browsed ? statusFor(browsed.definition) : undefined
  const colophon =
    browsed &&
    (browsed.definition.acpCommandId ? t.runtime.picker.addedNote(browsed.definition.name) : browsed.definition.capabilities.accounts === 'local' ? t.runtime.providerNote(browsed.definition.name) : t.runtime.picker.managed)

  const showColophon = Boolean(colophon && !(lead && leading) && !catalog.isError && !(browsed && readiness?.state === 'problem'))
  const hasContextStrip = Boolean(showColophon || note || showCurrent || showInUse)
  const colophonTitle = colophon ? (browsedStatus?.binaryPath ? `${colophon} · ${browsedStatus.binaryPath}` : colophon) : undefined

  return (
    <div
      role="group"
      aria-labelledby={labelledBy}
      aria-label={labelledBy ? undefined : t.runtime.picker.label}
      aria-invalid={invalid || undefined}
      className={cn(
        'flex min-w-0 flex-col overflow-hidden bg-surface-elevated text-ink',
        variant === 'inline' &&
          'rounded-panel border border-hairline-strong shadow-surface aria-invalid:border-red aria-invalid:ring-1 aria-invalid:ring-red',
        variant === 'popover' && 'min-h-0 flex-1',
      )}
    >
      {/* Band 1: compact horizontal runtime tab bar */}
      <div ref={railRef} className="shrink-0">
        {lookup.error ? (
          <p className="border-b border-hairline px-3 py-2 text-[12.5px] font-medium text-red-text">{describeError(lookup.error)}</p>
        ) : (
          <RunnerRail
            entries={entries}
            lead={
              lead && {
                ...lead,
                tabId: leadTab,
                label: [lead.name, lead.stateText, value === null ? t.runtime.picker.current : null].filter(Boolean).join(', '),
                selected: leading,
                inUse: value === null,
                onBrowse: () => setOnLead(true),
              }
            }
            browsed={browsedId}
            current={valueRuntime}
            tabId={tabId}
            panelId={panelId}
            onBrowse={browse}
            // Down arrow leaves the tab bar for the model search, or for the first control in the lead panel.
            onLeave={() => (leading ? panelRef.current?.querySelector<HTMLElement>('button:not([disabled]), a[href], input, textarea')?.focus() : searchRef.current?.focus())}
          />
        )}
      </div>

      {/* Band 2: seamless search + compact model tree (or setup guidance / lead panel) */}
      <div
        ref={panelRef}
        id={panelId}
        role="tabpanel"
        aria-labelledby={leading ? leadTab : browsedId ? tabId(browsedId) : undefined}
        className="flex min-h-0 min-w-0 flex-1 flex-col"
      >
        {lead && leading ? (
          <div className="max-h-[260px] overflow-y-auto">{lead.panel}</div>
        ) : catalog.isError ? (
          <div className="grid gap-1 px-3 py-3 text-[12.5px] text-red-text">
            <p className="font-semibold">{t.config.basic.catalogUnavailable}</p>
            <p>{t.config.basic.catalogReason(describeError(catalog.error))}</p>
          </div>
        ) : browsed && readiness?.state === 'problem' ? (
          <RunnerSetup runtime={browsed.definition} problem={readiness.problem} version={browsed.version} />
        ) : (
          <ModelList
            rows={rows}
            hidden={hidden}
            grouped={grouped}
            query={query}
            onQuery={setQuery}
            active={activeKey}
            onActive={setActive}
            chosen={chosen}
            loading={catalog.isLoading || reading.size > 0}
            reading={reading}
            failures={listedFailures}
            reasonOf={reasonOf}
            blocked={blocked}
            onToggle={toggle}
            onChoose={choose}
            onRail={() => railRef.current?.querySelector<HTMLElement>('[role="tab"][tabindex="0"]')?.focus()}
            onThinking={() => (thinkingRef.current?.querySelector<HTMLElement>('[role="radio"][data-state="checked"]') ?? thinkingRef.current?.querySelector<HTMLElement>('[role="radio"]:not([disabled])'))?.focus()}
            autoFocus={variant === 'popover'}
            listMaxHeightClass={variant === 'popover' ? 'max-h-[240px]' : 'max-h-[220px]'}
            searchRef={searchRef}
          />
        )}
      </div>

      {/* Band 3: subtle single-line context strip */}
      {hasContextStrip && (
        <div className="flex shrink-0 flex-col gap-0.5 border-t border-hairline px-3 py-1.5 text-[11.5px] leading-snug text-ink-500">
          {showColophon && colophon && (
            <p title={colophonTitle} className="flex min-w-0 items-center gap-1.5">
              <span className="min-w-0 truncate">{colophon}</span>
              {browsedStatus?.binaryPath && (
                <span className="max-w-[38%] shrink-0 truncate font-mono text-[10.5px] text-ink-500/80">{browsedStatus.binaryPath}</span>
              )}
            </p>
          )}
          {note && (
            <p role="note" title={note} className="truncate text-ink-700">
              {note}
            </p>
          )}
          {showCurrent && (
            <p role="note" title={t.runtime.picker.notInCatalog(`${value.provider} / ${value.modelId}`)} className="truncate text-ink-700">
              {t.runtime.picker.notInCatalog(`${value.provider} / ${value.modelId}`)}
            </p>
          )}
          {showInUse && (
            <p role="note" title={`${t.runtime.picker.inUse} ${value.provider} / ${value.modelId}`} className="truncate text-ink-700">
              {`${t.runtime.picker.inUse} `}
              <span className="font-mono">{`${value.provider} / ${value.modelId}`}</span>
            </p>
          )}
        </div>
      )}

      {/* Band 4: compact horizontal thinking pill bar */}
      <div ref={thinkingRef} className="shrink-0">
        <ThinkingRuler
          value={thinkingLevel}
          levels={levels}
          defaultLevel={chosenModel?.defaultThinkingLevel}
          hint={!value ? (lead?.thinkingNote ?? t.runtime.picker.noModel) : levels.length === 0 ? noLevelsNote : null}
          onChange={onThinkingChange}
        />
      </div>
    </div>
  )
}
