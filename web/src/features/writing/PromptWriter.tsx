import { LoaderCircleIcon } from 'lucide-react'
import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { useTextGenerationSettings, writePromptDraft } from '@/api/text-generation'
import { ErrorAlert } from '@/components/ErrorAlert'
import { RuntimeGlyph } from '@/components/runner-picker/RuntimeMark'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { t } from '@/i18n'
import { useRuntimeLookup } from '@/lib/runtime'
import { writingSettingsPath } from '@/lib/scope'
import { useNow } from '@/lib/use-now'
import { Spark } from './marks'
import { selectionName, writerName } from './writer-text'

// What a draft is for: the Prompt of an Agent (an existing one by id, or a new one in a project or the platform with its name as typed so
// far) or of a project. The request and the text in the box are added when writing.
export type PromptWriterAsk = Omit<Schemas['PromptDraftRequest'], 'request' | 'currentText'>

// The last draft put in the box, for Undo: the text it replaced, the draft, and a note when the chosen writer could not write.
interface Written {
  before: string
  after: string
  fellBack: string | null
}

// The longest request the server takes (PromptDraftRequest.request).
const MAX_REQUEST = 4000

const linkClass = 'font-medium underline decoration-ink decoration-1 underline-offset-4 hover:text-ink-700 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:decoration-riso-pink [[data-skin=plate]_&]:decoration-2 [[data-skin=plate]_&]:hover:text-riso-blue'

