import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState, type ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { DirtyContext } from '@/features/prompts/draft-navigation'
import { CredentialEditor, type CredentialInput } from './CredentialEditor'

const wrapper = ({ children }: { children: ReactNode }) => <DirtyContext value={() => {}}>{children}</DirtyContext>

describe('项目凭证本地表单生命周期（尚未接HTTP）', () => {
  it('412保留遮蔽秘密与原基线，明确核对后才用实际看过的新revision提交', async () => {
    const user = userEvent.setup(), close = vi.fn(), revisions: number[] = []
    const save = vi.fn(async (_input: CredentialInput, revision: number) => {
      revisions.push(revision)
      if (revisions.length === 1) throw new ApiError({ status: 412, code: 'precondition_failed' })
    })
    const props = { id: 'account-a', mode: 'replace-key' as const, revision: 1, summary: '账号A · 版本1', readLatest: async () => ({ revision: 2, summary: '账号A · 版本2' }), onSave: save, onClose: close }
    const { rerender } = render(<CredentialEditor {...props} />, { wrapper })
    await user.type(screen.getByLabelText('API Key'), 'local-secret')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(await screen.findByText('账号A · 版本2')).toBeInTheDocument()
    expect(screen.getByLabelText('API Key')).toHaveAttribute('type', 'password')
    expect(screen.getByLabelText('API Key')).toHaveValue('local-secret')
    expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
    rerender(<CredentialEditor {...props} revision={3} summary="账号A · 版本3" />)
    expect(screen.queryByText('账号A · 版本3')).toBeNull()
    await user.click(screen.getByRole('button', { name: '已核对，采用此版本' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(revisions).toEqual([1, 2]); expect(close).toHaveBeenCalledOnce()
    expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ key: 'local-secret' }), 2)
  })

  it('取最新失败保留草稿且不能采用；重读deleted不允许复活', async () => {
    const user = userEvent.setup()
    const latest = vi.fn().mockRejectedValueOnce(new ApiError({ status: 0, code: 'unreachable' })).mockResolvedValueOnce({ revision: 2, summary: '账号A · 已删除', deleted: true })
    render(<CredentialEditor id="a" mode="replace-key" revision={1} summary="账号A" readLatest={latest} onSave={async () => { throw new ApiError({ status: 412, code: 'precondition_failed' }) }} onClose={() => {}} />, { wrapper })
    await user.type(screen.getByLabelText('API Key'), 'retained-secret')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(latest).toHaveBeenCalledOnce())
    expect(screen.getByRole('button', { name: '已核对，采用此版本' })).toBeDisabled()
    expect(screen.getByLabelText('API Key')).toHaveValue('retained-secret')
    await user.click(screen.getByRole('button', { name: '重新读取最新信息' }))
    expect(await screen.findByText(/账号已删除，不能继续替换/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '已核对，采用此版本' })).toBeDisabled()
  })

  it('取消/成功由拥有者卸载并清空，重复提交在请求进行中只发一次', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const save = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
    function Host() {
      const [open, setOpen] = useState(true)
      return open ? <CredentialEditor id="a" mode="create-key" revision={0} summary="新账号" readLatest={async () => ({ revision: 0, summary: '' })} onSave={save} onClose={() => setOpen(false)} /> : <button onClick={() => setOpen(true)}>重新打开</button>
    }
    render(<Host />, { wrapper })
    await user.type(screen.getByLabelText('账号名称'), '账号A'); await user.type(screen.getByLabelText('API Key'), 'cancel-secret')
    await user.click(screen.getByRole('button', { name: '取消' })); await user.click(screen.getByRole('button', { name: '重新打开' }))
    expect(screen.getByLabelText('API Key')).toHaveValue('')
    await user.type(screen.getByLabelText('账号名称'), '账号A'); await user.type(screen.getByLabelText('API Key'), 'success-secret')
    await user.dblClick(screen.getByRole('button', { name: '保存' }))
    expect(save).toHaveBeenCalledOnce()
    await act(async () => finish?.())
    await user.click(screen.getByRole('button', { name: '重新打开' }))
    expect(screen.getByLabelText('API Key')).toHaveValue('')
  })

  it('再次取最新失败后不能采用上一轮核对信息，秘密草稿仍保留', async () => {
    const user = userEvent.setup()
    const readLatest = vi.fn().mockResolvedValueOnce({ revision: 2, summary: '版本2' }).mockRejectedValueOnce(new ApiError({ status: 0, code: 'unreachable' }))
    render(<CredentialEditor id="a" mode="replace-key" revision={1} summary="版本1" readLatest={readLatest} onSave={async () => { throw new ApiError({ status: 412, code: 'precondition_failed' }) }} onClose={() => {}} />, { wrapper })
    await user.type(screen.getByLabelText('API Key'), 'retained-local-input')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await screen.findByText('版本2')
    await user.click(screen.getByRole('button', { name: '重新读取最新信息' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '已核对，采用此版本' })).toBeDisabled())
    expect(screen.getByLabelText('API Key')).toHaveValue('retained-local-input')
    expect(screen.queryByText('版本2')).toBeNull()
  })
})
