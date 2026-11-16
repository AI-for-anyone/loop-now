import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { AlertDialog, AlertDialogContent, AlertDialogTitle } from './alert-dialog'
import { Button } from './button'
import { Dialog, DialogContent, DialogFooter, DialogTitle } from './dialog'
import type { DialogKind } from './dialog-style'
import { Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle } from './sheet'

const dialog = (kind?: DialogKind) =>
  render(
    <Dialog open>
      <DialogContent kind={kind} aria-describedby={undefined}>
        <DialogTitle>Title</DialogTitle>
        <DialogFooter>
          <Button variant="secondary">Cancel</Button>
          <Button>Create</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>,
  )

// Redesign step 5: every dialog across the app becomes a small sheet, with an index tab on its top edge that names the dialog's kind.
describe('What dialogs look like', () => {
  it('the index tab on the top edge names the kind: create is yellow with a plus sign, danger is red text on light red with a red sheet shadow, the rest light blue', () => {
    const { unmount } = dialog('create')
    let box = screen.getByRole('dialog', { name: 'Title' })
    let tab = box.querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('New')
    expect(tab).toHaveClass('bg-riso-yellow', 'rounded-t-input', 'border-b-0')
    expect(tab.querySelector('svg')).not.toBeNull()
    expect(box.querySelector('[data-dialog-paper]')).toHaveClass('border-2', 'border-ink', 'bg-paper-raised', 'shadow-[8px_8px_0_var(--riso-blue)]')
    unmount()

    const danger = dialog('danger')
    box = screen.getByRole('dialog', { name: 'Title' })
    tab = box.querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('Danger')
    expect(tab).toHaveClass('bg-tint-red', 'text-red-text')
    expect(box.querySelector('[data-dialog-paper]')).toHaveClass('shadow-[8px_8px_0_var(--red)]')
    danger.unmount()

    dialog()
    tab = screen.getByRole('dialog', { name: 'Title' }).querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('Edit')
    expect(tab).toHaveClass('bg-tint-blue')
  })

  it('“Close” is a yellow round stamp at the top right; a dashed line above the button row, the primary button fluorescent pink and the secondary button light paper, both with a navy border and shadow', () => {
    dialog('create')
    const box = screen.getByRole('dialog', { name: 'Title' })
    expect(within(box).getByRole('button', { name: 'Close' })).toHaveClass('rounded-full', 'bg-riso-yellow', 'border-ink')
    const footer = within(box).getByRole('button', { name: 'Create' }).parentElement!
    expect(footer).toHaveClass('border-t-2', 'border-dashed')
    expect(footer.className).toContain('[&_[data-variant=default]]:bg-riso-pink')
    expect(footer.className).toContain('[&_[data-variant=secondary]]:bg-paper-raised')
    expect(within(box).getByRole('button', { name: 'Cancel' })).toHaveAttribute('data-variant', 'secondary')
  })

  it('the confirmation dialog also has a kind label (“Confirm” by default) and the yellow round Close stamp', () => {
    render(
      <AlertDialog open>
        <AlertDialogContent aria-describedby={undefined}>
          <AlertDialogTitle>Delete?</AlertDialogTitle>
        </AlertDialogContent>
      </AlertDialog>,
    )
    const box = screen.getByRole('alertdialog', { name: 'Delete?' })
    expect(box.querySelector('[data-dialog-tab]')).toHaveTextContent('Confirm')
    expect(within(box).getByRole('button', { name: 'Close' })).toHaveClass('bg-riso-yellow')
  })
})

describe('What drawers look like', () => {
  it('the header has a riso-blue halftone background and the yellow round Close stamp; the footer is a light yellow button bar with a navy line on its top edge', () => {
    render(
      <Sheet open>
        <SheetContent aria-describedby={undefined}>
          <SheetHeader>
            <SheetTitle>Weekly report</SheetTitle>
          </SheetHeader>
          <SheetFooter>
            <Button variant="destructive-link">Delete template</Button>
            <Button className="ml-auto">Save changes</Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>,
    )
    const drawer = screen.getByRole('dialog', { name: 'Weekly report' })
    expect(drawer).toHaveClass('border-l-2', 'shadow-[-8px_0_0_var(--riso-blue)]')
    expect(within(drawer).getByRole('button', { name: 'Close' })).toHaveClass('bg-riso-yellow')
    const footer = within(drawer).getByRole('button', { name: 'Save changes' }).parentElement!
    expect(footer).toHaveAttribute('data-slot', 'sheet-footer')
    expect(footer).toHaveClass('bg-tint-yellow', 'border-t-2', 'border-ink')
  })
})
