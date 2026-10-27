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

describe('确认框', () => {
  it('提交进行中按 Esc 不关闭，失败原因显示在确认框中', async () => {
    const request = deferred()
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    render(<ConfirmDialog open onOpenChange={onOpenChange} title="删除？" confirmLabel="删除" onConfirm={() => request.promise} />)
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: '删除' }))
    await user.keyboard('{Escape}')
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()

    request.reject(notIdle)
    expect(await within(dialog).findByText(/Agent 不空闲/)).toBeInTheDocument()
    expect(onOpenChange).not.toHaveBeenCalled()
  })

  it('成功后调用 onOpenChange(false)', async () => {
    const onOpenChange = vi.fn()
    const user = userEvent.setup()
    render(<ConfirmDialog open onOpenChange={onOpenChange} title="删除？" confirmLabel="删除" onConfirm={async () => {}} />)
    await user.click(screen.getByRole('button', { name: '删除' }))
    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it('再次打开时不显示上次留下的错误', async () => {
    const user = userEvent.setup()
    const props = { onOpenChange: () => {}, title: '替换？', confirmLabel: '替换', onConfirm: () => Promise.reject(notIdle) }
    const { rerender } = render(<ConfirmDialog open {...props} />)
    await user.click(screen.getByRole('button', { name: '替换' }))
    expect(await screen.findByText(/Agent 不空闲/)).toBeInTheDocument()
    // 父组件直接关闭确认框（不经过 onOpenChange），之后再打开。
    rerender(<ConfirmDialog open={false} {...props} />)
    rerender(<ConfirmDialog open {...props} />)
    expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
    expect(screen.queryByText(/Agent 不空闲/)).toBeNull()
  })
})
