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

const linkClass = 'font-black underline decoration-riso-pink decoration-2 underline-offset-4 hover:text-riso-blue'

// Who writes, at the foot of the panel; read only while the panel is open.
function WriterLine({ settings }: { settings: ReturnType<typeof useTextGenerationSettings> }) {
  const copy = t.prompts.ai
  const lookup = useRuntimeLookup()
  if (settings.isPending) return <p className="text-[12.5px] font-bold text-ink-500">{copy.checking}</p>
  if (!settings.data) return <p className="text-[12.5px] font-bold text-ink-700">{copy.unknownWriter}</p>
  const { writer } = settings.data
  if (!writer) {
    return (
      <p className="text-[12.5px] leading-snug font-bold text-red-text">
        {copy.noWriter}{' '}
        <Link to={writingSettingsPath} className={linkClass}>
          {copy.setUp}
        </Link>
      </p>
    )
  }
  const runtime = lookup.get(writer.runtime)
  return (
    <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[12.5px] font-bold text-ink-700">
      <span className="inline-flex min-w-0 items-center gap-1.5">
        {runtime && (
          <span className="grid size-5 shrink-0 place-items-center rounded-[6px] bg-riso-blue text-paper-raised shadow-[1.5px_1.5px_0_var(--riso-pink)]">
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
  const settings = useTextGenerationSettings(open)
  const writing = startedAt !== null
  const now = useNow(1000, writing)
  useEffect(() => () => controller.current?.abort(), [])

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
    const signal = (controller.current = new AbortController()).signal
    setStartedAt(Date.now())
    setError(null)
    try {
      const draft = await writePromptDraft({ ...ask, request: request.trim(), currentText: text }, signal)
      onReplace(draft.text)
      const chosen = settings.data?.selection
      setWritten({ before, after: draft.text, fellBack: draft.writer.fellBack && chosen ? copy.fellBack(selectionName(chosen, lookup), writerName(draft.writer, lookup)) : null })
      setOpen(false)
      button.current?.focus()
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
  const example = ask.target === 'project' ? copy.examples.project : hasText ? copy.examples.improve : copy.examples.agent
  const seconds = startedAt === null ? 0 : Math.max(0, Math.floor((now - startedAt) / 1000))

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
        <section id={panelId} aria-labelledby={titleId} className="relative mt-1.5 mb-1 grid min-w-0 gap-3 rounded-card border-2 border-ink bg-tint-yellow px-4 pt-3.5 pb-3 shadow-[4px_4px_0_var(--riso-pink)]">
          {/* A tab pointing up at the button that opened the panel. */}
          <span aria-hidden="true" className="absolute -top-[9px] right-9 size-4 rotate-45 rounded-tl-[3px] border-t-2 border-l-2 border-ink bg-tint-yellow" />
          <h3 id={titleId} className="flex items-center gap-2 font-display-cn text-[21px] leading-none text-ink">
            <Spark className="size-5 text-riso-pink" />
            {copy.title}
          </h3>
          <div className="grid min-w-0 gap-1.5">
            <Label htmlFor={requestId}>{copy.ask}</Label>
            <Textarea
              id={requestId}
              rows={2}
              autoFocus
              className="min-h-[68px] bg-paper-raised text-[14px] read-only:text-ink-500"
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
          <div className="flex min-w-0 flex-wrap items-center justify-end gap-x-3 gap-y-2.5 border-t-2 border-dashed border-ink/30 pt-3">
            <div className="mr-auto min-w-0">
              <WriterLine settings={settings} />
            </div>
            {writing ? (
              <>
                {/* In progress is a fluorescent pink capsule, with the console's spinning tool icon and the time so far. */}
                <p role="status" className="inline-flex items-center gap-1.5 rounded-full bg-riso-pink px-3 py-1 text-[13px] font-black text-ink">
                  <LoaderCircleIcon aria-hidden="true" className="riso-spin size-3.5 motion-reduce:animate-none" strokeWidth={3} />
                  {copy.writing}
                  <span className="font-medium tabular-nums">{copy.seconds(seconds)}</span>
                </p>
                <Button type="button" variant="secondary" size="xs" className="border-2 border-ink" onClick={() => controller.current?.abort()}>
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
          <p role="status" className="inline-flex items-center gap-1.5 rounded-full bg-tint-green px-2.5 py-0.5 text-[12.5px] font-black text-green-text">
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
            }}
          >
            {copy.undo}
          </Button>
          {written.fellBack && <p className="basis-full text-[12.5px] font-bold text-ink-700">{written.fellBack}</p>}
        </div>
      )}
      {children(writing)}
    </div>
  )
}
