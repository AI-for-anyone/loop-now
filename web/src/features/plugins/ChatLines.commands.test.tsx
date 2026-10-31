import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import { CommandLine } from './ChatLines'
import type { Schemas } from '@/api/client'

const base: Schemas['ConsoleCommandResult'] = { handled: true, source: 'platform', plugin: '', reply: null, command: 'compact' }

it('平台命令显示结构化结果而不是插件回复', () => {
  render(<CommandLine command="/compact 保留决定" at="2026-10-31T01:00:00Z" result={{ ...base, tokensBefore: 4200, estimatedTokensAfter: 350 }} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent('平台命令')
  expect(line).toHaveTextContent('上下文已压缩：4200 → 约 350 tokens')
  expect(line).not.toHaveTextContent('插件')
})

it('/copy 没有回复时明确提示没有可复制内容', () => {
  render(<CommandLine command="/copy" at="2026-10-31T01:00:00Z" result={{ ...base, command: 'copy', copyText: null }} />)
  expect(screen.getByRole('note')).toHaveTextContent('没有可复制的回复')
})

it('/resume 显示完整 ID、时间、当前标记和恢复 epoch', () => {
  const { rerender } = render(<CommandLine command="/resume" at="2026-10-31T01:00:00Z" result={{ ...base, command: 'resume', resumeSessions: [
    { id: 'target-full-session-id', updatedAt: '2026-10-31T01:00:00Z', current: false },
    { id: 'current-full-session-id', updatedAt: '2026-10-30T01:00:00Z', current: true },
  ] }} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent('target-full-session-id')
  expect(line).toHaveTextContent('当前 · current-full-session-id')
  expect(line).not.toHaveTextContent('/managed/')
  rerender(<CommandLine command="/resume target-full-session-id" at="2026-10-31T01:00:00Z" result={{ ...base, command: 'resume', resumedSession: { state: 'active', epoch: 2 } }} />)
  expect(screen.getByRole('note')).toHaveTextContent('已恢复会话（epoch 2），页面历史仍保留')
})
