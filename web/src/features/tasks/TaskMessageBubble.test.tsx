import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { makeTaskMessage } from '@/test/tasks'
import { TaskMessageBubble } from './TaskMessageBubble'

const agent = { id: 'agent', name: 'task-checker' }
const sleepId = '00000000-0000-7000-8000-000000005101'
// The old Chinese wake sentence and old Chinese errorMessage cases are in src/zh-CN/features/tasks/TaskMessageBubble.zh.test.tsx.
it.each([
  ['English (current)', `Platform notice: your sleep is due; continue in the original conversation. sleepId=${sleepId}; note: wait for the review bot`],
])('task node sleep_wake is the same one-line wake note: no sticky card, instruction text or sleep ID inline — %s', (_, text) => {
  const message = { ...makeTaskMessage(), role: 'user' as const, origin: 'sleep_wake' as const, wakeSleepId: sleepId, text }
  render(<TaskMessageBubble message={message} agent={agent} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/^Woke up on schedule·.+·wait for the review bot$/u)
  expect(line).not.toHaveTextContent(/Platform notice|5101/u)
  expect(line).toHaveAttribute('title', `wait for the review bot\nSleep ${sleepId}`)
  expect(line.className).not.toMatch(/riso-tilt|riso-yellow/)
})
it('a console message that woke the node early carries only a small tag', () => {
  render(<TaskMessageBubble message={{ ...makeTaskMessage(), role: 'user', origin: 'console_node', text: 'look at this first', wakeSleepId: sleepId }} agent={agent} />)
  expect(screen.getByText('Woke early').closest('[data-early-wake]')).toHaveAttribute('title', `Sleep ${sleepId}`)
  expect(screen.queryByText(/Wake ref|5101/u)).toBeNull()
})
it.each([
  ['task_conflict', 'interrupted', 'The node or its conditions changed, so this message didn\'t run'],
  ['server_restarted', 'interrupted', "The server restarted; this turn wasn't replayed"],
  ['pi_exited', 'failed', 'Agent process exited unexpectedly'],
] as const)('a task turn with errorCode %s shows only its copy: the server text just restates the code', (errorCode, status, label) => {
  render(<TaskMessageBubble message={{ ...makeTaskMessage(), role: 'assistant', status, errorCode, errorMessage: 'The Pi process exited while replying.' }} agent={agent} />)
  expect(screen.getByText(label)).toBeInTheDocument()
  expect(screen.queryByText(/Run error|while replying/)).toBeNull()
})
it.each([
  ['an unknown code', 'some_new_code', 'The task turn failed.'],
])('with %s the server text is the fallback, in English for current turns', (_, errorCode, errorMessage) => {
  render(<TaskMessageBubble message={{ ...makeTaskMessage(), role: 'assistant', status: 'failed', errorCode, errorMessage }} agent={agent} />)
  expect(screen.getByText(`Run error · ${errorMessage}`)).toBeInTheDocument()
})
