import { fieldClass, selectClass } from '@/lib/field'
import { Textarea } from './textarea'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogTitle } from './alert-dialog'
import { Badge } from './badge'
import { Button } from './button'
import { Combobox } from './combobox'
import { Dialog, DialogContent, DialogTitle } from './dialog'
import { Input } from './input'
import { RadioCard, RadioCards } from './radio-cards'
import { Sheet, SheetBody, SheetContent, SheetHeader, SheetTitle } from './sheet'
import { Table, TableBody, TableCell, TableRow } from './table'

describe('Button', () => {
  it('renders by variant and size, uses the print focus ring, and drops the shadow when disabled', () => {
    render(
      <>
        <Button variant="interrupt">Interrupt</Button>
        <Button variant="ghost" size="sm">
          Remove
        </Button>
        <Button disabled>Save</Button>
      </>,
    )
    const interrupt = screen.getByRole('button', { name: 'Interrupt' })
    expect(interrupt).toHaveAttribute('data-variant', 'interrupt')
    expect(interrupt.className).toContain('bg-riso-pink')
    expect(interrupt.className).toContain('focus-visible:shadow-focus')
    // Ghost actions inside lists are 36px tall.
    expect(screen.getByRole('button', { name: 'Remove' }).className).toContain('h-9')
    const disabled = screen.getByRole('button', { name: 'Save' })
    expect(disabled).toBeDisabled()
    expect(disabled.className).toContain('disabled:shadow-none')
  })
})

describe('Badge', () => {
  it('stamps carry the status tone; interrupted and cut off by a server restart are not red', () => {
    render(
      <>
        <Badge variant="stamp" tone="interrupted">
          Interrupted
        </Badge>
        <Badge variant="stamp" tone="restart">
          Cut off by a server restart
        </Badge>
        <Badge variant="stamp" tone="danger">
          Failed
        </Badge>
      </>,
    )
    expect(screen.getByText('Interrupted')).toHaveAttribute('data-tone', 'interrupted')
    expect(screen.getByText('Interrupted').className).toContain('text-pink-text')
    expect(screen.getByText('Cut off by a server restart').className).toContain('text-ink-700')
    expect(screen.getByText('Failed').className).toContain('text-red-text')
    expect(screen.getByText('Interrupted').className).not.toContain('red')
  })
})

describe('Input', () => {
  it('switches to a light red background by aria-invalid on error', () => {
    render(<Input aria-label="Name" aria-invalid />)
    const input = screen.getByLabelText('Name')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input.className).toContain('aria-invalid:bg-error-field')
  })
})

describe('Right-side drawer', () => {
  function SheetDemo() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          Open
        </button>
        <Sheet open={open} onOpenChange={setOpen}>
          <SheetContent aria-describedby={undefined}>
            <SheetHeader>
              <SheetTitle>Template</SheetTitle>
            </SheetHeader>
            <SheetBody>Content</SheetBody>
          </SheetContent>
        </Sheet>
      </>
    )
  }

  it('Esc closes it and focus returns to the button that opened it', async () => {
    const user = userEvent.setup()
    render(<SheetDemo />)
    const trigger = screen.getByRole('button', { name: 'Open' })
    await user.click(trigger)
    expect(await screen.findByRole('dialog', { name: 'Template' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(trigger).toHaveFocus())
  })
})

