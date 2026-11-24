import { screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { renderWithQuery } from '@/test/render'
import { makeTaskMessage } from '@/test/tasks'
import { TaskMessageBubble } from '@/features/tasks/TaskMessageBubble'

// Task turns stored before the switch to English: the old Chinese wake sentence and an old Chinese errorMessage without a code.
const agent = { id: 'agent', name: 'task-checker' }
const sleepId = '00000000-0000-7000-8000-000000005101'
it.each([
  ['Chinese (sessions from before the switch)', `平台通知：sleep已到期，请接着原会话处理。sleepId=${sleepId}；说明：wait for the review bot`],
])('task node sleep_wake is the same one-line wake note: no sticky card, instruction text or sleep ID inline — %s', (_, text) => {
  const message = { ...makeTaskMessage(), role: 'user' as const, origin: 'sleep_wake' as const, wakeSleepId: sleepId, text }
  renderWithQuery(<TaskMessageBubble message={message} agent={agent} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/^sleep 到期唤醒·.+·wait for the review bot$/u)
  expect(line).not.toHaveTextContent(/Platform notice|平台通知|5101/u)
  expect(line).toHaveAttribute('title', `wait for the review bot\nsleep ${sleepId}`)
  expect(line.className).not.toMatch(/riso-tilt|riso-yellow/)
})
it.each([
  ['no code', null, '旧数据：任务回合执行失败'],
])('with %s the server text is the fallback, English or (older turns) Chinese', (_, errorCode, errorMessage) => {
  renderWithQuery(<TaskMessageBubble message={{ ...makeTaskMessage(), role: 'assistant', status: 'failed', errorCode, errorMessage }} agent={agent} />)
  expect(screen.getByText(`执行错误 · ${errorMessage}`)).toBeInTheDocument()
})
