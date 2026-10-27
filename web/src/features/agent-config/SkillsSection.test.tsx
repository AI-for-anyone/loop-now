import { screen, within } from '@testing-library/react'
import { delay, http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, errorBody } from '@/test/api'
import { renderConfig } from '@/test/config'

const openAddDialog = async (user: Awaited<ReturnType<typeof renderConfig>>['user']) => {
  await user.click(within(screen.getByRole('region', { name: 'Skills' })).getByRole('button', { name: '从库中添加' }))
  return screen.findByRole('dialog')
}

describe('从 Skills 库添加', () => {
  it('库加载中显示加载状态', async () => {
    const { user } = await renderConfig({
      handlers: [
        http.get(apiUrl('/skills'), async () => {
          await delay('infinite')
          return HttpResponse.json({ skills: [] })
        }),
      ],
    })
    const dialog = await openAddDialog(user)
    expect(within(dialog).getByText('加载中…')).toBeInTheDocument()
  })

  it('库加载失败时显示原因', async () => {
    const { user } = await renderConfig({
      handlers: [http.get(apiUrl('/skills'), () => HttpResponse.json(errorBody('internal_error', '数据库不可用'), { status: 500 }))],
    })
    const dialog = await openAddDialog(user)
    expect(await within(dialog).findByText(/数据库不可用/)).toBeInTheDocument()
    expect(within(dialog).queryByText('加载中…')).not.toBeInTheDocument()
  })
})
