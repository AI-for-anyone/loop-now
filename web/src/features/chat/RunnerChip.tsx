import { memo, useEffect, useId, useRef, useState } from 'react'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { useQueryClient } from '@tanstack/react-query'
import { CheckIcon, ChevronDownIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { keys, useAllModels, useModelProviders, useModels, useModelsOf, useRuntimes, useUpdateAgent } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ModelList } from '@/components/runner-picker/ModelList'
import { blocks, unavailableOf } from '@/components/runner-picker/provider-access'
import { RunnerSetup } from '@/components/runner-picker/RunnerSetup'
import { RuntimeMark } from '@/components/runner-picker/RuntimeMark'
import { buildRows, modelKey, orderProviders } from '@/components/runner-picker/runner-rows'
import { PermissionModeIcon } from '@/features/permissions/PermissionModePicker'
import { t } from '@/i18n'
import { shortModelName } from '@/lib/model-name'
import { asksByOwnRules, canRunAgent, effectiveMode, PERMISSION_MODES } from '@/lib/permissions'
import { canRun, railOrder, runtimeReadiness, statusOf, useRuntimeLookup, type AgentRuntime, type RuntimeDefinition } from '@/lib/runtime'
import { choosableLevels } from '@/lib/thinking'
import { cn } from '@/lib/utils'

type OpenMenu = 'harness' | 'model' | 'thinking' | 'permissions' | null
type ThinkingLevel = Schemas['ThinkingLevel']
type PermissionMode = Schemas['PermissionMode']

const SAVED_MS = 4000
const MODEL_LIMIT = 200

const TRIGGER_CLASS =
  'inline-flex h-7 max-w-full min-w-0 cursor-pointer items-center gap-1.5 rounded-[7px] border border-transparent bg-transparent px-2 text-[12px] font-medium whitespace-nowrap text-ink-700 transition-colors outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus aria-expanded:bg-surface-hover aria-expanded:text-ink'

const POPOVER_CLASS =
  'z-50 flex flex-col overflow-hidden rounded-[11px] border border-hairline-strong bg-surface-elevated text-ink shadow-floating outline-none'

function thinkingBarCount(level: string | null): number {
  if (!level || level === 'off') return 0
  if (level === 'minimal') return 1
  if (level === 'low') return 2
  if (level === 'medium') return 3
  return 4
}

// Compact 4-bar visual intensity indicator for thinking level.
function ThinkingBars({ level }: { level: string | null }) {
  const lit = thinkingBarCount(level)
  const heights = [4, 6, 8.5, 11]
  return (
    <span aria-hidden="true" className="inline-flex h-3 w-3.5 shrink-0 items-end gap-[1.5px]">
      {heights.map((height, index) => (
        <span
          key={height}
          style={{ height }}
          className={cn(
            'w-[2.5px] rounded-[1px] transition-colors',
            index < lit ? 'bg-riso-blue' : 'bg-ink-500/30',
          )}
        />
      ))}
    </span>
  )
}

