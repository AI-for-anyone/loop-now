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

describe('按钮', () => {
  it('按 variant 与 size 渲染，焦点用印刷焦点环，禁用时去掉投影', () => {
    render(
      <>
        <Button variant="interrupt">打断</Button>
        <Button variant="ghost" size="sm">
          移除
        </Button>
        <Button disabled>保存</Button>
      </>,
    )
    const interrupt = screen.getByRole('button', { name: '打断' })
    expect(interrupt).toHaveAttribute('data-variant', 'interrupt')
    expect(interrupt.className).toContain('bg-riso-pink')
    expect(interrupt.className).toContain('focus-visible:shadow-focus')
    // 列表内的 ghost 操作高 36。
    expect(screen.getByRole('button', { name: '移除' }).className).toContain('h-9')
    const disabled = screen.getByRole('button', { name: '保存' })
    expect(disabled).toBeDisabled()
    expect(disabled.className).toContain('disabled:shadow-none')
  })
})

describe('徽标', () => {
  it('印章带状态色调，已打断与服务端重启中断不用红色', () => {
    render(
      <>
        <Badge variant="stamp" tone="interrupted">
          已打断
        </Badge>
        <Badge variant="stamp" tone="restart">
          服务端重启中断
        </Badge>
        <Badge variant="stamp" tone="danger">
          失败
        </Badge>
      </>,
    )
    expect(screen.getByText('已打断')).toHaveAttribute('data-tone', 'interrupted')
    expect(screen.getByText('已打断').className).toContain('text-pink-text')
    expect(screen.getByText('服务端重启中断').className).toContain('text-ink-700')
    expect(screen.getByText('失败').className).toContain('text-red-text')
    expect(screen.getByText('已打断').className).not.toContain('red')
  })
})

describe('输入框', () => {
  it('出错时按 aria-invalid 切换成浅红底', () => {
    render(<Input aria-label="名称" aria-invalid />)
    const input = screen.getByLabelText('名称')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input.className).toContain('aria-invalid:bg-error-field')
  })
})

describe('右侧抽屉', () => {
  function SheetDemo() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          打开
        </button>
        <Sheet open={open} onOpenChange={setOpen}>
          <SheetContent aria-describedby={undefined}>
            <SheetHeader>
              <SheetTitle>模板</SheetTitle>
            </SheetHeader>
            <SheetBody>内容</SheetBody>
          </SheetContent>
        </Sheet>
      </>
    )
  }

  it('Esc 关闭，焦点回到打开它的按钮', async () => {
    const user = userEvent.setup()
    render(<SheetDemo />)
    const trigger = screen.getByRole('button', { name: '打开' })
    await user.click(trigger)
    expect(await screen.findByRole('dialog', { name: '模板' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(trigger).toHaveFocus())
  })
})

describe('对话框与确认框', () => {
  // 控制台的对话框由状态控制、关闭时卸载，没有 Radix 的 Trigger。
  function DialogDemo() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          新建
        </button>
        {open && (
          <Dialog open onOpenChange={(next) => !next && setOpen(false)}>
            <DialogContent aria-describedby={undefined}>
              <DialogTitle>新建 Agent</DialogTitle>
              <input aria-label="名称" />
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
          删除
        </button>
        <AlertDialog open={open} onOpenChange={setOpen}>
          <AlertDialogContent>
            <AlertDialogTitle>删除 Agent？</AlertDialogTitle>
            <AlertDialogDescription>删除后不能恢复。</AlertDialogDescription>
            <AlertDialogCancel>取消</AlertDialogCancel>
          </AlertDialogContent>
        </AlertDialog>
      </>
    )
  }

  it('对话框按 Esc 关闭后，焦点回到打开它的按钮', async () => {
    const user = userEvent.setup()
    render(<DialogDemo />)
    const opener = screen.getByRole('button', { name: '新建' })
    await user.click(opener)
    expect(await screen.findByRole('dialog', { name: '新建 Agent' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(opener).toHaveFocus())
  })

  it('确认框点“取消”关闭后，焦点回到打开它的按钮', async () => {
    const user = userEvent.setup()
    render(<ConfirmDemo />)
    const opener = screen.getByRole('button', { name: '删除' })
    await user.click(opener)
    await user.click(await screen.findByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    await waitFor(() => expect(opener).toHaveFocus())
  })
})

describe('可过滤的下拉', () => {
  function ComboboxDemo() {
    const [value, setValue] = useState<string | null>(null)
    return (
      <>
        <span id="label">模型</span>
        <Combobox
          aria-labelledby="label"
          value={value}
          onChange={setValue}
          options={[
            { value: 'a', label: 'Alpha', hint: 'alpha-1' },
            { value: 'b', label: 'Beta', hint: 'beta-2' },
            { value: 'c', label: 'Gamma', hint: 'gamma-3' },
          ]}
          placeholder="选择模型"
          filterLabel="按名称或 ID 过滤"
          emptyText="没有匹配的模型"
        />
      </>
    )
  }

  it('过滤后用上下键加回车选择，Esc 关闭', async () => {
    const user = userEvent.setup()
    render(<ComboboxDemo />)
    const trigger = screen.getByRole('combobox', { name: '模型' })
    expect(trigger).toHaveTextContent('选择模型')
    await user.click(trigger)
    const filter = await screen.findByRole('searchbox', { name: '按名称或 ID 过滤' })
    await user.type(filter, 'a')
    expect(within(screen.getByRole('listbox')).getAllByRole('option')).toHaveLength(3)
    await user.clear(filter)
    await user.type(filter, 'ta')
    // 只有 Beta 的名称或 ID 中含有“ta”。
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

  it('没有匹配时显示说明', async () => {
    const user = userEvent.setup()
    render(<ComboboxDemo />)
    await user.click(screen.getByRole('combobox', { name: '模型' }))
    await user.type(await screen.findByRole('searchbox'), 'zzz')
    expect(screen.getByText('没有匹配的模型')).toBeInTheDocument()
  })
})

describe('卡片式单选', () => {
  it('不预选，方向键切换', async () => {
    const user = userEvent.setup()
    render(
      <RadioCards aria-label="方式">
        <RadioCard value="queue" title="排队" />
        <RadioCard value="interrupt" title="打断" />
      </RadioCards>,
    )
    const radios = screen.getAllByRole('radio')
    for (const radio of radios) expect(radio).not.toBeChecked()
    await user.click(radios[0])
    expect(radios[0]).toBeChecked()
    // Radix 在按住方向键期间把焦点移到下一项并选中它（焦点在定时器中移动），所以按下与松开分开。
    await user.keyboard('{ArrowDown>}')
    await waitFor(() => expect(radios[1]).toBeChecked())
    await user.keyboard('{/ArrowDown}')
    expect(radios[0]).not.toBeChecked()
  })
})

describe('表格', () => {
  it('外层容器可以单独横向滚动', () => {
    render(
      <Table>
        <TableBody>
          <TableRow>
            <TableCell>令牌</TableCell>
          </TableRow>
        </TableBody>
      </Table>,
    )
    expect(screen.getByRole('table').parentElement?.className).toContain('overflow-x-auto')
  })
})
