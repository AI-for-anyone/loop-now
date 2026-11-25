import { useEffect, useId, useRef, useState, type FormEvent } from 'react'
import { CheckIcon, LoaderCircleIcon, XIcon } from 'lucide-react'
import { answerPermissionRequest } from '@/api/permissions'
import { ApiError, type Schemas } from '@/api/client'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { PermissionModeIcon } from './PermissionModePicker'

type Request = Schemas['PermissionRequest']
const copy = t.permissions.request

// The request as it was answered, when an answer came back 409 permission_request_answered (answered in the Inbox, or by someone else).
function answeredElsewhere(error: unknown): Request | null {
  if (!(error instanceof ApiError) || error.code !== 'permission_request_answered') return null
  const details = error.details as { request?: Request } | undefined
  return details?.request ?? null
}

const outcomeOf = (request: Pick<Request, 'state' | 'reason'>) => (request.reason && request.state === 'rejected' ? `${copy.states[request.state]} · ${copy.reason(request.reason)}` : copy.states[request.state])

// A request an Agent sent before acting (a command, a file change, a tool…), waiting for a person: what it wants to do, the command or
// change itself, and Approve / Reject; a typed reason rejects it and tells the Agent why. The same question is in the Inbox; whichever
// answer comes first counts, and the other side is told so. Once answered here it shows how until the list drops it.
export function PermissionRequestCard({ request, agentName }: { request: Request; agentName: string }) {
  const [sending, setSending] = useState<'approve' | 'reject' | null>(null)
  const [answered, setAnswered] = useState<Request | null>(null)
  const [elsewhere, setElsewhere] = useState<Request | null>(null)
  const [error, setError] = useState<unknown>(null)
  const [reason, setReason] = useState('')
  const ids = { title: useId(), reason: useId() }
  const done = answered ?? elsewhere
  // The buttons go once it is answered: focus moves to how it was answered, so it is not lost and the outcome is read out.
  const outcome = useRef<HTMLParagraphElement>(null)
  const settled = done !== null
  useEffect(() => {
    if (settled && document.activeElement === document.body) outcome.current?.focus()
  }, [settled])
  const answer = (decision: 'approve' | 'reject', text?: string) => {
    if (sending || done) return
    setSending(decision)
    setError(null)
    answerPermissionRequest(request.id, text ? { decision, reason: text } : { decision })
      .then(setAnswered)
      .catch((cause: unknown) => {
        const stored = answeredElsewhere(cause)
        if (stored) setElsewhere(stored)
        else setError(cause)
      })
      .finally(() => setSending(null))
  }
  const submitReason = (event: FormEvent) => {
    event.preventDefault()
    if (reason.trim()) answer('reject', reason.trim())
  }
  return (
    <section
      aria-labelledby={ids.title}
      data-permission-request={request.id}
      data-state={done?.state ?? 'pending'}
      className="flex min-w-0 flex-col overflow-hidden rounded-card bg-paper-raised shadow-[0_0_0_2px_var(--ink),5px_5px_0_var(--riso-pink)]"
    >
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 border-b-2 border-ink bg-tint-pink-2 px-4 py-2">
        {/* The stamp: waiting in pink; once answered, how, in the outcome's color. */}
        <span
          className={cn(
            'riso-tilt inline-flex items-center gap-1 rounded-stamp px-2 text-[12px] leading-[22px] font-black [--tilt-shadow:inset_0_0_0_1.5px_currentColor] [--tilt:-3deg]',
            !done ? 'text-ink [--tilt-bg:var(--riso-pink)]' : done.state === 'approved' ? 'text-green-text [--tilt-bg:var(--tint-green)]' : done.state === 'rejected' ? 'text-red-text [--tilt-bg:var(--tint-red)]' : 'text-ink-700 [--tilt-bg:var(--paper-raised)]',
          )}
        >
          <PermissionModeIcon mode="supervised" className="size-3.5" />
          {done ? copy.states[done.state] : copy.label}
        </span>
        <h3 id={ids.title} className="min-w-0 text-[14px] font-black [overflow-wrap:anywhere]">
          {copy.asks(agentName, copy.kinds[request.kind])}
        </h3>
        <time dateTime={request.createdAt} className="ml-auto shrink-0 text-[12px] font-bold text-ink-500">
          {t.chat.time(request.createdAt)}
        </time>
      </div>
      <div className="flex min-w-0 flex-col gap-3 px-4 py-3">
        <p className="text-[14.5px] leading-snug font-bold [overflow-wrap:anywhere]">{request.title}</p>
        {request.detail && request.detail !== request.title && (
          <pre className="max-h-60 min-w-0 overflow-auto rounded-input bg-ink px-3.5 py-2.5 font-mono text-[12.5px] leading-[1.55] whitespace-pre-wrap text-[#f3efe4] [overflow-wrap:anywhere]">{request.detail}</pre>
        )}
        {done ? (
          <p ref={outcome} tabIndex={-1} role="status" className={cn('inline-flex items-center gap-1.5 self-start rounded-full px-3 text-[13px] leading-7 font-black outline-none focus-visible:shadow-focus', done.state === 'approved' ? 'bg-tint-green text-green-text' : done.state === 'rejected' ? 'bg-tint-red text-red-text' : 'bg-ink/[0.07] text-ink-700')}>
            {done.state === 'approved' ? <CheckIcon aria-hidden="true" className="size-4" strokeWidth={3} /> : <XIcon aria-hidden="true" className="size-4" strokeWidth={3} />}
            {elsewhere ? copy.answeredElsewhere(outcomeOf(elsewhere)) : outcomeOf(done)}
          </p>
        ) : (
          <>
            <div className="flex flex-wrap items-center gap-2.5">
              <Button type="button" size="sm" disabled={sending !== null} onClick={() => answer('approve')}>
                {sending === 'approve' ? <LoaderCircleIcon aria-hidden="true" className="riso-spin motion-reduce:animate-none" strokeWidth={3} /> : <CheckIcon aria-hidden="true" strokeWidth={3} />}
                {sending === 'approve' ? copy.approving : copy.approve}
              </Button>
              {/* Reject takes the reason when one is typed: it is never dropped on the way. */}
              <Button type="button" size="sm" variant="danger-outline" disabled={sending !== null} onClick={() => answer('reject', reason.trim() || undefined)}>
                {sending === 'reject' ? <LoaderCircleIcon aria-hidden="true" className="riso-spin motion-reduce:animate-none" strokeWidth={3} /> : <XIcon aria-hidden="true" strokeWidth={3} />}
                {sending === 'reject' ? copy.rejecting : copy.reject}
              </Button>
            </div>
            <form onSubmit={submitReason} className="flex min-w-0 flex-wrap items-center gap-2">
              <label htmlFor={ids.reason} className="sr-only">
                {copy.reasonLabel}
              </label>
              <Input id={ids.reason} value={reason} onChange={(event) => setReason(event.target.value)} placeholder={copy.reasonPlaceholder} maxLength={4000} disabled={sending !== null} className="h-9 min-w-0 flex-[1_1_260px] text-[13.5px]" />
              <Button type="submit" size="sm" variant="secondary" disabled={sending !== null || !reason.trim()}>
                {copy.rejectWithReason}
              </Button>
            </form>
            <p className="text-[12px] text-ink-500">{copy.alsoInInbox}</p>
          </>
        )}
        <ErrorAlert error={error} />
      </div>
    </section>
  )
}
