import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { keys } from '@/api/queries'
import { Input } from '@/components/ui/input'
import { inDesktopShell } from '@/test/desktop'
import { serverShowsDialog } from '@/test/path-picks'
import { renderWithQuery } from '@/test/render'
import type { PathKind } from './desktop'
import { PathPicker } from './PathPicker'

// A path field as the forms have it: a labelled input inside the picker.
function PathField({ kind = 'directory', initial = '' }: { kind?: PathKind; initial?: string }) {
  const [value, setValue] = useState(initial)
  return (
    <div>
      <label htmlFor="path">Path</label>
      <PathPicker kind={kind} value={value} onPick={setValue}>
        <Input id="path" value={value} onChange={(event) => setValue(event.target.value)} />
      </PathPicker>
    </div>
  )
}

const setup = (props: { kind?: PathKind; initial?: string } = {}) => ({ user: userEvent.setup(), ...renderWithQuery(<PathField {...props} />) })

describe('Choosing a path in a browser, with the dialog Loop Now\'s server shows on this computer', () => {
  it('“Choose…” asks the server, starting at the value; while the dialog is open the button says so and where the dialog is; the chosen path fills the input and can still be edited', async () => {
    const dialog = serverShowsDialog('hold')
    const { user } = setup({ initial: '  /Users/me/code  ' })
    const choose = await screen.findByRole('button', { name: 'Choose…' })
    await user.click(choose)
    expect(choose).toHaveAccessibleName('Choosing…')
    expect(choose).toHaveAttribute('aria-disabled', 'true')
    expect(choose).toHaveFocus()
    expect(screen.getByRole('status')).toHaveTextContent('The dialog is open on this computer. If keys do nothing, click it once.')
    await waitFor(() => expect(dialog.requests).toEqual([{ kind: 'directory', defaultPath: '/Users/me/code' }]))
    // A second click while it is open asks for nothing more: one dialog at a time.
    await user.click(choose)
    expect(dialog.requests).toHaveLength(1)

    dialog.release({ path: '/Users/me/picked' })
    const input = screen.getByLabelText('Path')
    await waitFor(() => expect(input).toHaveValue('/Users/me/picked'))
    expect(choose).toHaveAccessibleName('Choose…')
    expect(choose).not.toHaveAttribute('aria-disabled')
    expect(screen.queryByRole('status')).toBeNull()
    await user.type(input, '/sub')
    expect(input).toHaveValue('/Users/me/picked/sub')
  })

  it('cancelling changes nothing; an empty field opens the dialog where it usually opens; a file is asked for as a file', async () => {
    const dialog = serverShowsDialog({ path: null })
    const { user } = setup({ kind: 'file' })
    const choose = await screen.findByRole('button', { name: 'Choose…' })
    const input = screen.getByLabelText('Path')
    await user.click(choose)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Choose…' })).not.toHaveAttribute('aria-disabled'))
    expect(dialog.requests).toEqual([{ kind: 'file' }])
    expect(input).toHaveValue('')
    await user.type(input, 'hermes')
    await user.click(screen.getByRole('button', { name: 'Choose…' }))
    await waitFor(() => expect(dialog.requests).toHaveLength(2))
    expect(dialog.requests[1]).toEqual({ kind: 'file', defaultPath: 'hermes' })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Choose…' })).not.toHaveAttribute('aria-disabled'))
    expect(input).toHaveValue('hermes')
    expect(screen.queryByText(/dialog/)).toBeNull()
  })

  it.each([
    ['another dialog is already open', { status: 409, code: 'path_picker_busy' as const }, 'A dialog to choose a path is already open. Choose or cancel there first.'],
    ['this computer cannot show one, with the server\'s reason', { status: 409, code: 'path_picker_unavailable' as const, message: 'no logged-in desktop session' }, "This computer can't show the dialog to choose a path: no logged-in desktop session"],
    ['anything else', { status: 500, code: 'internal_error' as const }, 'Internal server error'],
  ])('a refusal is said under the field (%s), and goes away with the next try', async (_case, refusal, said) => {
    const dialog = serverShowsDialog(refusal)
    const { user } = setup({ initial: '/Users/me/code' })
    await user.click(await screen.findByRole('button', { name: 'Choose…' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(said)
    expect(screen.getByLabelText('Path')).toHaveValue('/Users/me/code')

    dialog.answer = { path: '/Users/me/picked' }
    await user.click(screen.getByRole('button', { name: 'Choose…' }))
    await waitFor(() => expect(screen.getByLabelText('Path')).toHaveValue('/Users/me/picked'))
    expect(screen.queryByText(said)).toBeNull()
  })

  it('when the field goes away (its form is closed) the request is dropped, which closes the dialog', async () => {
    const dialog = serverShowsDialog('hold')
    const { user, unmount } = setup()
    await user.click(await screen.findByRole('button', { name: 'Choose…' }))
    await waitFor(() => expect(dialog.requests).toHaveLength(1))
    unmount()
    await waitFor(() => expect(dialog.aborted).toBe(1))
  })

  it('the desktop window keeps its own dialog, even when the server could show one', async () => {
    const pickPath = inDesktopShell({ answer: '/Users/me/from-shell' })
    const dialog = serverShowsDialog({ path: '/Users/me/from-server' })
    const { user } = setup()
    await user.click(screen.getByRole('button', { name: 'Choose…' }))
    await waitFor(() => expect(screen.getByLabelText('Path')).toHaveValue('/Users/me/from-shell'))
    expect(pickPath).toHaveBeenCalledWith({ kind: 'directory' })
    expect(dialog.requests).toEqual([])
  })

  it('when the server cannot show a dialog here, the path is typed: there is no “Choose…”', async () => {
    const { client } = setup()
    await waitFor(() => expect(client.getQueryData(keys.context)).toMatchObject({ pathPicker: false }))
    expect(screen.getByLabelText('Path')).toBeInTheDocument()
    expect(screen.queryByRole('button')).toBeNull()
  })
})