// Four independent compact inline menus docked inside the chat composer toolbar:
// 1) Harness (runtime with single-color ink-frame logo; collapses to logo + chevron on narrow screens)
// 2) Model (searchable provider/model dropdown with concise short model names)
// 3) Thinking level (shown when the chosen model supports selectable thinking levels)
// 4) Permissions (4 permission modes with runtime constraints)
// Every choice saves immediately via PATCH /agents/{id} and applies from the next message.
export const RunnerChip = memo(function RunnerChip({ agent }: { agent: Schemas['Agent'] }) {
  const lookup = useRuntimeLookup()
  const client = useQueryClient()
  const update = useUpdateAgent(agent.id)
  const [openMenu, setOpenMenu] = useState<OpenMenu>(null)
  const [browsedRuntime, setBrowsedRuntime] = useState<AgentRuntime | null>(null)
  const [setupTarget, setSetupTarget] = useState<AgentRuntime | null>(null)
  const [query, setQuery] = useState('')
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set([agent.model.provider]))
  const [activeRow, setActiveRow] = useState<string | null>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const openMenuRef = useRef<OpenMenu>(null)
  const thinkLabelId = useId()
  const permLabelId = useId()
  const setMenu = (next: OpenMenu) => {
    openMenuRef.current = next
    setOpenMenu(next)
  }

  // Optimistic draft merged in order of edits; out-of-order PATCH answers only commit the latest turn.
  const [draft, setDraft] = useState<Schemas['AgentUpdate']>({})
  const inFlight = useRef(0)
  const latest = useRef(0)
  const [failure, setFailure] = useState<unknown>(null)
  const [savedAt, setSavedAt] = useState<number | null>(null)
  useEffect(() => {
    if (savedAt === null) return
    const timer = setTimeout(() => setSavedAt(null), SAVED_MS)
    return () => clearTimeout(timer)
  }, [savedAt])

  const ref = draft.model ?? agent.model
  const level = 'thinkingLevel' in draft ? (draft.thinkingLevel ?? null) : (agent.thinkingLevel ?? null)
  const mode = draft.permissionMode ?? agent.permissionMode
  const runtime = draft.model ? lookup.forProvider(ref.provider) : lookup.get(agent.runtime)
  const currentModels = useModels(ref.provider)
  const currentModel = currentModels.data?.models.find((candidate) => candidate.id === ref.modelId)
  const levels = choosableLevels(currentModel)
  const shownLevel = level ?? currentModel?.defaultThinkingLevel ?? null
  const levelText = shownLevel && (!currentModel || levels.length > 0) ? (t.config.basic.thinking.levels[shownLevel] ?? shownLevel) : null

  const bundled = runtime?.capabilities.bundled ?? false
  const runtimeName = runtime?.name ?? agent.runtime
  const modelShort = shortModelName(currentModel ?? ref.modelId)
  const copy = t.runtime.picker.chip
  const menus = t.runtime.picker.menus
  const detail = copy.detail(
    runtimeName,
    bundled ? copy.bundled : copy.local,
    ref.provider,
    currentModel ? t.config.basic.modelOption(currentModel.name, currentModel.id) : ref.modelId,
    levelText,
  )

  // Load catalog and runtime statuses once any Harness or Model menu is opened.
  const needsCatalog = openMenu === 'harness' || openMenu === 'model'
  const catalog = useModelProviders(agent.id, { enabled: needsCatalog })
  const statuses = useRuntimes(needsCatalog && lookup.all.some((definition) => !definition.capabilities.bundled))
  const read = { checking: statuses.isLoading, failed: statuses.isError }
  const statusFor = (definition: RuntimeDefinition) => statusOf(statuses.data?.runtimes, definition)
  const readinessOf = (definition: RuntimeDefinition) => runtimeReadiness(definition, { ...read, status: statusFor(definition) })
  const offered = lookup.all.filter((definition) => canRunAgent({ workingDirectory: agent.workingDirectory, permissionMode: mode }, definition))
  const harnessEntries = railOrder(offered, readinessOf).map((definition) => ({
    definition,
    readiness: readinessOf(definition),
    version: statusFor(definition)?.version ?? null,
  }))

  const activeRuntimeId = browsedRuntime ?? runtime?.id ?? agent.runtime
  const activeRuntimeDef = lookup.get(activeRuntimeId) ?? runtime
  const providers = catalog.data?.providers ?? []
  const reasonOf = (provider: Schemas['ModelProvider']) => unavailableOf(provider, agent.id)
  const blocked = (provider: Schemas['ModelProvider']) => blocks(reasonOf(provider))
  const ownProviders = orderProviders(
    providers.filter((provider) => provider.runtime === activeRuntimeId),
    ref.provider,
    blocked,
  )
  const grouped = ownProviders.length > 1
  const all = useAllModels(openMenu === 'model' && grouped)
  const fromAll = (id: string) => all.data?.get(id)
  const wanted =
    openMenu === 'model'
      ? [
          ...new Set(
            grouped
              ? ownProviders.filter((provider) => expanded.has(provider.id) && !all.isLoading && !fromAll(provider.id)).map((provider) => provider.id)
              : ownProviders.map((provider) => provider.id),
          ),
        ]
      : []
  const reads = useModelsOf(wanted)
  const modelMap = new Map<string, readonly Schemas['Model'][] | undefined>(grouped ? ownProviders.map((provider) => [provider.id, fromAll(provider.id)]) : [])
  wanted.forEach((id, index) => modelMap.set(id, reads[index]?.data?.models ?? modelMap.get(id)))
  const reading = new Set([...(grouped && all.isLoading ? ownProviders.map((provider) => provider.id) : []), ...wanted.filter((_, index) => reads[index]?.isLoading)])
  const failures = new Map(wanted.flatMap((id, index) => (reads[index]?.isError && !modelMap.get(id) ? [[id, reads[index].error] as const] : [])))
  const usableNow = (provider: Schemas['ModelProvider']) => provider.usable === true && !blocked(provider)
  const { rows, hidden } = buildRows({ providers: ownProviders, models: modelMap, expanded, grouped, query, limit: MODEL_LIMIT, usableNow })
  const pickable = rows.filter((row) => row.kind !== 'section')
  const chosenKey = modelKey(ref.provider, ref.modelId)
  const activeModelKey = pickable.some((row) => row.key === activeRow) ? activeRow : (pickable.find((row) => row.key === chosenKey)?.key ?? pickable[0]?.key ?? null)

  const save = (body: Schemas['AgentUpdate'], closeOnSuccess: boolean) => {
    setDraft((current) => ({ ...current, ...body }))
    setFailure(null)
    inFlight.current += 1
    const turn = ++latest.current
    void update
      .mutateAsync(body)
      .then((next) => {
        if (turn === latest.current) client.setQueryData(keys.agent(agent.id), next)
        setSavedAt(Date.now())
        if (closeOnSuccess) {
          setMenu(null)
          setBrowsedRuntime(null)
        }
      })
      .catch((error: unknown) => setFailure(error))
      .finally(() => {
        inFlight.current -= 1
        if (inFlight.current === 0) setDraft({})
      })
  }

  const chooseModel = (provider: Schemas['ModelProvider'], model: Schemas['Model']) => {
    const nextLevels = choosableLevels(model)
    const nextThinking: ThinkingLevel | null = level && nextLevels.includes(level) ? level : null
    // Only openai-codex takes a speed: leaving it clears the speed the Agent had.
    const tier = provider.id !== 'openai-codex' && agent.serviceTier && agent.serviceTier !== 'default' ? { serviceTier: null } : {}
    save({ model: { provider: provider.id, modelId: model.id }, thinkingLevel: nextThinking, ...tier }, true)
  }

  const toggleProvider = (providerId: string) =>
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(providerId)) next.delete(providerId)
      else next.add(providerId)
      return next
    })

  const openChange = (menu: Exclude<OpenMenu, null>, next: boolean) => {
    if (next) {
      setMenu(menu)
      setFailure(null)
      if (menu === 'harness') {
        setSetupTarget(null)
      } else if (menu === 'model') {
        setQuery('')
        setActiveRow(null)
        setExpanded((current) => new Set(current).add(ref.provider))
      }
    } else if (openMenuRef.current === menu) {
      setMenu(null)
      setBrowsedRuntime(null)
      setSetupTarget(null)
    }
  }

  const keepFocusOnSwitch = (event: Event) => {
    if (openMenuRef.current !== null) event.preventDefault()
  }

  const ignoreSiblingTrigger = (event: { target: EventTarget | null; preventDefault: () => void }) => {
    const target = event.target as HTMLElement | null
    if (target?.closest('[data-composer-menu-trigger]')) event.preventDefault()
  }

  const selectHarness = (entry: (typeof harnessEntries)[number]) => {
    if (!canRun(entry.readiness)) {
      setSetupTarget(entry.definition.id)
      return
    }
    setSetupTarget(null)
    if (entry.definition.id === runtime?.id) {
      setMenu(null)
      setBrowsedRuntime(null)
      return
    }
    // Switch to browsing the chosen Harness's models so the user picks the target model cleanly in one click.
    const nextProviders = providers.filter((provider) => provider.runtime === entry.definition.id)
    setBrowsedRuntime(entry.definition.id)
    setQuery('')
    setActiveRow(null)
    if (nextProviders.length > 0) {
      setExpanded(new Set([nextProviders[0].id]))
    }
    setMenu('model')
  }

  const setupEntry = setupTarget ? harnessEntries.find((entry) => entry.definition.id === setupTarget) : undefined
  const onlyFull = runtime !== undefined && !runtime.capabilities.permissionModes.includes('supervised')
  const thinkingScale = [...levels].reverse()
  const thinkingMarks = [
    {
      value: '__default__',
      level: currentModel?.defaultThinkingLevel ?? null,
      text: currentModel?.defaultThinkingLevel
        ? t.config.basic.thinking.defaultWithLevel(t.config.basic.thinking.levels[currentModel.defaultThinkingLevel] ?? currentModel.defaultThinkingLevel)
        : t.config.basic.thinking.default,
    },
    ...thinkingScale.map((lv) => ({ value: lv, level: lv, text: t.config.basic.thinking.levels[lv] ?? lv })),
  ]

  const permNotes = runtime
    ? [
        !runtime.capabilities.permissionModes.includes('supervised')
          ? t.permissions.onlyFull(runtime.name)
          : effectiveMode(mode, runtime) !== mode
            ? t.permissions.fallback(runtime.name, t.permissions.modes[mode].name)
            : null,
        asksByOwnRules(mode, runtime) ? t.permissions.ownRules(runtime.name) : null,
      ].filter((line): line is string => line !== null)
    : []

  return (
    <span className="inline-flex max-w-full min-w-0 flex-wrap items-center gap-0.5">
      {/* 1. Harness menu */}
      <PopoverPrimitive.Root open={openMenu === 'harness'} onOpenChange={(next) => openChange('harness', next)}>
        <PopoverPrimitive.Trigger
          type="button"
          data-composer-menu-trigger="harness"
          title={detail}
          aria-label={menus.harnessLabel(runtimeName)}
          className={TRIGGER_CLASS}
        >
          {runtime && <RuntimeMark runtime={runtime} className="size-5" />}
          <span className="hidden truncate sm:inline">{runtimeName}</span>
          <ChevronDownIcon aria-hidden="true" className="size-3 shrink-0 opacity-50" />
        </PopoverPrimitive.Trigger>
        <PopoverPrimitive.Portal>
          <PopoverPrimitive.Content
            role="dialog"
            aria-label={menus.harnessTitle}
            side="top"
            align="start"
            sideOffset={8}
            collisionPadding={12}
            onCloseAutoFocus={keepFocusOnSwitch}
            onInteractOutside={ignoreSiblingTrigger}
            className={cn(POPOVER_CLASS, setupEntry ? 'w-[min(380px,calc(100vw-24px))]' : 'w-[min(256px,calc(100vw-24px))]')}
          >
            <div className="px-2.5 pt-2 pb-1 text-[11px] font-semibold text-ink-500">{menus.harnessTitle}</div>
            <div role="listbox" aria-label={menus.harnessTitle} className="flex flex-col gap-0.5 p-1 pt-0">
              {harnessEntries.map((entry) => {
                const isCurrent = entry.definition.id === runtime?.id
                const isProblem = entry.readiness.state === 'problem'
                const problemLabel =
                  entry.readiness.state === 'problem'
                    ? entry.definition.acpCommandId && entry.readiness.problem === 'not_found'
                      ? t.runtime.picker.addedNotFound
                      : t.runtime.picker.problem[entry.readiness.problem]
                    : null
                return (
                  <button
                    key={entry.definition.id}
                    type="button"
                    role="option"
                    aria-selected={isCurrent}
                    onClick={() => selectHarness(entry)}
                    className={cn(
                      'flex w-full cursor-pointer items-center gap-2 rounded-ticket px-2 py-1.5 text-left text-[12.5px] font-medium text-ink transition-colors outline-none hover:bg-surface-hover focus-visible:shadow-focus',
                      isCurrent && 'bg-surface-active',
                      isProblem && 'opacity-75',
                    )}
                  >
                    <RuntimeMark runtime={entry.definition} className="size-5 shrink-0" />
                    <span className="flex min-w-0 flex-1 items-center gap-1.5">
                      <span className="truncate">{entry.definition.name}</span>
                      {entry.definition.capabilities.bundled && (
                        <span className="shrink-0 rounded-full border border-hairline bg-surface-subtle px-1.5 py-px text-[10px] font-medium text-ink-500">
                          {t.runtime.picker.bundled}
                        </span>
                      )}
                      {entry.definition.acpCommandId && !entry.definition.capabilities.bundled && (
                        <span className="shrink-0 rounded-full border border-hairline bg-surface-subtle px-1.5 py-px text-[10px] font-medium text-ink-500">
                          ACP
                        </span>
                      )}
                    </span>
                    {!entry.definition.capabilities.bundled && entry.readiness.state === 'ready' && (
                      <span className="inline-flex shrink-0 items-center gap-1 font-mono text-[10.5px] text-ink-500">
                        <span aria-hidden="true" className="size-1.5 rounded-full bg-green" />
                        {entry.version}
                      </span>
                    )}
                    {problemLabel && (
                      <span className="shrink-0 rounded-full bg-tint-red px-1.5 py-px text-[10px] font-medium text-red-text">
                        {problemLabel}
                      </span>
                    )}
                    {isCurrent && <CheckIcon aria-hidden="true" className="size-3.5 shrink-0 text-riso-blue" strokeWidth={2.5} />}
                  </button>
                )
              })}
            </div>
            {setupEntry && setupEntry.readiness.state === 'problem' && (
              <div className="border-t border-hairline">
                <RunnerSetup runtime={setupEntry.definition} problem={setupEntry.readiness.problem} version={setupEntry.version} />
              </div>
            )}
          </PopoverPrimitive.Content>
        </PopoverPrimitive.Portal>
      </PopoverPrimitive.Root>

      {/* 2. Model menu */}
      <PopoverPrimitive.Root open={openMenu === 'model'} onOpenChange={(next) => openChange('model', next)}>
        <PopoverPrimitive.Trigger
          type="button"
          data-composer-menu-trigger="model"
          title={detail}
          aria-label={menus.modelLabel(modelShort)}
          className={TRIGGER_CLASS}
        >
          <span className="max-w-[160px] truncate">{modelShort}</span>
          <ChevronDownIcon aria-hidden="true" className="size-3 shrink-0 opacity-50" />
        </PopoverPrimitive.Trigger>
        <PopoverPrimitive.Portal>
          <PopoverPrimitive.Content
            role="dialog"
            aria-label={menus.modelTitle}
            side="top"
            align="start"
            sideOffset={8}
            collisionPadding={12}
            onCloseAutoFocus={keepFocusOnSwitch}
            onInteractOutside={ignoreSiblingTrigger}
            className={cn(POPOVER_CLASS, 'max-h-[min(360px,calc(100dvh-140px))] w-[min(324px,calc(100vw-24px))]')}
            onOpenAutoFocus={(event) => {
              event.preventDefault()
              searchRef.current?.focus()
            }}
          >
            {browsedRuntime && activeRuntimeDef && (
              <div className="flex items-center gap-1.5 border-b border-hairline bg-surface-subtle/60 px-3 py-1.5 text-[11.5px] font-medium text-ink-700">
                <RuntimeMark runtime={activeRuntimeDef} className="size-4" />
                <span className="truncate">{activeRuntimeDef.name}</span>
                <span className="ml-auto text-[11px] text-ink-500">{copy.hint}</span>
              </div>
            )}
            <ModelList
              rows={rows}
              hidden={hidden}
              grouped={grouped}
              query={query}
              onQuery={setQuery}
              active={activeModelKey}
              onActive={setActiveRow}
              chosen={chosenKey}
              loading={catalog.isLoading || (ownProviders.length > 0 && reading.size > 0 && rows.length === 0)}
              reading={reading}
              failures={failures}
              reasonOf={reasonOf}
              blocked={blocked}
              onToggle={toggleProvider}
              onChoose={chooseModel}
              onRail={() => setMenu('harness')}
              onThinking={() => {
                if (levels.length > 0) setMenu('thinking')
              }}
              autoFocus
              listMaxHeightClass="max-h-[240px]"
              searchRef={searchRef}
            />
            {failure !== null && (
              <div className="shrink-0 border-t border-hairline px-3 py-2">
                <ErrorAlert error={failure} />
              </div>
            )}
          </PopoverPrimitive.Content>
        </PopoverPrimitive.Portal>
      </PopoverPrimitive.Root>

      {/* 3. Thinking level menu (shown only when the chosen model has choosable thinking levels) */}
      {levels.length > 0 && levelText && (
        <PopoverPrimitive.Root open={openMenu === 'thinking'} onOpenChange={(next) => openChange('thinking', next)}>
          <PopoverPrimitive.Trigger
            type="button"
            data-composer-menu-trigger="thinking"
            aria-label={menus.thinkingLabel(levelText)}
            className={TRIGGER_CLASS}
          >
            <ThinkingBars level={shownLevel} />
            <span className="truncate">{levelText}</span>
            <ChevronDownIcon aria-hidden="true" className="size-3 shrink-0 opacity-50" />
          </PopoverPrimitive.Trigger>
          <PopoverPrimitive.Portal>
            <PopoverPrimitive.Content
              role="dialog"
              aria-label={menus.thinkingTitle}
              side="top"
              align="start"
              sideOffset={8}
              collisionPadding={12}
              onCloseAutoFocus={keepFocusOnSwitch}
              onInteractOutside={ignoreSiblingTrigger}
              className={cn(POPOVER_CLASS, 'w-[208px]')}
            >
              <span id={thinkLabelId} className="px-2.5 pt-2 pb-1 text-[11px] font-semibold text-ink-500">
                {menus.thinkingTitle}
              </span>
              <div role="radiogroup" aria-labelledby={thinkLabelId} className="flex flex-col gap-0.5 p-1 pt-0">
                {thinkingMarks.map((mark) => {
                  const selected = (level === null && mark.value === '__default__') || level === mark.value
                  return (
                    <button
                      key={mark.value}
                      type="button"
                      role="radio"
                      aria-checked={selected}
                      onClick={() => save({ thinkingLevel: mark.value === '__default__' ? null : (mark.value as ThinkingLevel) }, true)}
                      className={cn(
                        'flex w-full cursor-pointer items-center gap-2 rounded-ticket px-2 py-1.5 text-left text-[12.5px] font-medium text-ink transition-colors outline-none hover:bg-surface-hover focus-visible:shadow-focus',
                        selected && 'bg-surface-active',
                      )}
                    >
                      <ThinkingBars level={mark.level} />
                      <span className="min-w-0 flex-1 truncate">{mark.text}</span>
                      {selected && <CheckIcon aria-hidden="true" className="size-3.5 shrink-0 text-riso-blue" strokeWidth={2.5} />}
                    </button>
                  )
                })}
              </div>
              {failure !== null && (
                <div className="border-t border-hairline px-2.5 py-2">
                  <ErrorAlert error={failure} />
                </div>
              )}
            </PopoverPrimitive.Content>
          </PopoverPrimitive.Portal>
        </PopoverPrimitive.Root>
      )}

      {/* 4. Permissions menu */}
      <PopoverPrimitive.Root open={openMenu === 'permissions'} onOpenChange={(next) => openChange('permissions', next)}>
        <PopoverPrimitive.Trigger
          type="button"
          data-composer-menu-trigger="permissions"
          aria-label={menus.permissionsLabel(t.permissions.modes[mode].name)}
          className={TRIGGER_CLASS}
        >
          <PermissionModeIcon mode={mode} className={cn('size-3.5 shrink-0', mode === 'full' ? 'text-pink-text' : 'text-ink-500')} />
          <span className="truncate">{menus.shortPermission[mode]}</span>
          <ChevronDownIcon aria-hidden="true" className="size-3 shrink-0 opacity-50" />
        </PopoverPrimitive.Trigger>
        <PopoverPrimitive.Portal>
          <PopoverPrimitive.Content
            role="dialog"
            aria-label={menus.permissionsTitle}
            side="top"
            align="start"
            sideOffset={8}
            collisionPadding={12}
            onCloseAutoFocus={keepFocusOnSwitch}
            onInteractOutside={ignoreSiblingTrigger}
            className={cn(POPOVER_CLASS, 'w-[min(272px,calc(100vw-24px))]')}
          >
            <span id={permLabelId} className="px-2.5 pt-2 pb-1 text-[11px] font-semibold text-ink-500">
              {menus.permissionsTitle}
            </span>
            <div role="radiogroup" aria-labelledby={permLabelId} className="flex flex-col gap-0.5 p-1 pt-0">
              {PERMISSION_MODES.map((candidate: PermissionMode) => {
                const disabled = onlyFull && candidate !== 'full'
                const selected = candidate === mode
                return (
                  <button
                    key={candidate}
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    disabled={disabled}
                    onClick={() => save({ permissionMode: candidate }, true)}
                    className={cn(
                      'flex w-full cursor-pointer items-start gap-2 rounded-ticket px-2 py-1.5 text-left text-ink transition-colors outline-none hover:bg-surface-hover focus-visible:shadow-focus disabled:pointer-events-none disabled:opacity-40',
                      selected && 'bg-surface-active',
                    )}
                  >
                    <PermissionModeIcon
                      mode={candidate}
                      className={cn('mt-0.5 size-3.5 shrink-0', candidate === 'full' ? 'text-pink-text' : 'text-ink-500')}
                    />
                    <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <span className="text-[12.5px] leading-tight font-medium">{t.permissions.modes[candidate].name}</span>
                      <span className="text-[11px] leading-snug text-ink-500">{t.permissions.modes[candidate].hint}</span>
                    </span>
                    {selected && <CheckIcon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-riso-blue" strokeWidth={2.5} />}
                  </button>
                )
              })}
            </div>
            {permNotes.length > 0 && (
              <div className="border-t border-hairline px-2.5 py-2 text-[11px] leading-snug text-ink-500">
                {permNotes.map((line) => (
                  <p key={line}>{line}</p>
                ))}
              </div>
            )}
            {failure !== null && (
              <div className="border-t border-hairline px-2.5 py-2">
                <ErrorAlert error={failure} />
              </div>
            )}
          </PopoverPrimitive.Content>
        </PopoverPrimitive.Portal>
      </PopoverPrimitive.Root>

      {savedAt !== null && (
        <span role="status" className="ml-1 text-[11.5px] font-medium whitespace-nowrap text-green-text">
          {copy.saved}
        </span>
      )}
    </span>
  )
})
