import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { LoginFeedback } from './LoginFeedback'

describe('Current login status card', () => {
  it('when blocked, the keyboard opens the current authorization and the cancel button works; cancelling again is disabled while a cancel is in progress', async () => {
    const user = userEvent.setup(), open = vi.fn(), cancel = vi.fn()
    const result = render(<LoginFeedback view="waiting" blocked onOpen={open} onCancel={cancel} />)
    expect(screen.getByRole('status')).toHaveTextContent("The authorization page didn't open")
    await user.tab()
    expect(screen.getByRole('button', { name: 'Open authorization page' })).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(open).toHaveBeenCalledOnce()
    await user.tab()
    await user.keyboard(' ')
    expect(cancel).toHaveBeenCalledOnce()
    result.rerender(<LoginFeedback view="waiting" cancelling onCancel={cancel} />)
    expect(screen.getByRole('button', { name: 'Cancelling…' })).toBeDisabled()
  })

  it('a known authorization URL is a native noopener link, removed in final states and not persisted', () => {
    const href = 'https://auth.openai.com/oauth/authorize?state=fixture&code_challenge=fixture'
    const view = render(<LoginFeedback view="waiting" authorizationHref={href} />)
    const link = screen.getByRole('link', { name: 'Open authorization page' })
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(link).toHaveAttribute('href', href)
    view.rerender(<LoginFeedback view="expired" />)
    expect(screen.queryByRole('link', { name: 'Open authorization page' })).toBeNull()
    expect(document.body.innerHTML).not.toContain('code_challenge=fixture')
  })

  it.each(['failed', 'cancelled', 'expired'] as const)('%s keeps the existing-account note and allows starting again', async (view) => {
    const retry = vi.fn(), user = userEvent.setup()
    render(<LoginFeedback view={view} hasAccount onRetry={retry} />)
    expect(screen.getByText(/The existing account is unchanged/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Start login again' }))
    expect(retry).toHaveBeenCalledOnce()
    expect(screen.queryByRole('button', { name: 'Open authorization page' })).toBeNull()
  })

  it('a first login with no saved account does not claim the existing account is kept', () => {
    render(<LoginFeedback view="failed" />)
    expect(screen.queryByText(/The existing account is unchanged/)).toBeNull()
    expect(screen.getByRole('alert')).toHaveTextContent("This login didn't complete")
  })
})
