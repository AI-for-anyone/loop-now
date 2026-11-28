import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { makeBackgroundTask } from '@/test/api'
import { resetThemeForTest, setSkin } from '@/theme/store'
import { BackgroundFollowUpLine } from '@/features/chat/BackgroundFollowUp'
import { BackgroundToolChips } from '@/features/chat/BackgroundToolChip'
import { ToolList } from '@/features/chat/RunStatus'

const NOW = new Date('2026-10-12T08:02:00Z')

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(NOW)
})

afterEach(() => {
  vi.useRealTimers()
  window.localStorage.clear()
  resetThemeForTest()
})

describe('BackgroundToolChips (Chinese UI)', () => {
  it('groups running and ended background tools onto the quiet line with Chinese labels and group popover', async () => {
    const user = userEvent.setup()
    const tasks = [
      makeBackgroundTask({ id: 'a1', toolCallId: 'c1', kind: 'subagent', label: '调查架构 A', startedAt: '2026-10-12T08:00:10Z', status: 'running' }),
      makeBackgroundTask({ id: 'a2', toolCallId: 'c2', kind: 'subagent', label: '调查架构 B', startedAt: '2026-10-12T08:00:20Z', status: 'running' }),
      makeBackgroundTask({ id: 'a3', toolCallId: 'c3', kind: 'subagent', label: '调查架构 C', startedAt: '2026-10-12T08:00:30Z', status: 'running' }),
      makeBackgroundTask({ id: 'b1', toolCallId: 'c4', kind: 'command', label: 'npm test', startedAt: '2026-10-12T08:00:40Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:00Z' }),
      makeBackgroundTask({ id: 'b2', toolCallId: 'c5', kind: 'command', label: 'npm lint', startedAt: '2026-10-12T08:00:50Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:10Z' }),
    ]
    const names = new Map([
      ['c1', 'Agent'],
      ['c2', 'Agent'],
      ['c3', 'Agent'],
      ['c4', 'Bash'],
      ['c5', 'Bash'],
    ])

    render(
      <ToolList
        tools={[{ toolCallId: 'r1', toolName: 'Read', state: 'succeeded', at: '2026-10-12T08:00:00Z' }]}
        after={<BackgroundToolChips tasks={tasks} toolNames={names} onStop={vi.fn(async () => ({}))} />}
      />,
    )

    const runningGroup = screen.getByRole('button', { name: 'Agent 后台运行中，3 个' })
    expect(runningGroup.querySelector('[data-tool-text]')).toHaveTextContent('Agent×3后台运行中')
    expect(runningGroup.querySelector('[data-slot="tool-spinner"]')).not.toBeNull()

    const completedGroup = screen.getByRole('button', { name: 'Bash 后台已完成，2 个' })
    expect(completedGroup.querySelector('[data-tool-text]')).toHaveTextContent('Bash×2后台已完成')

    await user.click(runningGroup)
    const dialog = await screen.findByRole('dialog', { name: 'Agent 后台运行中，3 个' })
    const rows = within(dialog).getAllByRole('listitem')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('调查架构 A')
    expect(rows[0]).toHaveTextContent('已运行 1:50')
    expect(within(rows[0]).getByRole('button', { name: '停止后台任务：调查架构 A' })).toBeInTheDocument()

    await user.click(within(rows[0]).getByRole('button', { name: '查看详情：调查架构 A' }))
    expect(within(dialog).getByRole('button', { name: '返回 Agent 列表（3 个）' })).toHaveTextContent('返回')
    expect(dialog).toHaveTextContent('调查架构 A')

    await user.click(within(dialog).getByRole('button', { name: '返回 Agent 列表（3 个）' }))
    expect(within(dialog).getByRole('button', { name: '查看详情：调查架构 A' })).toHaveFocus()
  })

  it('renders follow-up heading tasks in quiet grouped style in mono and keeps pills in Plate', () => {
    const tasks = [
      makeBackgroundTask({ id: 'a1', kind: 'command', label: 'npm test', startedAt: '2026-10-12T08:00:10Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:00Z' }),
      makeBackgroundTask({ id: 'a2', kind: 'command', label: 'npm lint', startedAt: '2026-10-12T08:00:20Z', status: 'completed', exitCode: 0, endedAt: '2026-10-12T08:01:05Z' }),
    ]
    const { unmount } = render(<BackgroundFollowUpLine tasks={tasks} runtimeName="Claude Code" at="2026-10-12T08:01:10Z" />)

    const monoNote = screen.getByRole('note')
    expect(monoNote).toHaveTextContent('后台任务结束后，Claude Code 接着说：')
    const monoGroup = within(monoNote).getByRole('button', { name: '命令 后台已完成，2 个' })
    expect(monoGroup.querySelector('[data-tool-text]')).toHaveTextContent('命令×2后台已完成')
    expect(monoGroup.className).not.toContain('rounded-full')
    unmount()

    setSkin('plate')
    render(<BackgroundFollowUpLine tasks={tasks} runtimeName="Claude Code" at="2026-10-12T08:01:10Z" />)
    const plateNote = screen.getByRole('note')
    const plateButtons = within(plateNote).getAllByRole('button')
    expect(plateButtons).toHaveLength(2)
    expect(plateButtons[0].className).toContain('rounded-full')
    expect(plateButtons[1].className).toContain('rounded-full')
  })
})
