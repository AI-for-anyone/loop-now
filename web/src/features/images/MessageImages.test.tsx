import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import { config } from '@/config'
import { MessageImages } from './MessageImages'
it('history uses only the durable API image ID, with a keyboard-accessible enlarged view', async () => {
  const id = '00000000-0000-7000-8000-000000000044'
  render(<MessageImages images={[{ id, mimeType: 'image/png', size: 32, width: 2, height: 2 }]} />)
  expect(screen.getByRole('img')).toHaveAttribute('src', `${config.apiBase}/message-images/${id}`)
  await userEvent.click(screen.getByRole('button', { name: 'Enlarge image: Image 1 · 2×2' }))
  const dialog = await screen.findByRole('dialog')
  expect(dialog).toBeInTheDocument()
  const original = within(dialog).getByRole('link', { name: 'Open original in new tab' })
  expect(original).toHaveAttribute('href', `${config.apiBase}/message-images/${id}`)
  expect(original).toHaveAttribute('target', '_blank')
  expect(original).toHaveAttribute('rel', 'noopener noreferrer')
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})
