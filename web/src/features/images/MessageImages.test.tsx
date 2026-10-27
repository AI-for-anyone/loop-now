import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it } from 'vitest'
import { config } from '@/config'
import { MessageImages } from './MessageImages'
it('history uses only the durable API image ID, with a keyboard-accessible enlarged view', async () => {
  const id = '00000000-0000-7000-8000-000000000044'
  render(<MessageImages images={[{ id, mimeType: 'image/png', size: 32, width: 2, height: 2 }]} />)
  expect(screen.getByRole('img')).toHaveAttribute('src', `${config.apiBase}/message-images/${id}`)
  await userEvent.click(screen.getByRole('button', { name: '放大图片：图片 1 · 2×2' }))
  expect(await screen.findByRole('dialog')).toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})