// Who writes, at the foot of the panel; read only while the panel is open.
function WriterLine({ settings }: { settings: ReturnType<typeof useTextGenerationSettings> }) {
  const copy = t.prompts.ai
  const lookup = useRuntimeLookup()
  if (settings.isPending) return <p className="text-[12.5px] font-medium text-ink-500 [[data-skin=plate]_&]:font-bold">{copy.checking}</p>
  if (!settings.data) return <p className="text-[12.5px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">{copy.unknownWriter}</p>
  const { writer } = settings.data
  if (!writer) {
    return (
      <p className="text-[12.5px] leading-snug font-medium text-red-text [[data-skin=plate]_&]:font-bold">
        {copy.noWriter}{' '}
        <Link to={writingSettingsPath} className={linkClass}>
          {copy.setUp}
        </Link>
      </p>
    )
  }
  const runtime = lookup.get(writer.runtime)
  return (
    <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[12.5px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">
      <span className="inline-flex min-w-0 items-center gap-1.5">
        {runtime && (
          <span className="grid size-5 shrink-0 place-items-center rounded-[6px] border border-hairline bg-surface-control text-ink [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-blue [[data-skin=plate]_&]:text-paper-raised [[data-skin=plate]_&]:shadow-[1.5px_1.5px_0_var(--riso-pink)]">
            <RuntimeGlyph runtime={runtime} className="size-3" />
          </span>
        )}
        <span className="[overflow-wrap:anywhere]">{copy.by(writerName(writer, lookup))}</span>
      </span>
      {/* The dot goes with the link, so a narrow line never ends on a lone dot. */}
      <span className="inline-flex items-center gap-2 whitespace-nowrap">
        <span aria-hidden="true" className="text-ink-500">
          ·
        </span>
        <Link to={writingSettingsPath} className={linkClass}>
          {copy.change}
        </Link>
      </span>
    </p>
  )
}

// "Write with AI" beside a Prompt editor: the label row gets the button; the button opens a small panel above the text (never a dialog on
// top of a dialog) where the person says what they want; the draft replaces the text in the box, with a line to undo it. While it writes,
// the box is locked and Stop aborts the request (the server then stops the CLI); the request is aborted too when the editor goes away.
// Nothing is saved here: the editor's own save stays as it is.
export function PromptWriter({ label, text, onReplace, ask, children }: { label: ReactNode; text: string; onReplace: (text: string) => void; ask: PromptWriterAsk; children: (writing: boolean) => ReactNode }) {
  const copy = t.prompts.ai
  const panelId = useId(), titleId = useId(), requestId = useId(), noteId = useId()
  const lookup = useRuntimeLookup()
  const [open, setOpen] = useState(false)
  const [request, setRequest] = useState('')
  const [startedAt, setStartedAt] = useState<number | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [written, setWritten] = useState<Written | null>(null)
  const controller = useRef<AbortController | null>(null)
  const button = useRef<HTMLButtonElement>(null)
  // Set when a draft has been put in the box: the button gets the focus back once it is enabled again (after the render that closes the
  // panel), so the focus does not fall to the page.
  const refocus = useRef(false)
  const settings = useTextGenerationSettings(open)
  const writing = startedAt !== null
  const now = useNow(1000, writing)
  useEffect(() => () => controller.current?.abort(), [])
  useEffect(() => {
    if (writing || !refocus.current) return
    refocus.current = false
    button.current?.focus()
  }, [writing])

  const hasText = text.trim() !== ''
  const noWriter = settings.data !== undefined && settings.data.writer === null
  const canWrite = !writing && !noWriter && (request.trim() !== '' || hasText)
  // Undo stays offered while the box still holds the draft.
  const undoable = written !== null && written.after === text && !open

  const close = () => {
    setOpen(false)
    setError(null)
    button.current?.focus()
  }
  const write = async () => {
    if (!canWrite) return
    const before = text
    // Who was chosen when the request went out, for the note when someone else wrote it instead.
    const chosen = settings.data?.selection
    const signal = (controller.current = new AbortController()).signal
    setStartedAt(Date.now())
    setError(null)
    try {
      const draft = await writePromptDraft({ ...ask, request: request.trim(), currentText: text }, signal)
      onReplace(draft.text)
      const writer = writerName(draft.writer, lookup)
      setWritten({ before, after: draft.text, fellBack: !draft.writer.fellBack ? null : chosen ? copy.fellBack(selectionName(chosen, lookup), writer) : copy.fellBackUnknown(writer) })
      setOpen(false)
      refocus.current = true
    } catch (cause) {
      if (!signal.aborted) setError(cause)
    } finally {
      if (controller.current?.signal === signal) controller.current = null
      setStartedAt(null)
    }
  }
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
      event.preventDefault()
      void write()
    }
  }
  // Escape stops a draft being written, otherwise closes the panel. A dialog around the editor (New Agent) is kept open: it lets an Escape
  // from inside [data-prompt-writer] through (CreateAgentDialog).
  const onPanelKeyDown = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key !== 'Escape') return
    event.stopPropagation()
    if (writing) controller.current?.abort()
    else close()
  }
  const example = ask.target === 'project' ? copy.examples.project : hasText ? copy.examples.improve : copy.examples.agent
  const elapsed = startedAt === null ? 0 : Math.max(0, now - startedAt)

  return (
    <div className="grid min-w-0 gap-1.5">
      <div className="flex min-w-0 flex-wrap items-end justify-between gap-x-3 gap-y-1.5">
        {label}
        <Button ref={button} type="button" variant="ink" size="xs" aria-expanded={open} aria-controls={open ? panelId : undefined} disabled={writing} onClick={() => (open ? close() : setOpen(true))}>
          <Spark className="size-3.5" />
          {copy.button}
        </Button>
      </div>
      {open && (
        <section id={panelId} aria-labelledby={titleId} data-prompt-writer onKeyDown={onPanelKeyDown} className="relative mt-1.5 mb-1 grid min-w-0 gap-3 rounded-card border border-hairline-strong bg-surface-elevated px-4 pt-3.5 pb-3 shadow-floating [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tint-yellow [[data-skin=plate]_&]:shadow-[4px_4px_0_var(--riso-pink)]">
          {/* A tab pointing up at the button that opened the panel. */}
          <span aria-hidden="true" className="absolute -top-[7px] right-9 size-3.5 rotate-45 rounded-tl-[2px] border-t border-l border-hairline-strong bg-surface-elevated [[data-skin=plate]_&]:-top-[9px] [[data-skin=plate]_&]:size-4 [[data-skin=plate]_&]:rounded-tl-[3px] [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-l-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-tint-yellow" />
          <h3 id={titleId} className="flex items-center gap-2 font-display-cn text-[17px] leading-none font-semibold text-ink [[data-skin=plate]_&]:text-[21px] [[data-skin=plate]_&]:font-normal">
            <Spark className="size-4 text-ink [[data-skin=plate]_&]:size-5 [[data-skin=plate]_&]:text-riso-pink" />
            {copy.title}
          </h3>
          <div className="grid min-w-0 gap-1.5">
            <Label htmlFor={requestId}>{copy.ask}</Label>
            <Textarea
              id={requestId}
              rows={2}
              autoFocus
              maxLength={MAX_REQUEST}
              className="min-h-[68px] bg-surface-control text-[14px] read-only:text-ink-500 [[data-skin=plate]_&]:bg-paper-raised"
              placeholder={example}
              value={request}
              readOnly={writing}
              aria-describedby={noteId}
              onChange={(event) => setRequest(event.target.value)}
              onKeyDown={onKeyDown}
            />
            <p id={noteId} className="text-[12.5px] text-ink-700">
              {hasText ? copy.replaces : copy.fills}
            </p>
          </div>
          <ErrorAlert error={error} />
          <div className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-2.5 border-t border-hairline pt-3 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/30">
            <div className="mr-auto min-w-0">
              <WriterLine settings={settings} />
            </div>
            {writing ? (
              <>
                {/* In progress pill: neutral active surface in mono, fluorescent pink capsule in Plate. */}
                <p role="status" className="inline-flex items-center gap-1.5 rounded-full border border-hairline-strong bg-surface-active px-3 py-1 text-[13px] font-semibold text-ink [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:font-black">
                  <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3.5 motion-reduce:animate-none" strokeWidth={3} />
                  {copy.writing}
                  {/* Ticks every second, so it stays out of what screen readers announce. */}
                  <span aria-hidden="true" className="font-medium tabular-nums">
                    {copy.elapsed(elapsed)}
                  </span>
                </p>
                {/* Stop takes the place of Write: the second click of a double click on Write must not stop the draft it started. */}
                <Button type="button" variant="secondary" size="xs" className="[[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink" onClick={(event) => event.detail < 2 && controller.current?.abort()}>
                  {copy.stop}
                </Button>
              </>
            ) : (
              <>
                <Button type="button" variant="ghost" size="xs" onClick={close}>
                  {t.common.cancel}
                </Button>
                <Button type="button" size="xs" className="min-w-[72px]" disabled={!canWrite} onClick={() => void write()}>
                  {copy.write}
                </Button>
              </>
            )}
          </div>
        </section>
      )}
      {undoable && (
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 py-0.5">
          <p role="status" className="inline-flex items-center gap-1.5 rounded-full border border-hairline bg-tint-green px-2.5 py-0.5 text-[12.5px] font-medium text-green-text [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-black">
            <Spark className="size-3.5" />
            {written.before.trim() ? copy.replaced : copy.filled}
          </p>
          <Button
            type="button"
            variant="ghost"
            size="xs"
            className="h-7 px-1.5"
            onClick={() => {
              onReplace(written.before)
              setWritten(null)
              button.current?.focus()
            }}
          >
            {copy.undo}
          </Button>
          {written.fellBack && (
            <p role="status" className="basis-full text-[12.5px] font-medium text-ink-700 [[data-skin=plate]_&]:font-bold">
              {written.fellBack}
            </p>
          )}
        </div>
      )}
      {children(writing)}
    </div>
  )
}
