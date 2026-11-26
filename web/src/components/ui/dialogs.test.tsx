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

describe('What dialogs look like', () => {
  it('uses clean elevated monochrome surfaces by default and preserves Plate index tabs and offset shadows', () => {
    const { unmount } = dialog('create')
    let box = screen.getByRole('dialog', { name: 'Title' })
    let tab = box.querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('New')
    expect(tab).toHaveClass('rounded-stamp', 'bg-surface-subtle', '[[data-skin=plate]_&]:bg-riso-yellow', '[[data-skin=plate]_&]:rounded-t-input', '[[data-skin=plate]_&]:border-b-0')
    expect(tab.querySelector('svg')).not.toBeNull()
    expect(box.querySelector('[data-dialog-paper]')).toHaveClass(
      'rounded-dialog',
      'border',
      'border-hairline-strong',
      'bg-surface-elevated',
      'shadow-dialog',
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:bg-paper-raised',
      '[[data-skin=plate]_&]:shadow-[8px_8px_0_var(--riso-blue)]',
    )
    unmount()

    const danger = dialog('danger')
    box = screen.getByRole('dialog', { name: 'Title' })
    tab = box.querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('Danger')
    expect(tab).toHaveClass('bg-tint-red', 'text-red-text')
    expect(box.querySelector('[data-dialog-paper]')).toHaveClass('[[data-skin=plate]_&]:shadow-[8px_8px_0_var(--red)]')
    danger.unmount()

    dialog()
    tab = screen.getByRole('dialog', { name: 'Title' }).querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('Edit')
    expect(tab).toHaveClass('bg-surface-subtle', '[[data-skin=plate]_&]:bg-tint-blue')
  })

  it('places the Close button inside a non-scrolling top bar outside the scrollable body so scrolled content never slides under it', () => {
    dialog('create')
    const box = screen.getByRole('dialog', { name: 'Title' })
    const topbar = box.querySelector('[data-dialog-topbar]')!
    const body = box.querySelector('[data-dialog-body]')!
    const closeBtn = within(box).getByRole('button', { name: 'Close' })

    expect(topbar).toHaveClass('shrink-0')
    expect(topbar).toContainElement(closeBtn)
    expect(body).toHaveClass('overflow-y-auto')
    expect(body).not.toContainElement(closeBtn)
    expect(body).toContainElement(within(box).getByRole('heading', { name: 'Title' }))

    expect(closeBtn).toHaveClass('rounded-ticket', 'text-ink-500', '[[data-skin=plate]_&]:rounded-full', '[[data-skin=plate]_&]:bg-riso-yellow', '[[data-skin=plate]_&]:border-ink')
    const footer = within(box).getByRole('button', { name: 'Create' }).parentElement!
    expect(footer).toHaveClass('border-t', 'border-hairline', '[[data-skin=plate]_&]:border-t-2', '[[data-skin=plate]_&]:border-dashed')
    expect(footer.className).toContain('[[data-skin=plate]_&_[data-variant=default]]:bg-riso-pink')
    expect(footer.className).toContain('[[data-skin=plate]_&_[data-variant=secondary]]:bg-paper-raised')
    expect(within(box).getByRole('button', { name: 'Cancel' })).toHaveAttribute('data-variant', 'secondary')
  })

  it('the confirmation dialog shares the kind label (“Confirm” by default) and non-scrolling Close topbar', () => {
    render(
      <AlertDialog open>
        <AlertDialogContent aria-describedby={undefined}>
          <AlertDialogTitle>Delete?</AlertDialogTitle>
        </AlertDialogContent>
      </AlertDialog>,
    )
    const box = screen.getByRole('alertdialog', { name: 'Delete?' })
    const topbar = box.querySelector('[data-dialog-topbar]')!
    const body = box.querySelector('[data-dialog-body]')!
    const closeBtn = within(box).getByRole('button', { name: 'Close' })

    expect(box.querySelector('[data-dialog-tab]')).toHaveTextContent('Confirm')
    expect(topbar).toHaveClass('shrink-0')
    expect(topbar).toContainElement(closeBtn)
    expect(body).toHaveClass('overflow-y-auto')
    expect(body).not.toContainElement(closeBtn)
    expect(closeBtn).toHaveClass('[[data-skin=plate]_&]:bg-riso-yellow')
  })
})

describe('What drawers look like', () => {
  it('uses clean elevated surfaces in mono skin and preserves the Plate riso-blue header, yellow Close stamp and yellow footer', () => {
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
    expect(drawer).toHaveClass(
      'border-l',
      'border-hairline-strong',
      'bg-surface-elevated',
      'shadow-dialog',
      '[[data-skin=plate]_&]:border-l-2',
      '[[data-skin=plate]_&]:shadow-[-8px_0_0_var(--riso-blue)]',
    )
    expect(within(drawer).getByRole('button', { name: 'Close' })).toHaveClass('[[data-skin=plate]_&]:bg-riso-yellow')
    const footer = within(drawer).getByRole('button', { name: 'Save changes' }).parentElement!
    expect(footer).toHaveAttribute('data-slot', 'sheet-footer')
    expect(footer).toHaveClass(
      'border-t',
      'border-hairline',
      'bg-surface-subtle',
      '[[data-skin=plate]_&]:bg-tint-yellow',
      '[[data-skin=plate]_&]:border-t-2',
      '[[data-skin=plate]_&]:border-ink',
    )
  })
})
