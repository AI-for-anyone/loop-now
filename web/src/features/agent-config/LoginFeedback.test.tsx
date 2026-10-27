import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { LoginFeedback } from './LoginFeedback'

describe('本次登录状态卡', () => {
  it('被拦截时键盘打开当前授权、取消按钮可用，取消处理中禁止再次取消', async () => {
    const user = userEvent.setup(), open = vi.fn(), cancel = vi.fn()
    const result = render(<LoginFeedback view="waiting" blocked onOpen={open} onCancel={cancel} />)
    expect(screen.getByRole('status')).toHaveTextContent('授权页未打开')
    await user.tab()
    expect(screen.getByRole('button', { name: '打开授权页' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(open).toHaveBeenCalledOnce()
    await user.tab()
    await user.keyboard(' ')
    expect(cancel).toHaveBeenCalledOnce()
    result.rerender(<LoginFeedback view="waiting" cancelling onCancel={cancel} />)
    expect(screen.getByRole('button', { name: '正在取消…' })).toBeDisabled()
  })

  it('已知授权入口是noopener原生链接，终态移除且不持久化', () => {
    const href = 'https://auth.openai.com/oauth/authorize?state=fixture&code_challenge=fixture'
    const view = render(<LoginFeedback view="waiting" authorizationHref={href} />)
    const link = screen.getByRole('link', { name: '打开授权页' })
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(link).toHaveAttribute('href', href)
    view.rerender(<LoginFeedback view="expired" />)
    expect(screen.queryByRole('link', { name: '打开授权页' })).toBeNull()
    expect(document.body.innerHTML).not.toContain('code_challenge=fixture')
  })

  it.each(['failed', 'cancelled', 'expired'] as const)('%s 保留旧账号文案并允许重新发起', async (view) => {
    const retry = vi.fn(), user = userEvent.setup()
    render(<LoginFeedback view={view} hasAccount onRetry={retry} />)
    expect(screen.getByText(/原有账号保持不变/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新发起登录' }))
    expect(retry).toHaveBeenCalledOnce()
    expect(screen.queryByRole('button', { name: '打开授权页' })).toBeNull()
  })

  it('首次登录没有已保存账号时，不声称保留原账号', () => {
    render(<LoginFeedback view="failed" />)
    expect(screen.queryByText(/原有账号保持不变/)).toBeNull()
    expect(screen.getByRole('alert')).toHaveTextContent('这次登录没有完成')
  })
})
