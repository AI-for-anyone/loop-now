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

it('/resume 显示可读标题、摘要、短 ID 和当前标记，点击恢复传回完整 ID', () => {
  const targetID = '12345678-1234-1234-1234-123456789abc'
  let selected = ''
  const { rerender } = render(<CommandLine command="/resume" at="2026-10-31T01:00:00Z" onResume={id => { selected = id }} result={{ ...base, command: 'resume', resumeSessions: [
    { id: targetID, title: '修复 resume 列表', preview: '最近讨论：显示标题和摘要', updatedAt: '2026-10-31T01:00:00Z', current: false },
    { id: 'current-full-session-id', title: '', preview: '', updatedAt: '2026-10-30T01:00:00Z', current: true },
  ] }} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent('修复 resume 列表')
  expect(line).toHaveTextContent('最近讨论：显示标题和摘要')
  expect(line).toHaveTextContent('12345678…9abc')
  expect(line).toHaveTextContent('无文字消息')
  expect(line).toHaveTextContent('当前')
  expect(line).not.toHaveTextContent(targetID)
  expect(screen.getByTitle(targetID)).toBeInTheDocument()
  expect(screen.getAllByRole('button', { name: '恢复' })).toHaveLength(1)
  screen.getByRole('button', { name: '恢复' }).click()
  expect(selected).toBe(targetID)
  expect(line).not.toHaveTextContent('/managed/')
  rerender(<CommandLine command={`/resume ${targetID}`} at="2026-10-31T01:00:00Z" result={{ ...base, command: 'resume', resumedSession: { state: 'active', epoch: 2 } }} />)
  expect(screen.getByRole('note')).toHaveTextContent('已恢复会话（epoch 2），页面历史仍保留')
})
