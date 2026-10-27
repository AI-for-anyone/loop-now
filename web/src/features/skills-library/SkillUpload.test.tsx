import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { unzipSync } from 'fflate'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, context } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { SkillUpload } from './SkillUpload'

function withPath(content: BlobPart, relativePath: string) {
  const file = new File([content], relativePath.split('/').at(-1)!)
  Object.defineProperty(file, 'webkitRelativePath', { value: relativePath })
  return file
}

async function setup(limits: Partial<Schemas['Limits']> = {}) {
  server.use(http.get(apiUrl('/context'), () => HttpResponse.json({ ...context, limits: { ...context.limits, ...limits } })))
  const onArchive = vi.fn<(archive: Blob) => Promise<unknown>>(() => Promise.resolve())
  const user = userEvent.setup()
  const { client } = renderWithQuery(<SkillUpload onArchive={onArchive} />)
  // 等上限加载完成，预检才会生效。
  await waitFor(() => expect(client.isFetching()).toBe(0))
  return { user, onArchive }
}

describe('上传目录', () => {
  it('读取或打包失败时显示原因，不上传', async () => {
    const { user, onArchive } = await setup()
    const broken = withPath('x', 'demo/run.sh')
    broken.arrayBuffer = () => Promise.reject(new DOMException('cannot read', 'NotReadableError'))
    await user.upload(screen.getByLabelText('上传目录'), [withPath('---\nname: demo\n---', 'demo/SKILL.md'), broken])
    expect(await screen.findByText('读取或打包目录失败，请确认目录中的文件都可以读取后重试。')).toBeInTheDocument()
    expect(onArchive).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: '上传目录' })).toBeEnabled()
  })

  it('读取与打包期间显示上传中并禁用上传按钮', async () => {
    const { user, onArchive } = await setup()
    let release!: (buffer: ArrayBuffer) => void
    const slow = withPath('echo hi', 'demo/run.sh')
    slow.arrayBuffer = () => new Promise((resolve) => (release = resolve))
    await user.upload(screen.getByLabelText('上传目录'), [slow])
    expect(await screen.findByText('上传中…')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上传目录' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '上传 zip' })).toBeDisabled()
    release(new TextEncoder().encode('echo hi').buffer as ArrayBuffer)
    await waitFor(() => expect(onArchive).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(screen.getByRole('button', { name: '上传目录' })).toBeEnabled())
  })

  it('.DS_Store 与 __MACOSX/ 不计入文件数，也不放进压缩包', async () => {
    const { user, onArchive } = await setup({ maxSkillEntries: 2 })
    await user.upload(screen.getByLabelText('上传目录'), [
      withPath('---\nname: demo\n---', 'demo/SKILL.md'),
      withPath('echo hi', 'demo/scripts/run.sh'),
      withPath('finder', 'demo/.DS_Store'),
      withPath('finder', 'demo/scripts/.DS_Store'),
      withPath('fork', 'demo/__MACOSX/._SKILL.md'),
    ])
    await waitFor(() => expect(onArchive).toHaveBeenCalledTimes(1))
    expect(screen.queryByText(/文件数超过上限/)).not.toBeInTheDocument()
    const entries = unzipSync(new Uint8Array(await onArchive.mock.calls[0][0].arrayBuffer()))
    expect(Object.keys(entries).sort()).toEqual(['SKILL.md', 'scripts/run.sh'])
  })

  it('.DS_Store 不计入大小', async () => {
    const { user, onArchive } = await setup({ maxSkillArchive: 1024 })
    await user.upload(screen.getByLabelText('上传目录'), [withPath('a'.repeat(100), 'demo/SKILL.md'), withPath('x'.repeat(2000), 'demo/.DS_Store')])
    await waitFor(() => expect(onArchive).toHaveBeenCalledTimes(1))
    expect(screen.queryByText(/超过压缩包上限/)).not.toBeInTheDocument()
  })

  it('打包后的压缩包超过上限时本地提示，不上传', async () => {
    // 原始内容只有 2 字节，但压缩包带有文件头，会超过 100 字节的上限。
    const { user, onArchive } = await setup({ maxSkillArchive: 100 })
    await user.upload(screen.getByLabelText('上传目录'), [withPath('a', 'demo/SKILL.md'), withPath('b', 'demo/scripts/run.sh')])
    expect(await screen.findByText('超过压缩包上限（100 B）')).toBeInTheDocument()
    expect(onArchive).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: '上传目录' })).toBeEnabled()
  })
})
