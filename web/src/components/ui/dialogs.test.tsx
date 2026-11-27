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
  it('uses clean elevated surfaces driven by theme tokens and keeps dialog kind tabs', () => {
    const { unmount } = dialog('create')
    let box = screen.getByRole('dialog', { name: 'Title' })
    let tab = box.querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('New')
    expect(tab).toHaveClass('rounded-stamp', 'bg-surface-subtle')
    expect(tab.querySelector('svg')).not.toBeNull()
    expect(box.querySelector('[data-dialog-paper]')).toHaveClass(
      'rounded-dialog',
      'border',
      'border-hairline-strong',
      'bg-surface-elevated',
      'shadow-dialog',
    )
    unmount()

    const danger = dialog('danger')
    box = screen.getByRole('dialog', { name: 'Title' })
    tab = box.querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('Danger')
    expect(tab).toHaveClass('bg-tint-red', 'text-red-text')
    danger.unmount()

    dialog()
    tab = screen.getByRole('dialog', { name: 'Title' }).querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('Edit')
    expect(tab).toHaveClass('bg-surface-subtle')
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

    expect(closeBtn).toHaveClass('rounded-ticket', 'text-ink-500')
    const footer = within(box).getByRole('button', { name: 'Create' }).parentElement!
    expect(footer).toHaveClass('border-t', 'border-hairline')
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
    expect(closeBtn).toHaveClass('rounded-ticket', 'text-ink-500')
  })
})

describe('What drawers look like', () => {
  it('uses clean elevated surfaces and semantic border tokens', () => {
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
    )
    expect(within(drawer).getByRole('button', { name: 'Close' })).toHaveClass('rounded-ticket')
    const footer = within(drawer).getByRole('button', { name: 'Save changes' }).parentElement!
    expect(footer).toHaveAttribute('data-slot', 'sheet-footer')
    expect(footer).toHaveClass(
      'border-t',
      'border-hairline',
      'bg-surface-subtle',
    )
  })
})
