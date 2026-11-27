import { useEffect, useId, useMemo, useReducer, useState } from 'react'
import { BookOpenIcon, ChevronDownIcon } from 'lucide-react'
import { ApiError } from '@/api/client'
import { useModelSettings, useSaveModelSettings } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { useDirtyForm } from '@/features/prompts/draft-navigation'
import { createPromptDraft, promptDirty, reducePromptDraft, type PromptText } from '@/features/prompts/prompt-draft'
import { describeError } from '@/lib/errors'
import { t } from '@/i18n'
import { JsonCodeEditor, type JsonDiagnostic } from './JsonCodeEditor'
import { ModelExamplesSheet } from './ModelExamplesSheet'

const asDraft = (settings: { modelsJson: string; revision: number }): PromptText => ({ text: settings.modelsJson, version: settings.revision })

function ModelSettingsEditor({ initial, fullHeight }: { initial: PromptText; fullHeight: boolean }) {
  const copy = t.settings.models
  const [state, dispatch] = useReducer(reducePromptDraft, initial, createPromptDraft)
  const [error, setError] = useState<unknown>(null)
  const [readError, setReadError] = useState<unknown>(null)
  const [saved, setSaved] = useState(false)
  const [inserted, setInserted] = useState<string | null>(null)
  const [diagnostics, setDiagnostics] = useState<JsonDiagnostic[]>([])
  const [examplesOpen, setExamplesOpen] = useState(false)
  const save = useSaveModelSettings()
  const id = useId()
  useDirtyForm('global-model-settings', promptDirty(state))
  useEffect(() => { dispatch({ type: 'latest', value: initial }) }, [initial])

  const review = async () => {
    dispatch({ type: 'reading' })
    setReadError(null)
    try {
      const latest = await save.readLatest()
      dispatch({ type: 'reviewed', value: asDraft(latest) })
    } catch (reason) {
      dispatch({ type: 'readFailed' })
      setReadError(reason)
    }
  }
  const submit = async () => {
    if (state.pending || state.conflict || !promptDirty(state) || diagnostics.length > 0) return
    dispatch({ type: 'saving' })
    setError(null)
    setSaved(false)
    setInserted(null)
    try {
      const result = await save.mutateAsync({ modelsJson: state.draft, expectedRevision: state.base.version })
      dispatch({ type: 'saved', value: asDraft(result) })
      setSaved(true)
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 412 && reason.code === 'precondition_failed') {
        dispatch({ type: 'conflict' })
        await review()
      } else {
        dispatch({ type: 'failed' })
        setError(reason)
      }
    }
  }
  const edit = (text: string) => {
    dispatch({ type: 'edit', text })
    setSaved(false)
    setInserted(null)
    setError(null)
  }

  return <div className={fullHeight ? 'flex min-h-0 flex-1 flex-col gap-3' : 'grid min-w-0 gap-4'}>
    <div className={fullHeight ? 'flex min-h-0 flex-1 flex-col gap-1.5' : 'grid min-w-0 gap-1.5'}>
      <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
        <Label htmlFor={id}>{copy.label}</Label>
        <Button type="button" size="sm" variant="ghost" onClick={() => setExamplesOpen(true)}><BookOpenIcon />{copy.openExamples}</Button>
      </div>
      <JsonCodeEditor id={id} label={copy.label} value={state.draft} disabled={state.pending} fill={fullHeight} diagnostics={diagnostics} onDiagnosticsChange={setDiagnostics} onChange={edit} />
      <p className="shrink-0 text-[12.5px] text-ink-500">{copy.revision(state.base.version)}</p>
    </div>
    {state.pinned && state.latest.version !== state.base.version && !state.conflict && <Alert variant="warning" role="status" className="shrink-0"><AlertDescription>{copy.backgroundUpdated}</AlertDescription></Alert>}
    {state.conflict && <div className="grid min-w-0 shrink-0 gap-3" role="group" aria-label={copy.conflictTitle}>
      <Alert variant="warning" role="status"><AlertDescription>{copy.conflictBody}</AlertDescription></Alert>
      {state.reading && <p role="status" className="text-[13px]">{copy.reading}</p>}
      {state.readFailed && <><p className="text-[13px] font-bold text-red-text">{copy.readFailed}</p><ErrorAlert error={readError} /><Button variant="secondary" className="justify-self-start" onClick={() => void review()}>{copy.readAgain}</Button></>}
      {state.review && <>
        <div className="grid min-w-0 gap-3 md:grid-cols-2">
          <div className="grid min-w-0 gap-2"><p className="text-[13px] font-semibold">{copy.serverVersion(state.review.version)}</p><pre className="max-h-80 overflow-auto rounded-card border border-code-border bg-code-bg p-4 font-mono text-[13px] leading-[1.7] whitespace-pre-wrap text-code-fg [overflow-wrap:anywhere]">{state.review.text || copy.empty}</pre></div>
          <div className="grid min-w-0 gap-2"><p className="text-[13px] font-semibold">{copy.draft}</p><pre className="max-h-80 overflow-auto rounded-card border border-hairline-strong bg-surface-subtle p-4 font-mono text-[13px] leading-[1.7] whitespace-pre-wrap [overflow-wrap:anywhere]">{state.draft || copy.empty}</pre></div>
        </div>
        {state.latest.version > state.review.version && <p className="text-[13px] font-medium">{copy.newerThanReview}</p>}
        <Button variant="secondary" className="h-auto min-h-11 max-w-full justify-self-start py-2 text-left whitespace-normal" onClick={() => { dispatch({ type: 'adopt' }); setError(null) }}>{copy.adopt(state.review.version)}</Button>
      </>}
    </div>}
    {error !== null && <Alert variant="error" role="alert" className="shrink-0"><AlertDescription className="grid gap-2 text-[13.5px] text-ink">
      <p className="font-semibold text-red-text">{copy.saveFailed}</p>
      <details className="grid gap-2">
        <summary className="flex w-fit cursor-pointer items-center gap-1.5 font-medium">{copy.errorDetails}<ChevronDownIcon className="size-4" /></summary>
        <pre className="max-h-56 overflow-auto rounded-card border border-code-border bg-code-bg p-3 font-mono text-[12.5px] whitespace-pre-wrap text-code-fg">{[describeError(error), ...(error instanceof ApiError ? error.fields.map(field => t.errors.field(field.field, field.message)) : [])].join('\n')}</pre>
      </details>
    </AlertDescription></Alert>}
    <div className="flex shrink-0 flex-wrap items-center justify-end gap-3">
      {inserted && <p role="status" className="mr-auto text-[13px] font-medium text-green-text">{inserted}</p>}
      {promptDirty(state) && !inserted && <p role="status" className="mr-auto rounded-full border border-hairline bg-tint-yellow px-3 py-0.5 text-[13px] font-semibold">{t.common.unsaved}</p>}
      {saved && !promptDirty(state) && <p role="status" className="mr-auto text-[13px] font-medium text-green-text">{t.common.saved}</p>}
      <Button size="lg" disabled={!promptDirty(state) || state.pending || state.conflict || diagnostics.length > 0} onClick={() => void submit()}>{state.pending ? t.common.saving : copy.save}</Button>
    </div>
    <ModelExamplesSheet open={examplesOpen} draft={state.draft} onOpenChange={setExamplesOpen} onInsert={(text, title) => { edit(text); setInserted(copy.inserted(title)) }} />
  </div>
}

export function ModelSettingsCard({ fullHeight = false }: { fullHeight?: boolean } = {}) {
  const settings = useModelSettings()
  const copy = t.settings.models
  const initial = useMemo(() => settings.data ? asDraft(settings.data) : null, [settings.data])
  return <div className={fullHeight ? 'flex min-h-0 flex-1 flex-col gap-3' : 'grid min-w-0 gap-4'}>
    <div className="grid shrink-0 gap-1">
      <p className="text-[13.5px] text-ink-700">{copy.description}</p>
      <p className="text-[13px] leading-relaxed text-ink-500">{copy.effect}</p>
    </div>
    {settings.isError && <ErrorAlert error={settings.error} className="shrink-0" />}
    {settings.isPending && <p role="status" className="shrink-0 text-[13px] text-ink-500">{t.common.loading}</p>}
    {settings.data?.loadError && <Alert variant="warning" role="alert" className="shrink-0"><AlertDescription>{settings.data.loadError}</AlertDescription></Alert>}
    {initial && <ModelSettingsEditor initial={initial} fullHeight={fullHeight} />}
  </div>
}
