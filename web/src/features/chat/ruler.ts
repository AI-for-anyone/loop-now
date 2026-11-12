import { useEffect, useRef, useState, type RefObject } from 'react'
import { t } from '@/i18n'
import { sleepWakeNote } from '@/features/sleep/wake-text'
import type { Turn } from './turns'

// 刻度的名称最多取这么多字；显示时再按宽度单行截断。
const PREVIEW_CHARS = 80
// 只处理开头的一段：每次渲染都要给每一轮算一遍名称，粘贴进来的长消息不必整条处理。
const PREVIEW_SOURCE = 2000
function clip(text: string) {
  const head = text.trimStart().slice(0, PREVIEW_SOURCE)
  const chars = Array.from(head.replace(/\s+/gu, ' ').trim())
  if (chars.length > PREVIEW_CHARS) return `${chars.slice(0, PREVIEW_CHARS).join('')}…`
  return chars.join('')
}

// 一轮对话的预览：用户消息的开头；平台提醒与 sleep 唤醒写明是哪种提醒；只有图片时写图片张数；
// 没有用户消息时用回复的开头，回复也没有文字时用通用说法。
export function turnPreview(turn: Turn): string {
  const user = turn.user
  if (user?.origin === 'inbox_reminder') return t.chat.ruler.labeled(t.chat.inboxReminder.title, t.chat.inboxReminder.received)
  if (user?.origin === 'sleep_wake') {
    const note = clip(sleepWakeNote(user.text)?.note ?? '')
    return note ? t.chat.ruler.labeled(t.sleep.woke, note) : t.sleep.woke
  }
  const text = clip(user?.text ?? '')
  if (text) return text
  if (user?.images?.length) return t.chat.queueList.imagesOnly(user.images.length)
  return clip(turn.reply?.text ?? '') || t.chat.ruler.noText
}

// 对话区里哪些轮次（带 data-run-id 的轮次块）此刻有一部分在滚动区的可视范围内，返回它们的执行 ID。
// 轮次增减时补上新的、去掉没了的观察对象（去掉的轮次没有刻度，留在结果里也不影响）；浏览器不支持 IntersectionObserver 时一条也不算可见。
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