describe('Dialog and confirmation dialog', () => {
  // Console dialogs are controlled by state and unmounted when closed; they have no Radix Trigger.
  function DialogDemo() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          New
        </button>
        {open && (
          <Dialog open onOpenChange={(next) => !next && setOpen(false)}>
            <DialogContent aria-describedby={undefined}>
              <DialogTitle>New Agent</DialogTitle>
              <input aria-label="Name" />
            </DialogContent>
          </Dialog>
        )}
      </>
    )
  }

  function ConfirmDemo() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          Delete
        </button>
        <AlertDialog open={open} onOpenChange={setOpen}>
          <AlertDialogContent>
            <AlertDialogTitle>Delete Agent?</AlertDialogTitle>
            <AlertDialogDescription>This can't be undone.</AlertDialogDescription>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
          </AlertDialogContent>
        </AlertDialog>
      </>
    )
  }

  it('after a dialog closes with Esc, focus returns to the button that opened it', async () => {
    const user = userEvent.setup()
    render(<DialogDemo />)
    const opener = screen.getByRole('button', { name: 'New' })
    await user.click(opener)
    expect(await screen.findByRole('dialog', { name: 'New Agent' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(opener).toHaveFocus())
  })

  it('after a confirmation dialog closes with “Cancel”, focus returns to the button that opened it', async () => {
    const user = userEvent.setup()
    render(<ConfirmDemo />)
    const opener = screen.getByRole('button', { name: 'Delete' })
    await user.click(opener)
    await user.click(await screen.findByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    await waitFor(() => expect(opener).toHaveFocus())
  })
})

describe('Filterable dropdown', () => {
  function ComboboxDemo() {
    const [value, setValue] = useState<string | null>(null)
    return (
      <>
        <span id="label">Model</span>
        <Combobox
          aria-labelledby="label"
          value={value}
          onChange={setValue}
          options={[
            { value: 'a', label: 'Alpha', hint: 'alpha-1' },
            { value: 'b', label: 'Beta', hint: 'beta-2' },
            { value: 'c', label: 'Gamma', hint: 'gamma-3' },
          ]}
          placeholder="Choose a model"
          filterLabel="Filter by name or ID"
          emptyText="No matching models"
        />
      </>
    )
  }

  it('after filtering, the up and down keys plus Enter choose, Esc closes', async () => {
    const user = userEvent.setup()
    render(<ComboboxDemo />)
    const trigger = screen.getByRole('combobox', { name: 'Model' })
    expect(trigger).toHaveTextContent('Choose a model')
    await user.click(trigger)
    const filter = await screen.findByRole('searchbox', { name: 'Filter by name or ID' })
    await user.type(filter, 'a')
    expect(within(screen.getByRole('listbox')).getAllByRole('option')).toHaveLength(3)
    await user.clear(filter)
    await user.type(filter, 'ta')
    // Only Beta has "ta" in its name or ID.
    expect(within(screen.getByRole('listbox')).getAllByRole('option').map((o) => o.getAttribute('aria-label'))).toEqual(['Beta'])
    await user.keyboard('{Enter}')
    await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull())
    expect(trigger).toHaveTextContent('Beta')
    await user.click(trigger)
    await screen.findByRole('listbox')
    await user.keyboard('{ArrowDown}{Enter}')
    expect(trigger).toHaveTextContent('Gamma')
    await user.click(trigger)
    await screen.findByRole('listbox')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull())
    expect(trigger).toHaveTextContent('Gamma')
  })

  it('shows a note when nothing matches', async () => {
    const user = userEvent.setup()
    render(<ComboboxDemo />)
    await user.click(screen.getByRole('combobox', { name: 'Model' }))
    await user.type(await screen.findByRole('searchbox'), 'zzz')
    expect(screen.getByText('No matching models')).toBeInTheDocument()
  })
})

describe('Radio cards', () => {
  it('nothing is preselected; the arrow keys switch', async () => {
    const user = userEvent.setup()
    render(
      <RadioCards aria-label="Mode">
        <RadioCard value="queue" title="Queue" />
        <RadioCard value="interrupt" title="Interrupt" />
      </RadioCards>,
    )
    const radios = screen.getAllByRole('radio')
    for (const radio of radios) expect(radio).not.toBeChecked()
    await user.click(radios[0])
    expect(radios[0]).toBeChecked()
    // While the arrow key is held down, Radix moves focus to the next item and selects
    // it (focus moves in a timer), so key down and key up are sent separately.
    await user.keyboard('{ArrowDown>}')
    await waitFor(() => expect(radios[1]).toBeChecked())
    await user.keyboard('{/ArrowDown}')
    expect(radios[0]).not.toBeChecked()
  })
})

describe('Table', () => {
  it('the outer container can scroll sideways on its own', () => {
    render(
      <Table>
        <TableBody>
          <TableRow>
            <TableCell>Token</TableCell>
          </TableRow>
        </TableBody>
      </Table>,
    )
    expect(screen.getByRole('table').parentElement?.className).toContain('overflow-x-auto')
  })
})

// Redesign: inputs, textareas and selects all have a white background, a 2px navy
// border and 12px rounded corners (as do all form controls in the design mockups).
it('form controls have a 2px navy border and 12px rounded corners', () => {
  render(<><input aria-label="x" className={fieldClass} /><Input aria-label="Name" /><Textarea aria-label="About" /></>)
  for (const name of ['Name', 'About']) expect(screen.getByLabelText(name)).toHaveClass('rounded-input', 'bg-white', 'shadow-[inset_0_0_0_2px_var(--ink)]')
  expect(selectClass).toContain('shadow-[inset_0_0_0_2px_var(--ink)]')
})
