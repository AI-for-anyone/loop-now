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
  return <ImagePicker images={images} onChange={setImages} limits={limits} disabled={disabled}><textarea aria-label="Draft" /></ImagePicker>
}
it('shows only a compact plus button with an accessible name and opens the file picker', async () => {
  render(<Harness />)
  const button = screen.getByRole('button', { name: 'Add image' })
  expect(button).toHaveAttribute('title', 'Add image')
  expect(button).toHaveAttribute('data-size', 'icon-sm')
  expect(button.textContent).toBe('')
  expect(button.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  expect(screen.queryByText(/Paste a screenshot|(Up to|At most) .*images/)).not.toBeInTheDocument()
  const picker = screen.getByLabelText('Choose an image file')
  const click = vi.spyOn(picker, 'click')
  await userEvent.click(button)
  expect(click).toHaveBeenCalledOnce()
})
it('keeps both the plus button and file selection disabled', () => {
  render(<Harness disabled />)
  expect(screen.getByRole('button', { name: 'Add image' })).toBeDisabled()
  expect(screen.getByLabelText('Choose an image file')).toBeDisabled()
  fireEvent.paste(screen.getByRole('textbox'), { clipboardData: { files: [image('disabled.png')] } })
  expect(screen.queryByRole('list', { name: 'Images to send' })).not.toBeInTheDocument()
})
it('selects, pastes and drops images without reading other file formats; removal and zoom are explicit', async () => {
  render(<Harness />)
  await userEvent.upload(screen.getByLabelText('Choose an image file'), image('chosen.png'))
  expect(await screen.findByAltText('chosen.png')).toBeInTheDocument()
  fireEvent.paste(screen.getByRole('textbox'), { clipboardData: { files: [image('pasted.png')] } })
  fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { types: ['Files'], files: [image('dropped.webp', 'data', 'image/webp')] } })
  expect(await screen.findByAltText('dropped.webp')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Enlarge image: pasted.png' }))
  expect(await screen.findByRole('dialog')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  await userEvent.click(screen.getByRole('button', { name: 'Remove image 1' }))
  expect(screen.queryByAltText('chosen.png')).not.toBeInTheDocument()
  fireEvent.paste(screen.getByRole('textbox'), { clipboardData: { files: [image('unsafe.svg', '<svg/>', 'image/svg+xml')] } })
  expect(await screen.findByRole('alert')).toHaveTextContent('Only PNG, JPEG, and WebP')
  expect(screen.queryByAltText('unsafe.svg')).not.toBeInTheDocument()
})
it('hands the thumbnail strip to the composer; upload state and removal show on each thumbnail', async () => {
  const drafts = [{ id: 'a', file: image('first.png') }, { id: 'b', file: image('second.png') }]
  const strip = (uploads: ImageUploads, images = drafts) => <ImagePicker images={images} onChange={() => {}} limits={limits} uploads={uploads}>{(add, strip) => <div data-testid="box">{strip}<div data-testid="row">{add}<textarea aria-label="Draft" /></div></div>}</ImagePicker>
  const view = render(strip({ a: 'uploading', b: 'uploading' }, []))
  expect(screen.getByTestId('box').firstElementChild).toBe(screen.getByTestId('row'))
  view.rerender(strip({ a: 'uploading', b: 'uploading' }))
  const list = await screen.findByRole('list', { name: 'Images to send' })
  expect(screen.getByTestId('box').firstElementChild).toContainElement(list)
  expect(within(list).getAllByRole('listitem')).toHaveLength(2)
  expect(within(list).getAllByText('Uploading')).toHaveLength(2)
  expect(screen.getByRole('status')).toHaveTextContent('Uploading images…')
  view.rerender(strip({ b: 'failed' }))
  const [first, second] = within(list).getAllByRole('listitem')
  expect(within(first).queryByText(/Upload/)).not.toBeInTheDocument()
  expect(within(second).getByText('Upload failed')).toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
  expect(within(second).getByRole('button', { name: 'Remove image 2' })).toHaveAttribute('title', 'Remove image 2')
})
it('checks count, individual/total bytes and zero bytes atomically', () => {
  const existing = [{ id: 'old', file: image('old.png', '12345678') }]
  expect(imageProblem(existing, [image('new.png', '12345')], limits)).toContain('total at most')
  expect(imageProblem([], [image('large.png', '123456789')], limits)).toContain('Each image')
  expect(imageProblem([], Array.from({ length: 4 }, () => image('one.png', '1')), limits)).toContain('At most 3 images')
  expect(imageProblem([], [image('empty.png', '')], limits)).toContain('is empty')
})
