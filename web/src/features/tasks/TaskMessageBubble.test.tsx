import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { makeTaskMessage } from '@/test/tasks'
import { TaskMessageBubble } from './TaskMessageBubble'

const agent = { id: 'agent', name: '任务检查员' }
const sleepId = '00000000-0000-7000-8000-000000005101'
it('task node sleep_wake is the same one-line wake note: no sticky card, instruction text or sleep ID inline', () => {
  const message = { ...makeTaskMessage(), role: 'user' as const, origin: 'sleep_wake' as const, wakeSleepId: sleepId, text: `平台通知：sleep已到期，请接着原会话处理。sleepId=${sleepId}；说明：wait for the review bot` }
  render(<TaskMessageBubble message={message} agent={agent} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/^sleep 到期唤醒·.+·wait for the review bot$/u)
  expect(line).not.toHaveTextContent(/平台通知|5101/u)
  expect(line).toHaveAttribute('title', `wait for the review bot\nsleep ${sleepId}`)
  expect(line.className).not.toMatch(/riso-tilt|riso-yellow/)
})
it('a console message that woke the node early carries only a small tag', () => {
  render(<TaskMessageBubble message={{ ...makeTaskMessage(), role: 'user', origin: 'console_node', text: '先看这个', wakeSleepId: sleepId }} agent={agent} />)
  expect(screen.getByText('提前唤醒 sleep').closest('[data-early-wake]')).toHaveAttribute('title', `sleep ${sleepId}`)
  expect(screen.queryByText(/唤醒关联|5101/u)).toBeNull()
})
