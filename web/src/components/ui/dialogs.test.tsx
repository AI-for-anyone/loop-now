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
        <DialogTitle>标题</DialogTitle>
        <DialogFooter>
          <Button variant="secondary">取消</Button>
          <Button>创建</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>,
  )

// 改版第 5 步：全站弹窗统一成一张小纸，顶边贴一张写明类别的索引标签。
describe('弹窗的样子', () => {
  it('顶边的索引标签写明类别：新建是黄色带加号，危险操作浅红底红字、纸的投影是红色，其余浅蓝底', () => {
    const { unmount } = dialog('create')
    let box = screen.getByRole('dialog', { name: '标题' })
    let tab = box.querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('新建')
    expect(tab).toHaveClass('bg-riso-yellow', 'rounded-t-input', 'border-b-0')
    expect(tab.querySelector('svg')).not.toBeNull()
    expect(box.querySelector('[data-dialog-paper]')).toHaveClass('border-2', 'border-ink', 'bg-paper-raised', 'shadow-[8px_8px_0_var(--riso-blue)]')
    unmount()

    const danger = dialog('danger')
    box = screen.getByRole('dialog', { name: '标题' })
    tab = box.querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('危险操作')
    expect(tab).toHaveClass('bg-tint-red', 'text-red-text')
    expect(box.querySelector('[data-dialog-paper]')).toHaveClass('shadow-[8px_8px_0_var(--red)]')
    danger.unmount()

    dialog()
    tab = screen.getByRole('dialog', { name: '标题' }).querySelector('[data-dialog-tab]')!
    expect(tab).toHaveTextContent('编辑')
    expect(tab).toHaveClass('bg-tint-blue')
  })

  it('右上角是黄色圆章的“关闭”；按钮区上方一条虚线，主按钮荧光粉、次要按钮浅纸底，都是藏青描边加投影', () => {
    dialog('create')
    const box = screen.getByRole('dialog', { name: '标题' })
    expect(within(box).getByRole('button', { name: '关闭' })).toHaveClass('rounded-full', 'bg-riso-yellow', 'border-ink')
    const footer = within(box).getByRole('button', { name: '创建' }).parentElement!
    expect(footer).toHaveClass('border-t-2', 'border-dashed')
    expect(footer.className).toContain('[&_[data-variant=default]]:bg-riso-pink')
    expect(footer.className).toContain('[&_[data-variant=secondary]]:bg-paper-raised')
    expect(within(box).getByRole('button', { name: '取消' })).toHaveAttribute('data-variant', 'secondary')
  })

  it('确认框也有类别标签（默认“确认”）和黄色圆章关闭', () => {
    render(
      <AlertDialog open>
        <AlertDialogContent aria-describedby={undefined}>
          <AlertDialogTitle>删除？</AlertDialogTitle>
        </AlertDialogContent>
      </AlertDialog>,
    )
    const box = screen.getByRole('alertdialog', { name: '删除？' })
    expect(box.querySelector('[data-dialog-tab]')).toHaveTextContent('确认')
    expect(within(box).getByRole('button', { name: '关闭' })).toHaveClass('bg-riso-yellow')
  })
})

describe('抽屉的样子', () => {
  it('头部是孔版蓝网点底、黄色圆章关闭；底部是浅黄色按钮栏，上缘一道藏青线', () => {
    render(
      <Sheet open>
        <SheetContent aria-describedby={undefined}>
          <SheetHeader>
            <SheetTitle>周报整理</SheetTitle>
          </SheetHeader>
          <SheetFooter>
            <Button variant="destructive-link">删除模板</Button>
            <Button className="ml-auto">保存修改</Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>,
    )
    const drawer = screen.getByRole('dialog', { name: '周报整理' })
    expect(drawer).toHaveClass('border-l-2', 'shadow-[-8px_0_0_var(--riso-blue)]')
    expect(within(drawer).getByRole('button', { name: '关闭' })).toHaveClass('bg-riso-yellow')
    const footer = within(drawer).getByRole('button', { name: '保存修改' }).parentElement!
    expect(footer).toHaveAttribute('data-slot', 'sheet-footer')
    expect(footer).toHaveClass('bg-tint-yellow', 'border-t-2', 'border-ink')
  })
})
