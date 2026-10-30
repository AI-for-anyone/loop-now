import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, sidebar } from '@/test/shell'

describe('全局设置页草稿导航', () => {
  it('模型配置加载后可编辑，未保存时离开需要确认', async () => {
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })),
      ...baseHandlers([]),
    )
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, '/settings')
    const editor = await screen.findByLabelText('models.json')
    await user.keyboard('{Control>}{End}{/Control}')
    await user.type(editor, ' ')
    await user.click(within(sidebar()).getByRole('link', { name: '插件市场' }))
    expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/settings')
    expect(editor.textContent?.trim()).toBe('{"providers":{}}')
    expect(editor.textContent).not.toBe('{"providers":{}}')
  })
})
