import { useEffect, useRef, useState, type RefObject } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, type Schemas } from '@/api/client'
import { consoleKeys, markMessageRead } from '@/api/console-inbox'
import { useReplyInbox } from '@/api/inbox'
import { keys } from '@/api/queries'

type Message = Schemas['InboxMessage']

// 原题已不能回答时服务端返回的错误：只提交一次，不改成回复别的消息，也不自动重试。
const terminal = ['already_answered', 'question_expired', 'question_closed', 'task_conflict', 'not_found']
export const isTerminalAnswerError = (error: unknown) => error instanceof ApiError && terminal.includes(error.code)

// 回答一条问题：点选项（optionId）或用输入框写文字（text），始终提交到这条原消息。
// 是否记为答案以响应里的 answerTo 为准：answerTo 不是这条原题时，文字已存为普通留言（outcome 为 ordinary）。
// 被拒绝时错误详情里带回最新的问题，在页面上替换原来的状态。
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
      // 先放进线索的缓存，随后的刷新失败也不丢掉这次的结果。
      client.setQueryData<Schemas['InboxThread']>(keys.inboxThread(result.threadId), (current) =>
        current ? { ...current, messages: [...current.messages.filter((item) => item.id !== result.id), result] } : current,
      )
      setOutcome(result.answerTo?.messageId === message.id ? 'answered' : 'ordinary')
      return result
    } catch (error) {
      if (isTerminalAnswerError(error)) setOutcome('rejected')
      throw error
    } finally {
      // 原题所在的各处（线索、Agent 对话、任务节点的 Inbox 记录、任务详情）与待你处理都重新获取最新状态。
      for (const queryKey of [keys.inboxThread(message.threadId), ['consoleMessages'], consoleKeys.pending, ['taskInbox'], ['task']]) void client.invalidateQueries({ queryKey })
    }
  }
  return { submit, pending: reply.isPending, error: reply.error, outcome, latest, reset: () => { reply.reset(); setOutcome(null) } }
}

// 发给操作者的未读消息大部分进入视野时标为已读。浏览器不支持 IntersectionObserver 时不自动标记。
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
          // 标记失败只影响已读状态；这条消息下次显示时再标。
          marked.current = false
        })
      },
      { threshold: 0.6 },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [client, ref, message.id, unread])
}
