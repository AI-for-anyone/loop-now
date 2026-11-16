import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState, type ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { DirtyContext } from '@/features/prompts/draft-navigation'
import { CredentialEditor, type CredentialInput } from './CredentialEditor'

const wrapper = ({ children }: { children: ReactNode }) => <DirtyContext value={() => {}}>{children}</DirtyContext>

describe('Project credential local form lifecycle (not wired to HTTP yet)', () => {
  it('412 keeps the masked secret and the original baseline; only after explicit review does it submit with the new revision actually seen', async () => {
    const user = userEvent.setup(), close = vi.fn(), revisions: number[] = []
    const save = vi.fn(async (_input: CredentialInput, revision: number) => {
      revisions.push(revision)
      if (revisions.length === 1) throw new ApiError({ status: 412, code: 'precondition_failed' })
    })
    const props = { id: 'account-a', mode: 'replace-key' as const, revision: 1, summary: 'Account A · Version 1', readLatest: async () => ({ revision: 2, summary: 'Account A · Version 2' }), onSave: save, onClose: close }
    const { rerender } = render(<CredentialEditor {...props} />, { wrapper })
    await user.type(screen.getByLabelText('API Key'), 'local-secret')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Account A · Version 2')).toBeInTheDocument()
    expect(screen.getByLabelText('API Key')).toHaveAttribute('type', 'password')
    expect(screen.getByLabelText('API Key')).toHaveValue('local-secret')
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
    rerender(<CredentialEditor {...props} revision={3} summary="Account A · Version 3" />)
    expect(screen.queryByText('Account A · Version 3')).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Reviewed, adopt this version' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(revisions).toEqual([1, 2]); expect(close).toHaveBeenCalledOnce()
    expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ key: 'local-secret' }), 2)
  })

  it('a failed read of the latest keeps the draft and cannot be adopted; reading deleted again does not allow reviving it', async () => {
    const user = userEvent.setup()
    const latest = vi.fn().mockRejectedValueOnce(new ApiError({ status: 0, code: 'unreachable' })).mockResolvedValueOnce({ revision: 2, summary: 'Account A · Deleted', deleted: true })
    render(<CredentialEditor id="a" mode="replace-key" revision={1} summary="Account A" readLatest={latest} onSave={async () => { throw new ApiError({ status: 412, code: 'precondition_failed' }) }} onClose={() => {}} />, { wrapper })
    await user.type(screen.getByLabelText('API Key'), 'retained-secret')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(latest).toHaveBeenCalledOnce())
    expect(screen.getByRole('button', { name: 'Reviewed, adopt this version' })).toBeDisabled()
    expect(screen.getByLabelText('API Key')).toHaveValue('retained-secret')
    await user.click(screen.getByRole('button', { name: 'Read the latest again' }))
    expect(await screen.findByText(/The account was deleted, so it can't be replaced/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Reviewed, adopt this version' })).toBeDisabled()
  })

  it('cancel and success are unmounted and cleared by the owner; repeated submits send only once while the request is in progress', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const save = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
    function Host() {
      const [open, setOpen] = useState(true)
      return open ? <CredentialEditor id="a" mode="create-key" revision={0} summary="New account" readLatest={async () => ({ revision: 0, summary: '' })} onSave={save} onClose={() => setOpen(false)} /> : <button onClick={() => setOpen(true)}>Reopen</button>
    }
    render(<Host />, { wrapper })
    await user.type(screen.getByLabelText('Account name'), 'Account A'); await user.type(screen.getByLabelText('API Key'), 'cancel-secret')
    await user.click(screen.getByRole('button', { name: 'Cancel' })); await user.click(screen.getByRole('button', { name: 'Reopen' }))
    expect(screen.getByLabelText('API Key')).toHaveValue('')
    await user.type(screen.getByLabelText('Account name'), 'Account A'); await user.type(screen.getByLabelText('API Key'), 'success-secret')
    await user.dblClick(screen.getByRole('button', { name: 'Save key' }))
    expect(save).toHaveBeenCalledOnce()
    await act(async () => finish?.())
    await user.click(screen.getByRole('button', { name: 'Reopen' }))
    expect(screen.getByLabelText('API Key')).toHaveValue('')
  })

  it('after another failed read of the latest, the previous review cannot be adopted and the secret draft is still kept', async () => {
    const user = userEvent.setup()
    const readLatest = vi.fn().mockResolvedValueOnce({ revision: 2, summary: 'Version 2' }).mockRejectedValueOnce(new ApiError({ status: 0, code: 'unreachable' }))
    render(<CredentialEditor id="a" mode="replace-key" revision={1} summary="Version 1" readLatest={readLatest} onSave={async () => { throw new ApiError({ status: 412, code: 'precondition_failed' }) }} onClose={() => {}} />, { wrapper })
    await user.type(screen.getByLabelText('API Key'), 'retained-local-input')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await screen.findByText('Version 2')
    await user.click(screen.getByRole('button', { name: 'Read the latest again' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Reviewed, adopt this version' })).toBeDisabled())
    expect(screen.getByLabelText('API Key')).toHaveValue('retained-local-input')
    expect(screen.queryByText('Version 2')).toBeNull()
  })
})
