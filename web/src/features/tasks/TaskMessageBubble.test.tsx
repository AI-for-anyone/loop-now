import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { makeTaskMessage } from '@/test/tasks'
import { TaskMessageBubble } from './TaskMessageBubble'

const agent = { id: 'agent', name: '任务检查员' }
const sleepId = '00000000-0000-7000-8000-000000005101'
it.each([
  ['English (current)', `Platform notice: your sleep is due; continue in the original conversation. sleepId=${sleepId}; note: wait for the review bot`],
  ['Chinese (sessions from before the switch)', `平台通知：sleep已到期，请接着原会话处理。sleepId=${sleepId}；说明：wait for the review bot`],
])('task node sleep_wake is the same one-line wake note: no sticky card, instruction text or sleep ID inline — %s', (_, text) => {
  const message = { ...makeTaskMessage(), role: 'user' as const, origin: 'sleep_wake' as const, wakeSleepId: sleepId, text }
  render(<TaskMessageBubble message={message} agent={agent} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/^sleep 到期唤醒·.+·wait for the review bot$/u)
  expect(line).not.toHaveTextContent(/Platform notice|平台通知|5101/u)
  expect(line).toHaveAttribute('title', `wait for the review bot\nsleep ${sleepId}`)
  expect(line.className).not.toMatch(/riso-tilt|riso-yellow/)
})
it('a console message that woke the node early carries only a small tag', () => {
  render(<TaskMessageBubble message={{ ...makeTaskMessage(), role: 'user', origin: 'console_node', text: '先看这个', wakeSleepId: sleepId }} agent={agent} />)
  expect(screen.getByText('提前唤醒 sleep').closest('[data-early-wake]')).toHaveAttribute('title', `sleep ${sleepId}`)
  expect(screen.queryByText(/唤醒关联|5101/u)).toBeNull()
})
it.each([
  ['task_conflict', 'interrupted', '节点或执行条件已变化，这条消息没有执行'],
  ['server_restarted', 'interrupted', '服务重启，这一轮没有重放'],
  ['pi_exited', 'failed', 'Agent 进程异常退出'],
] as const)('a task turn with errorCode %s shows only its copy: the server text just restates the code', (errorCode, status, label) => {
  render(<TaskMessageBubble message={{ ...makeTaskMessage(), role: 'assistant', status, errorCode, errorMessage: 'The Pi process exited while replying.' }} agent={agent} />)
  expect(screen.getByText(label)).toBeInTheDocument()
  expect(screen.queryByText(/执行错误|while replying/)).toBeNull()
})
it.each([
  ['an unknown code', 'some_new_code', 'The task turn failed.'],
  ['no code', null, '旧数据：任务回合执行失败'],
])('with %s the server text is the fallback, English or (older turns) Chinese', (_, errorCode, errorMessage) => {
  render(<TaskMessageBubble message={{ ...makeTaskMessage(), role: 'assistant', status: 'failed', errorCode, errorMessage }} agent={agent} />)
  expect(screen.getByText(`执行错误 · ${errorMessage}`)).toBeInTheDocument()
})
