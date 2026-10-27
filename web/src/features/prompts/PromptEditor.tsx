import { useEffect, useId, useReducer, useState } from 'react'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { Label } from '@/components/ui/label'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ApiError } from '@/api/client'
import { t } from '@/i18n'
import { useDirtyForm } from './draft-navigation'
import { createPromptDraft, promptDirty, reducePromptDraft, type PromptText } from './prompt-draft'

export function PromptEditor({ formId, label, saveLabel, text, version, save, readLatest }: {
  formId: string
  label: string
  saveLabel: string
  text: string
  version: number
  save: (value: PromptText) => Promise<PromptText>
  readLatest: () => Promise<PromptText>
}) {
  const [state, dispatch] = useReducer(reducePromptDraft, { text, version }, createPromptDraft)
  const [error, setError] = useState<unknown>(null), [readError, setReadError] = useState<unknown>(null)
  const [saved, setSaved] = useState(false)
  const id = useId(), copy = t.prompts
  useDirtyForm(formId, promptDirty(state))
  useEffect(() => { dispatch({ type: 'latest', value: { text, version } }) }, [text, version])
  const review = async () => {
    dispatch({ type: 'reading' }); setReadError(null)
    try { dispatch({ type: 'reviewed', value: await readLatest() }) }
    catch (e) { dispatch({ type: 'readFailed' }); setReadError(e) }
  }
  const submit = async () => {
    if (state.pending || state.conflict || !promptDirty(state)) return
    const value = { text: state.draft, version: state.base.version }
    dispatch({ type: 'saving' }); setError(null); setSaved(false)
    try { dispatch({ type: 'saved', value: await save(value) }); setSaved(true) }
    catch (e) {
      if (e instanceof ApiError && e.status === 412 && e.code === 'precondition_failed') {
        dispatch({ type: 'conflict' }); await review()
      } else { dispatch({ type: 'failed' }); setError(e) }
    }
  }
  return <div className="grid min-w-0 gap-3.5">
    <div className="grid min-w-0 gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Textarea id={id} rows={7} className="min-h-[160px] max-h-[420px] resize-y font-mono text-[14px]" value={state.draft} disabled={state.pending} onChange={event => { dispatch({ type: 'edit', text: event.target.value }); setSaved(false) }} />
      <p className="text-[12.5px] text-ink-500">{copy.baseline(state.base.version)}</p>
    </div>
    {state.pinned && state.latest.version !== state.base.version && !state.conflict && <Alert variant="warning" role="status"><AlertDescription>{copy.backgroundUpdated}</AlertDescription></Alert>}
    {state.conflict && <div className="grid min-w-0 gap-3" role="group" aria-label={copy.conflictTitle}>
      <Alert variant="warning" role="status"><AlertDescription>{copy.conflictBody}</AlertDescription></Alert>
      {state.reading && <p role="status" className="text-[13px]">{copy.reading}</p>}
      {state.readFailed && <><p className="text-[13px] font-bold text-red-text">{copy.readFailed}</p><ErrorAlert error={readError} /><Button variant="secondary" className="justify-self-start" onClick={() => void review()}>{copy.readAgain}</Button></>}
      {state.review && <>
        <div className="grid min-w-0 gap-3 md:grid-cols-2">
          <div className="grid min-w-0 gap-2"><p className="text-[13px] font-black">{copy.serverVersion(state.review.version)}</p><pre className="max-h-80 overflow-auto rounded-card bg-tint-blue p-4 font-mono text-[13px] leading-[1.7] whitespace-pre-wrap [overflow-wrap:anywhere]">{state.review.text || copy.empty}</pre></div>
          <div className="grid min-w-0 gap-2"><p className="text-[13px] font-black">{copy.draft}</p><pre className="max-h-80 overflow-auto rounded-card bg-tint-yellow p-4 font-mono text-[13px] leading-[1.7] whitespace-pre-wrap [overflow-wrap:anywhere]">{state.draft || copy.empty}</pre></div>
        </div>
        {state.latest.version > state.review.version && <p className="text-[13px] font-bold">{copy.newerThanReview}</p>}
        <Button variant="secondary" className="h-auto min-h-11 max-w-full justify-self-start py-2 text-left whitespace-normal" onClick={() => { dispatch({ type: 'adopt' }); setError(null) }}>{copy.adopt(state.review.version)}</Button>
      </>}
    </div>}
    <ErrorAlert error={error} />
    <div className="flex flex-wrap items-center justify-end gap-3">
      {promptDirty(state) && <p role="status" className="mr-auto rounded-full bg-tint-yellow px-3 py-0.5 text-[13px] font-black">{t.common.unsaved}</p>}
      {saved && !promptDirty(state) && <p role="status" className="mr-auto text-[13px] font-bold text-green-text">{t.common.saved}</p>}
      <Button size="lg" disabled={!promptDirty(state) || state.pending || state.conflict} onClick={() => void submit()}>{state.pending ? t.common.saving : saveLabel}</Button>
    </div>
  </div>
}
