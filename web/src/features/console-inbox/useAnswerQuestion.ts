import { useEffect, useRef, useState, type RefObject } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, type Schemas } from '@/api/client'
import { consoleKeys, markMessageRead } from '@/api/console-inbox'
import { useReplyInbox } from '@/api/inbox'
import { keys } from '@/api/queries'

type Message = Schemas['InboxMessage']

// Errors the server returns when the original question can no longer be answered: submit only once, don't turn it into a
// reply to another message, and don't retry automatically.
const terminal = ['already_answered', 'question_expired', 'question_closed', 'task_conflict', 'not_found']
export const isTerminalAnswerError = (error: unknown) => error instanceof ApiError && terminal.includes(error.code)

// Answer a question: click an option (optionId) or write text in the composer (text); always submitted to the original message.
// Whether it counts as the answer is decided by answerTo in the response: when answerTo isn't this original question, the
// text was saved as an ordinary note (outcome is ordinary).
// When rejected, the error details carry the latest question, which replaces the old state on the page.
export function useAnswerQuestion() {
  const reply = useReplyInbox()
  const client = useQueryClient()
  const [outcome, setOutcome] = useState<'answered' | 'ordinary' | 'rejected' | null>(null)
  const details = reply.error instanceof ApiError ? reply.error.details : null
  const latest = details && typeof details === 'object' && 'question' in details ? (details.question as Schemas['InboxQuestion']) : null
  const submit = async (message: Message, input: { optionId?: string; text?: string; whenBusy?: Schemas['WhenBusy'] }) => {
    setOutcome(null)
    try {
      const result = await reply.mutateAsync({
        messageId: message.id,
        ...(input.optionId ? { optionId: input.optionId } : {}),
        ...(input.text?.trim() ? { text: input.text } : {}),
        ...(message.from.kind === 'agent' ? { whenBusy: input.whenBusy ?? 'queue' } : {}),
      })
      // Put it in the thread cache first, so this result isn't lost even if the following refresh fails.
      client.setQueryData<Schemas['InboxThread']>(keys.inboxThread(result.threadId), (current) =>
        current ? { ...current, messages: [...current.messages.filter((item) => item.id !== result.id), result] } : current,
      )
      setOutcome(result.answerTo?.messageId === message.id ? 'answered' : 'ordinary')
      return result
    } catch (error) {
      if (isTerminalAnswerError(error)) setOutcome('rejected')
      throw error
    } finally {
      // Everywhere the original question appears (the thread, the Agent chat, the task node's Inbox records, the task details)
      // and "Needs you" refetch the latest state.
      for (const queryKey of [keys.inboxThread(message.threadId), ['consoleMessages'], consoleKeys.pending, ['taskInbox'], ['task']]) void client.invalidateQueries({ queryKey })
    }
  }
  return { submit, pending: reply.isPending, error: reply.error, outcome, latest, reset: () => { reply.reset(); setOutcome(null) } }
}

// An unread message to the operator is marked read once most of it scrolls into view. Browsers without IntersectionObserver
// don't mark automatically.
export function useReadOnView(ref: RefObject<HTMLElement | null>, message: Message) {
  const client = useQueryClient()
  const unread = message.to.kind === 'console' && !message.readAt && !message.cancelledAt
  const marked = useRef(false)
  useEffect(() => {
    const el = ref.current
    if (!unread || marked.current || !el || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(
      (entries) => {
        if (marked.current || !entries.some((entry) => entry.isIntersecting)) return
        marked.current = true
        observer.disconnect()
        markMessageRead(client, message.id).catch(() => {
          // A failed mark only affects the read state; the message is marked again the next time it is shown.
          marked.current = false
        })
      },
      { threshold: 0.6 },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [client, ref, message.id, unread])
}
