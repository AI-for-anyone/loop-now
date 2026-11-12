import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { CommandLine } from './ChatLines'
import type { Schemas } from '@/api/client'
import { makeRun } from '@/test/api'

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

// 改版“一张印出来的纸”：命令结果的抬头是一张分段的小票，/resume 的会话列表按表格规则排。
it('命令结果的抬头是分段的小票：命令｜来源｜时间，命令与时间用等宽字；结果排在下面，左侧一道虚线', () => {
  render(<CommandLine command="/compact 保留决定" at="2026-10-31T01:00:00Z" result={{ ...base, tokensBefore: 4200, estimatedTokensAfter: 350 }} />)
  const line = screen.getByRole('note')
  const parts = [...line.querySelectorAll('[data-receipt-part]')]
  expect(parts.map(part => part.textContent)).toEqual(['/compact 保留决定', '平台命令', expect.stringMatching(/\d{2}:\d{2}$/)])
  expect(parts[0].querySelector('code')).toHaveClass('font-mono', 'font-bold')
  expect(parts[1]).toHaveClass('border-l-[1.5px]', 'border-dashed')
  expect(parts[2]).toHaveClass('border-l-[1.5px]', 'border-dashed', 'font-mono')
  expect(parts[0].parentElement).toHaveClass('bg-tint-blue', 'rounded-[6px]')
  const reply = screen.getByText('上下文已压缩：4200 → 约 350 tokens')
  expect(reply).toHaveClass('border-l-2', 'border-dashed')
})

it('/resume 的会话列表：上面一道实线，行与行之间虚线；当前会话是黄色标签；ID 与时间用等宽字；恢复是白底藏青描边的按钮', () => {
  render(<CommandLine command="/resume" at="2026-10-31T01:00:00Z" onResume={() => {}} result={{ ...base, command: 'resume', resumeSessions: [
    { id: '12345678-1234-1234-1234-123456789abc', title: '修复 resume 列表', preview: '最近讨论', updatedAt: '2026-10-31T01:00:00Z', current: false },
    { id: 'current-full-session-id', title: '当前的会话', preview: '', updatedAt: '2026-10-30T01:00:00Z', current: true },
  ] }} />)
  const list = screen.getByRole('list', { name: '可恢复的会话' })
  expect(list).toHaveClass('border-t-2', 'border-ink')
  const rows = [...list.querySelectorAll('[role="listitem"]')]
  expect(rows).toHaveLength(2)
  for (const row of rows) expect(row).toHaveClass('border-b-[1.5px]', 'border-dashed')
  expect(screen.getByText('当前')).toHaveClass('bg-riso-yellow', 'text-ink', 'font-black')
  expect(screen.getByTitle('12345678-1234-1234-1234-123456789abc')).toHaveClass('font-mono')
  expect(rows[0].querySelector('time')).toHaveClass('font-mono')
  expect(screen.getByRole('button', { name: '恢复' })).toHaveClass('border-2', 'border-ink', 'bg-paper-raised')
})

// /compact 的结果读返回里的 compactRun.compaction：压缩了写前后的 token；Pi 没有压缩时按原因说明（这时 token 为 null）。
const compaction = (overrides: Partial<Schemas['RunCompaction']>): Schemas['ConsoleCommandResult'] => ({
  ...base,
  command: 'compact',
  tokensBefore: null,
  estimatedTokensAfter: null,
  compactRun: { ...makeRun(), compaction: { instructions: '', outcome: null, skipReason: null, tokensBefore: null, estimatedTokensAfter: null, ...overrides } },
})

it('/compact 压缩了：显示压缩前后的 token（读 compactRun.compaction）', () => {
  render(<CommandLine command="/compact" at="2026-10-31T01:00:00Z" result={compaction({ outcome: 'compacted', tokensBefore: 52000, estimatedTokensAfter: 4100 })} />)
  expect(screen.getByRole('note')).toHaveTextContent('上下文已压缩：52000 → 约 4100 tokens')
})

it.each([
  ['too_small', '会话内容太少，不用压缩'],
  ['already_compacted', '上次压缩之后还没有新内容，不用再压缩'],
  ['cancelled_by_extension', '这次压缩被扩展取消了'],
] as const)('/compact 没有压缩（%s）：说明“%s”，不写 token', (skipReason, wording) => {
  render(<CommandLine command="/compact" at="2026-10-31T01:00:00Z" result={compaction({ outcome: 'skipped', skipReason })} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(wording)
  expect(line).not.toHaveTextContent('上下文已压缩')
  expect(line).not.toHaveTextContent('null')
})

// /copy 写剪贴板失败（Safari 里已不在按键手势内、http 地址没有 navigator.clipboard）：内容不丢，给一个能手动复制的办法。
it('/copy 没能写进剪贴板：说明原因，内容放在只读的框里可以选中，旁边的按钮再复制一次', async () => {
  const writeText = vi.fn(() => Promise.resolve())
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
  render(<CommandLine command="/copy" at="2026-10-31T01:00:00Z" copyFailed result={{ ...base, command: 'copy', copyText: '第一行\n第二行' }} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent('浏览器没有允许控制台写入剪贴板')
  expect(line).not.toHaveTextContent('已复制最近一条成功回复')
  const box = screen.getByRole('textbox', { name: '要复制的内容' })
  expect(box).toHaveValue('第一行\n第二行')
  expect(box).toHaveAttribute('readonly')
  const button = screen.getByRole('button', { name: '复制' })
  await userEvent.click(button)
  expect(writeText).toHaveBeenCalledWith('第一行\n第二行')
  expect(await screen.findByRole('button', { name: '已复制' })).toBeInTheDocument()
})

it('/copy 手动再复制仍然不行：选中框里的内容，方便直接按复制键', async () => {
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
  render(<CommandLine command="/copy" at="2026-10-31T01:00:00Z" copyFailed result={{ ...base, command: 'copy', copyText: '要复制的回复' }} />)
  const box = screen.getByRole<HTMLTextAreaElement>('textbox', { name: '要复制的内容' })
  await userEvent.click(screen.getByRole('button', { name: '复制' }))
  await waitFor(() => expect(box).toHaveFocus())
  expect(box.selectionStart).toBe(0)
  expect(box.selectionEnd).toBe('要复制的回复'.length)
  expect(screen.queryByRole('button', { name: '已复制' })).toBeNull()
})

it('/copy 写成功时照常只写“已复制”，不出现手动复制的框', () => {
  render(<CommandLine command="/copy" at="2026-10-31T01:00:00Z" result={{ ...base, command: 'copy', copyText: '回复' }} />)
  expect(screen.getByRole('note')).toHaveTextContent('已复制最近一条成功回复')
  expect(screen.queryByRole('textbox')).toBeNull()
})
