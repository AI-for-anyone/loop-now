import { useEffect, useRef, useState, type RefObject } from 'react'
import { t } from '@/i18n'
import { sleepWakeNote } from '@/features/sleep/wake-text'
import type { Turn } from './turns'

// A tick's name takes at most this many characters; on display it is further truncated to one line by width.
const PREVIEW_CHARS = 80
// Only the beginning is processed: every render computes the name of every turn, and a long pasted message needn't be processed whole.
const PREVIEW_SOURCE = 2000
function clip(text: string) {
  const head = text.trimStart().slice(0, PREVIEW_SOURCE)
  const chars = Array.from(head.replace(/\s+/gu, ' ').trim())
  if (chars.length > PREVIEW_CHARS) return `${chars.slice(0, PREVIEW_CHARS).join('')}…`
  return chars.join('')
}

// Preview of a conversation turn: the start of the user message; platform reminders and sleep wakes say which kind of reminder they
// are; with images only, the number of images;
// without a user message, the start of the reply; when the reply has no text either, a generic phrase.
export function turnPreview(turn: Turn): string {
  const user = turn.user
  if (user?.origin === 'inbox_reminder') return t.chat.ruler.labeled(t.chat.inboxReminder.title, t.chat.inboxReminder.received)
  if (user?.origin === 'background') {
    const label = clip(turn.run.triggerBackgroundTasks?.[0]?.label ?? user.run?.triggerBackgroundTasks?.[0]?.label ?? '')
    return label ? t.chat.ruler.labeled(t.background.followUp.ruler, label) : t.background.followUp.ruler
  }
  if (user?.origin === 'sleep_wake') {
    const note = clip(sleepWakeNote(user.text)?.note ?? '')
    return note ? t.chat.ruler.labeled(t.sleep.woke, note) : t.sleep.woke
  }
  const text = clip(user?.text ?? '')
  if (text) return text
  if (user?.images?.length) return t.chat.queueList.imagesOnly(user.images.length)
  return clip(turn.reply?.text ?? '') || t.chat.ruler.noText
}

// Which turns in the conversation area (turn blocks with data-run-id) are at least partly within the scroll area's visible range right
// now; returns their run IDs.
// When turns are added or removed, observe the new ones and stop observing the ones that are gone (removed turns have no ticks, so it
// does no harm if they linger in the result); if the browser doesn't support IntersectionObserver, none count as visible.
export function useVisibleTurns(root: RefObject<HTMLElement | null>, content: RefObject<HTMLElement | null>, keys: string): ReadonlySet<string> {
  const [visible, setVisible] = useState<ReadonlySet<string>>(() => new Set())
  const observer = useRef<IntersectionObserver | null>(null)
  const observed = useRef(new Map<Element, string>())
  const intersecting = useRef(new Set<string>())

  useEffect(() => {
    if (typeof IntersectionObserver === 'undefined' || !root.current) return
    const current = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          const key = observed.current.get(entry.target)
          if (key === undefined) continue
          if (entry.isIntersecting) intersecting.current.add(key)
          else intersecting.current.delete(key)
        }
        const next = intersecting.current
        setVisible((shown) => (shown.size === next.size && [...next].every((key) => shown.has(key)) ? shown : new Set(next)))
      },
      { root: root.current },
    )
    observer.current = current
    const tracked = observed.current
    const seen = intersecting.current
    return () => {
      current.disconnect()
      observer.current = null
      tracked.clear()
      seen.clear()
    }
  }, [root])

  useEffect(() => {
    const current = observer.current
    const inner = content.current
    if (!current || !inner) return
    const blocks = new Map<Element, string>()
    for (const child of inner.children) {
      const key = child instanceof HTMLElement ? child.dataset.runId : undefined
      if (key) blocks.set(child, key)
    }
    for (const [element, key] of observed.current) {
      if (blocks.has(element)) continue
      current.unobserve(element)
      observed.current.delete(element)
      intersecting.current.delete(key)
    }
    for (const [element, key] of blocks) {
      if (observed.current.has(element)) continue
      observed.current.set(element, key)
      current.observe(element)
    }
  }, [content, keys])

  return visible
}
