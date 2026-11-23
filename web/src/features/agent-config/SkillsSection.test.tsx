import { screen, within } from '@testing-library/react'
import { delay, http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, errorBody } from '@/test/api'
import { renderConfig } from '@/test/config'

const openAddDialog = async (user: Awaited<ReturnType<typeof renderConfig>>['user']) => {
  await user.click(within(screen.getByRole('region', { name: 'Skills' })).getByRole('button', { name: 'Add from library' }))
  return screen.findByRole('dialog')
}

describe('Add from the Skills library', () => {
  it('shows a loading state while the library loads', async () => {
    const { user } = await renderConfig({
      handlers: [
        http.get(apiUrl('/skills'), async () => {
          await delay('infinite')
          return HttpResponse.json({ skills: [] })
        }),
      ],
    })
    const dialog = await openAddDialog(user)
    expect(within(dialog).getByText('Loading…')).toBeInTheDocument()
  })

  it('shows the reason when the library fails to load', async () => {
    const { user } = await renderConfig({
      handlers: [http.get(apiUrl('/skills'), () => HttpResponse.json(errorBody('internal_error', 'Database unavailable'), { status: 500 }))],
    })
    const dialog = await openAddDialog(user)
    expect(await within(dialog).findByText(/Database unavailable/)).toBeInTheDocument()
    expect(within(dialog).queryByText('Loading…')).not.toBeInTheDocument()
  })
})
