import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { UserChatBubble } from './ChatBubble'

// 用户气泡是 Agent 对话与任务节点对话共用的组件：任务节点对话不受对话页放宽的影响。
describe('用户气泡', () => {
  it('默认最宽是所在列的 80%（任务节点对话用这个）', () => {
    render(<UserChatBubble text="帮我看一下" time="09:14" />)
    expect(screen.getByText('帮我看一下')).toHaveClass('max-w-[80%]')
  })

  it('Agent 对话页传 wide：宽屏时最宽是对话列的 88%，不超过 1100px；窄屏照旧 80%', () => {
    render(<UserChatBubble wide text="帮我看一下" time="09:14" />)
    expect(screen.getByText('帮我看一下')).toHaveClass('nav:max-w-[min(88%,1100px)]', 'max-w-[80%]')
  })
})
