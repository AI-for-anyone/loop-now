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
  // The precheck takes effect only after the limits have loaded.
  await waitFor(() => expect(client.isFetching()).toBe(0))
  return { user, onArchive }
}

describe('Upload folder', () => {
  it('shows the reason and uploads nothing when reading or packing fails', async () => {
    const { user, onArchive } = await setup()
    const broken = withPath('x', 'demo/run.sh')
    broken.arrayBuffer = () => Promise.reject(new DOMException('cannot read', 'NotReadableError'))
    await user.upload(screen.getByLabelText('Upload folder'), [withPath('---\nname: demo\n---', 'demo/SKILL.md'), broken])
    expect(await screen.findByText('Couldn\'t read or pack the folder. Make sure every file in it is readable, then try again.')).toBeInTheDocument()
    expect(onArchive).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Upload folder' })).toBeEnabled()
  })

  it('shows that it is uploading and disables the upload buttons while reading and packing', async () => {
    const { user, onArchive } = await setup()
    let release!: (buffer: ArrayBuffer) => void
    const slow = withPath('echo hi', 'demo/run.sh')
    slow.arrayBuffer = () => new Promise((resolve) => (release = resolve))
    await user.upload(screen.getByLabelText('Upload folder'), [slow])
    expect(await screen.findByText('Uploading…')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Upload folder' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Upload zip' })).toBeDisabled()
    release(new TextEncoder().encode('echo hi').buffer as ArrayBuffer)
    await waitFor(() => expect(onArchive).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Upload folder' })).toBeEnabled())
  })

  it('.DS_Store and __MACOSX/ are not counted as files and are left out of the archive', async () => {
    const { user, onArchive } = await setup({ maxSkillEntries: 2 })
    await user.upload(screen.getByLabelText('Upload folder'), [
      withPath('---\nname: demo\n---', 'demo/SKILL.md'),
      withPath('echo hi', 'demo/scripts/run.sh'),
      withPath('finder', 'demo/.DS_Store'),
      withPath('finder', 'demo/scripts/.DS_Store'),
      withPath('fork', 'demo/__MACOSX/._SKILL.md'),
    ])
    await waitFor(() => expect(onArchive).toHaveBeenCalledTimes(1))
    expect(screen.queryByText(/Too many files/)).not.toBeInTheDocument()
    const entries = unzipSync(new Uint8Array(await onArchive.mock.calls[0][0].arrayBuffer()))
    expect(Object.keys(entries).sort()).toEqual(['SKILL.md', 'scripts/run.sh'])
  })

  it('.DS_Store does not count toward the size', async () => {
    const { user, onArchive } = await setup({ maxSkillArchive: 1024 })
    await user.upload(screen.getByLabelText('Upload folder'), [withPath('a'.repeat(100), 'demo/SKILL.md'), withPath('x'.repeat(2000), 'demo/.DS_Store')])
    await waitFor(() => expect(onArchive).toHaveBeenCalledTimes(1))
    expect(screen.queryByText(/Over the archive limit/)).not.toBeInTheDocument()
  })

  it('warns locally and uploads nothing when the packed archive is over the limit', async () => {
    // The raw content is only 2 bytes, but the archive carries file headers, so it exceeds the 100-byte limit.
    const { user, onArchive } = await setup({ maxSkillArchive: 100 })
    await user.upload(screen.getByLabelText('Upload folder'), [withPath('a', 'demo/SKILL.md'), withPath('b', 'demo/scripts/run.sh')])
    expect(await screen.findByText('Over the archive limit (100 B)')).toBeInTheDocument()
    expect(onArchive).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Upload folder' })).toBeEnabled()
  })
})
