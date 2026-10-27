import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { projectAccount } from '@/test/project-credentials'
import { larkText as copy } from '@/i18n/lark'
import { ApiError } from '@/api/client'
import { LarkUserLogin, type LarkUserLoginProps } from './LarkUserLogin'
import { LarkAccountDetails } from './LarkAccountDetails'

const app = projectAccount({ purpose: 'cli', provider: null, cli: 'lark-cli', authMethod: null, name: '原应用' })
const props = (): LarkUserLoginProps => ({ projectId: app.projectId, available: true, sources: [app], onStart: vi.fn().mockResolvedValue(undefined), onRecover: vi.fn().mockResolvedValue(undefined) })
describe('Lark login presentation, injected actions only', () => {
  it('one application still requires explicit selection, pins its revision, never asks for secrets', async () => {
    const p = props(); render(<LarkUserLogin {...p} />)
    expect(screen.getByRole('combobox')).toHaveValue('')
    expect(screen.getByRole('button', { name: copy.start })).toBeDisabled()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    await userEvent.selectOptions(screen.getByRole('combobox'), app.id)
    await userEvent.click(screen.getByRole('button', { name: copy.start }))
    expect(p.onStart).toHaveBeenCalledExactlyOnceWith(app)
  })
  it('source replacement retains original selection and requires explicit review', async () => {
    const p = props(), view = render(<LarkUserLogin {...p} />)
    await userEvent.selectOptions(screen.getByRole('combobox'), app.id)
    view.rerender(<LarkUserLogin {...p} sources={[{ ...app, revision: 2 }]} />)
    expect(screen.getByText(copy.sourceChanged)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.start })).toBeDisabled()
    expect(p.onStart).not.toHaveBeenCalled()
  })
  it('zero compatible sources shows original setup path; no automatic registration or POST', () => {
    const p = props(); render(<LarkUserLogin {...p} sources={[]} applicationSetup={<a href="#application">原应用录入</a>} />)
    expect(screen.getByText(copy.noApplication)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '原应用录入' })).toHaveAttribute('href', '#application')
    expect(p.onStart).not.toHaveBeenCalled()
  })
  it.each(['unknown', 'not-found'] as const)('restores only original ID for %s, does not create another operation', async recovery => {
    const p = props(); render(<LarkUserLogin {...p} operationId="original-operation" recovery={recovery} />)
    await userEvent.click(screen.getByRole('button', { name: copy.recover }))
    expect(p.onRecover).toHaveBeenCalledExactlyOnceWith('original-operation')
    expect(p.onStart).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: copy.start })).toBeDisabled()
  })
  it('terminal observation absorbs late awaiting; new operation independent; no window preopen', async () => {
    const p = props(), open = vi.fn(), view = render(<LarkUserLogin {...p} phase="awaiting_authorization" operationId="one" authorizationReady userCode="synthetic code" onOpenAuthorization={open} />)
    expect(open).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: copy.open })); expect(open).toHaveBeenCalledOnce()
    view.rerender(<LarkUserLogin {...p} phase="cancelled" operationId="one" userCode="synthetic code" />)
    expect(screen.queryByText('synthetic code')).not.toBeInTheDocument()
    view.rerender(<LarkUserLogin {...p} phase="awaiting_authorization" operationId="one" authorizationReady userCode="synthetic code" onOpenAuthorization={open} />)
    expect(screen.queryByRole('button', { name: copy.open })).not.toBeInTheDocument()
    view.rerender(<LarkUserLogin {...p} phase="awaiting_authorization" operationId="two" authorizationReady onOpenAuthorization={open} />)
    expect(screen.getByRole('button', { name: copy.open })).toBeEnabled()
    expect(screen.queryByText(copy.code)).not.toBeInTheDocument()
  })
  it('pending async start prevents reentrant calls and cross-project source reuse', async () => {
    let finish!: () => void
    const p = props(); p.onStart = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
    const view = render(<LarkUserLogin {...p} />)
    await userEvent.selectOptions(screen.getByRole('combobox'), app.id)
    await userEvent.dblClick(screen.getByRole('button', { name: copy.start }))
    expect(p.onStart).toHaveBeenCalledOnce()
    view.rerender(<LarkUserLogin {...p} projectId="other-project" />)
    expect(screen.getByRole('combobox')).toHaveValue('')
    expect(screen.getByRole('button', { name: copy.start })).toBeDisabled()
    await act(async () => finish())
  })
  it('old backend disables starts, but saved account remains separate from operation failure', () => {
    const p = props(); render(<LarkUserLogin {...p} available={false} phase="failed" operationId="one" savedAccount={<LarkAccountDetails account={app} />} />)
    expect(screen.getByText(copy.unsupported)).toBeInTheDocument()
    expect(screen.getByText(copy.failed)).toBeInTheDocument()
    expect(screen.getByText(copy.bot)).toBeInTheDocument()
    expect(p.onStart).not.toHaveBeenCalled()
  })
  it('remark is never treated as identity; unknown and empty granted scopes stay distinct', () => {
    const account = { ...app, authMethod: 'oauth' as const, name: '人的备注' }
    const view = render(<LarkAccountDetails account={account} />)
    expect(screen.getByText(copy.unknownIdentity)).toBeInTheDocument()
    expect(screen.getByText(copy.unknownGrants)).toBeInTheDocument()
    view.rerender(<LarkAccountDetails account={account} identity="核实用户" grantedScopes={[]} />)
    expect(screen.getByText(copy.emptyGrants)).toBeInTheDocument()
    expect(screen.getByText('核实用户')).toBeInTheDocument()
  })
  it('relogin uses saved target without requiring source bot, and does not imply cross-user policy', async () => {
    const p = props(), target = { ...app, authMethod: 'oauth' as const, status: 'requires_login' as const }, relogin = vi.fn().mockResolvedValue(undefined)
    render(<LarkUserLogin {...p} sources={[]} targetAccount={target} onRelogin={relogin} />)
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: copy.relogin }))
    expect(relogin).toHaveBeenCalledExactlyOnceWith(target)
    expect(p.onStart).not.toHaveBeenCalled()
  })
  it('locally expired window hides entry/code without inventing a terminal result or creating another operation', () => {
    const p = props(); render(<LarkUserLogin {...p} operationId="one" phase="awaiting_authorization" expiresAt="2000-01-01T00:00:00Z" authorizationReady userCode="no-longer-visible" />)
    expect(screen.queryByText('no-longer-visible')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: copy.open })).not.toBeInTheDocument()
    expect(screen.getByText(copy.windowExpired)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: copy.start })).toBeDisabled()
  })
  it('original GET404 is unconfirmed rather than a deleted account and cannot dispatch start', async () => {
    const p = props(); p.onRecover = vi.fn().mockRejectedValue(new ApiError({ status: 404, code: 'not_found' }))
    render(<LarkUserLogin {...p} operationId="original" recovery="unknown" />)
    await userEvent.click(screen.getByRole('button', { name: copy.recover }))
    expect(screen.getByText(copy.notFound)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(p.onStart).not.toHaveBeenCalled()
  })
})
