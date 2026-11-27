import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ToolList } from '@/features/chat/RunStatus'

const visualText = (item: HTMLElement) => item.querySelector('[data-tool-visual]')?.textContent ?? item.textContent
const srText = (item: HTMLElement) => item.querySelector('.sr-only')?.textContent

describe('ToolList (Chinese UI)', () => {
  it('groups succeeded calls without showing “成功”, shows “失败” / “进行中” / “没有结果” visually, and gives full Chinese screen-reader labels', () => {
    render(
      <ToolList
        tools={[
          { toolCallId: 'c1', toolName: 'execute', state: 'succeeded', at: '2026-10-12T00:00:00Z' },
          { toolCallId: 'c2', toolName: 'execute', state: 'succeeded', at: '2026-10-12T00:00:01Z' },
          { toolCallId: 'c3', toolName: 'execute', state: 'failed', at: '2026-10-12T00:00:02Z' },
          { toolCallId: 'c4', toolName: 'execute', state: 'failed', at: '2026-10-12T00:00:03Z' },
          { toolCallId: 'c5', toolName: 'read', state: 'started', at: '2026-10-12T00:00:04Z' },
          { toolCallId: 'c6', toolName: 'edit', state: 'no_result', at: '2026-10-12T00:00:05Z' },
        ]}
      />,
    )
    const items = screen.getAllByRole('listitem')
    expect(items.map(visualText)).toEqual(['execute×2', 'execute失败×2', '…read进行中', 'edit没有结果'])
    expect(items.map(srText)).toEqual(['execute 成功 2 次', 'execute 失败 2 次', 'read 进行中', 'edit 没有结果'])
  })
})
