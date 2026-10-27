import { useState } from 'react'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { ImagePicker } from './ImagePicker'
import { imageProblem, type ImageDraft, type ImageUploads } from './image-drafts'
const limits = { maxCount: 3, maxImageBytes: 8, maxTotalBytes: 12 }
const image = (name: string, value = 'data', type = 'image/png') => new File([value], name, { type })
function Harness({ disabled = false }: { disabled?: boolean }) {
  const [images, setImages] = useState<ImageDraft[]>([])
  return <ImagePicker images={images} onChange={setImages} limits={limits} disabled={disabled}><textarea aria-label="草稿" /></ImagePicker>
}
it('shows only a compact plus button with an accessible name and opens the file picker', async () => {
  render(<Harness />)
  const button = screen.getByRole('button', { name: '添加图片' })
  expect(button).toHaveAttribute('title', '添加图片')
  expect(button).toHaveAttribute('data-size', 'icon-sm')
  expect(button.textContent).toBe('')
  expect(button.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  expect(screen.queryByText(/可粘贴截图|最多.*张/)).not.toBeInTheDocument()
  const picker = screen.getByLabelText('选择图片文件')
  const click = vi.spyOn(picker, 'click')
  await userEvent.click(button)
  expect(click).toHaveBeenCalledOnce()
})
it('keeps both the plus button and file selection disabled', () => {
  render(<Harness disabled />)
  expect(screen.getByRole('button', { name: '添加图片' })).toBeDisabled()
  expect(screen.getByLabelText('选择图片文件')).toBeDisabled()
  fireEvent.paste(screen.getByRole('textbox'), { clipboardData: { files: [image('禁用.png')] } })
  expect(screen.queryByRole('list', { name: '待发送图片' })).not.toBeInTheDocument()
})
it('selects, pastes and drops images without reading other file formats; removal and zoom are explicit', async () => {
  render(<Harness />)
  await userEvent.upload(screen.getByLabelText('选择图片文件'), image('选择.png'))
  expect(await screen.findByAltText('选择.png')).toBeInTheDocument()
  fireEvent.paste(screen.getByRole('textbox'), { clipboardData: { files: [image('粘贴.png')] } })
  fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { types: ['Files'], files: [image('拖入.webp', 'data', 'image/webp')] } })
  expect(await screen.findByAltText('拖入.webp')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '放大图片：粘贴.png' }))
  expect(await screen.findByRole('dialog')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  await userEvent.click(screen.getByRole('button', { name: '移除图片 1' }))
  expect(screen.queryByAltText('选择.png')).not.toBeInTheDocument()
  fireEvent.paste(screen.getByRole('textbox'), { clipboardData: { files: [image('unsafe.svg', '<svg/>', 'image/svg+xml')] } })
  expect(await screen.findByRole('alert')).toHaveTextContent('只支持 PNG、JPEG、WebP')
  expect(screen.queryByAltText('unsafe.svg')).not.toBeInTheDocument()
})
it('hands the thumbnail strip to the composer; upload state and removal show on each thumbnail', async () => {
  const drafts = [{ id: 'a', file: image('一.png') }, { id: 'b', file: image('二.png') }]
  const strip = (uploads: ImageUploads, images = drafts) => <ImagePicker images={images} onChange={() => {}} limits={limits} uploads={uploads}>{(add, strip) => <div data-testid="box">{strip}<div data-testid="row">{add}<textarea aria-label="草稿" /></div></div>}</ImagePicker>
  const view = render(strip({ a: 'uploading', b: 'uploading' }, []))
  expect(screen.getByTestId('box').firstElementChild).toBe(screen.getByTestId('row'))
  view.rerender(strip({ a: 'uploading', b: 'uploading' }))
  const list = await screen.findByRole('list', { name: '待发送图片' })
  expect(screen.getByTestId('box').firstElementChild).toContainElement(list)
  expect(within(list).getAllByRole('listitem')).toHaveLength(2)
  expect(within(list).getAllByText('上传中')).toHaveLength(2)
  expect(screen.getByRole('status')).toHaveTextContent('正在上传图片…')
  view.rerender(strip({ b: 'failed' }))
  const [first, second] = within(list).getAllByRole('listitem')
  expect(within(first).queryByText(/上传/)).not.toBeInTheDocument()
  expect(within(second).getByText('上传失败')).toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
  expect(within(second).getByRole('button', { name: '移除图片 2' })).toHaveAttribute('title', '移除图片 2')
})
it('checks count, individual/total bytes and zero bytes atomically', () => {
  const existing = [{ id: 'old', file: image('old.png', '12345678') }]
  expect(imageProblem(existing, [image('new.png', '12345')], limits)).toContain('合计')
  expect(imageProblem([], [image('large.png', '123456789')], limits)).toContain('单张')
  expect(imageProblem([], Array.from({ length: 4 }, () => image('one.png', '1')), limits)).toContain('最多 3 张')
  expect(imageProblem([], [image('empty.png', '')], limits)).toContain('为空')
})
