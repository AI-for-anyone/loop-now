import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { ConfirmDialog } from './ConfirmDialog'

function deferred() {
  let resolve!: () => void
  let reject!: (error: unknown) => void
  const promise = new Promise<void>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

const notIdle = new ApiError({ status: 409, code: 'agent_not_idle' })

describe('Confirmation dialog', () => {
  it('Esc does not close it while submitting; the failure reason shows in the dialog', async () => {
    const request = deferred()
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    render(<ConfirmDialog open onOpenChange={onOpenChange} title="Delete?" confirmLabel="Delete" onConfirm={() => request.promise} />)
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await user.keyboard('{Escape}')
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()

    request.reject(notIdle)
    expect(await within(dialog).findByText(/The Agent isn't idle/)).toBeInTheDocument()
    expect(onOpenChange).not.toHaveBeenCalled()
  })

  it('calls onOpenChange(false) on success', async () => {
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    render(<ConfirmDialog open onOpenChange={onOpenChange} title="Delete?" confirmLabel="Delete" onConfirm={async () => {}} />)
    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it('does not show the error left from last time when opened again', async () => {
    const user = userEvent.setup()
    const props = { onOpenChange: () => {}, title: 'Replace?', confirmLabel: 'Replace', onConfirm: () => Promise.reject(notIdle) }
    const { rerender } = render(<ConfirmDialog open {...props} />)
    await user.click(screen.getByRole('button', { name: 'Replace' }))
    expect(await screen.findByText(/The Agent isn't idle/)).toBeInTheDocument()
    // The parent closes the confirmation dialog directly (without going through onOpenChange), then opens it again.
    rerender(<ConfirmDialog open={false} {...props} />)
    rerender(<ConfirmDialog open {...props} />)
    expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
    expect(screen.queryByText(/The Agent isn't idle/)).toBeNull()
  })

  it('delete-type confirmation (destructive): top-edge label “Danger”, a tilted “Irreversible” stamp next to the title, a red confirm button; a plain confirmation says “Confirm” and has no stamp', () => {
    const { unmount } = render(<ConfirmDialog open onOpenChange={() => {}} title="Uninstall Grok CLI?" confirmLabel="Uninstall" destructive onConfirm={() => {}} />)
    let dialog = screen.getByRole('alertdialog', { name: 'Uninstall Grok CLI?' })
    expect(dialog.querySelector('[data-dialog-tab]')).toHaveTextContent('Danger')
    expect(within(dialog).getByText('Irreversible')).toHaveClass('riso-tilt', 'text-red-text')
    expect(within(dialog).getByRole('button', { name: 'Uninstall' })).toHaveAttribute('data-variant', 'destructive')
    unmount()
    render(<ConfirmDialog open onOpenChange={() => {}} title="Retry current node" confirmLabel="Retry" onConfirm={() => {}} />)
    dialog = screen.getByRole('alertdialog', { name: 'Retry current node' })
    expect(dialog.querySelector('[data-dialog-tab]')).toHaveTextContent('Confirm')
    expect(within(dialog).queryByText('Irreversible')).not.toBeInTheDocument()
  })

  it('a dangerous action that only unlinks something and can be added back (irreversible=false): still the “Danger” label and red button, but no “Irreversible” stamp', () => {
    render(<ConfirmDialog open onOpenChange={() => {}} title="Remove /work/docs from the list?" confirmLabel="Remove" destructive irreversible={false} onConfirm={() => {}} />)
    const dialog = screen.getByRole('alertdialog', { name: 'Remove /work/docs from the list?' })
    expect(dialog.querySelector('[data-dialog-tab]')).toHaveTextContent('Danger')
    expect(within(dialog).getByRole('button', { name: 'Remove' })).toHaveAttribute('data-variant', 'destructive')
    expect(within(dialog).queryByText('Irreversible')).not.toBeInTheDocument()
  })
})
