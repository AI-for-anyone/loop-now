import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { apiUrl, errorBody } from '@/test/api'
import { makeIdentity } from '@/test/inbox'
import { fakePlugins, makeComponents, makeDetail, makeManifest, makePlugin, makePreview } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

// webhook 的一个渠道能代管外部身份；飞书通知的渠道不能（不出现在选择里）。
const webhook = makeDetail(makePlugin(), makeManifest({ permissions: ['state', 'identities.manage'], components: makeComponents({ channels: [{ id: 'hooks', label: '回调', manageIdentities: true }] }) }))
const lark = makeDetail(
  makePlugin({ id: 'lark-notify', displayName: '飞书通知', version: '0.3.1' }),
  makeManifest({ id: 'lark-notify', displayName: '飞书通知', version: '0.3.1', components: makeComponents({ channels: [{ id: 'lark', label: '飞书', manageIdentities: false }] }) }),
)

function setup(identity: Schemas['ExternalIdentity'], plugins = [webhook, lark], manager?: (body: { managedBy: Schemas['ExternalIdentityManager'] | null }) => Response | undefined) {
  const fake = fakePlugins({ details: plugins })
  const writes: unknown[] = []
  let current = identity
  server.use(
    ...fake.handlers,
    ...baseHandlers([]),
    http.get(apiUrl('/external-identities'), () => HttpResponse.json({ identities: [current] })),
    http.get(apiUrl('/external-identities/:id'), () => HttpResponse.json(current)),
    http.get(apiUrl('/external-identities/:id/tokens'), () => HttpResponse.json({ tokens: [] })),
    http.get(apiUrl('/external-identities/:id/inbox'), () => HttpResponse.json({ messages: [], nextCursor: null })),
    http.put(apiUrl('/external-identities/:id/manager'), async ({ request }) => {
      const body = (await request.json()) as { managedBy: Schemas['ExternalIdentityManager'] | null }
      writes.push(body)
      const custom = manager?.(body)
      if (custom) return custom
      current = { ...current, managedBy: body.managedBy }
      return HttpResponse.json(current)
    }),
  )
  renderRoutes(routes, `/identities/${identity.id}`)
  return { writes, fake }
}

const section = () => screen.findByRole('region', { name: '代管' })

describe('外部身份的代管', () => {
  it('列出声明了代管的插件渠道；选一个保存，写出当前由谁代管', async () => {
    const user = userEvent.setup()
    const { writes } = setup(makeIdentity())
    const box = await section()
    const select = within(box).getByLabelText('代管的插件渠道')
    await waitFor(() => expect(within(select).getAllByRole('option').map((o) => o.textContent)).toEqual(['不代管', 'Webhook 通知 · 回调']))
    expect(within(box).getByRole('button', { name: '保存' })).toBeDisabled()
    await user.selectOptions(select, 'Webhook 通知 · 回调')
    await user.click(within(box).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(writes).toEqual([{ managedBy: { pluginId: 'webhook', channelId: 'hooks' } }]))
    expect(await within(box).findByRole('status')).toHaveTextContent('由 Webhook 通知 · 回调 代管')
  })

  it('改回不代管：保存 null', async () => {
    const user = userEvent.setup()
    const { writes } = setup(makeIdentity({ managedBy: { pluginId: 'webhook', channelId: 'hooks' } }))
    const box = await section()
    const select = within(box).getByLabelText('代管的插件渠道')
    await waitFor(() => expect(select).toHaveDisplayValue('Webhook 通知 · 回调'))
    await user.selectOptions(select, '不代管')
    await user.click(within(box).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(writes).toEqual([{ managedBy: null }]))
    await waitFor(() => expect(within(box).queryByRole('status')).toBeNull())
  })

  it('代管插件不是运行中：提示未运行，绑定照旧', async () => {
    setup(makeIdentity({ managedBy: { pluginId: 'webhook', channelId: 'hooks' } }), [makeDetail(makePlugin({ status: 'host_stopped' }), webhook.manifest), lark])
    const box = await section()
    expect(await within(box).findByText(/代管插件未运行（宿主已停止）/)).toBeInTheDocument()
    expect(within(box).getByRole('link', { name: '看插件' })).toHaveAttribute('href', '/plugins/webhook')
  })

  it('保存被拒（422，渠道不再声明代管）时写出原因', async () => {
    const user = userEvent.setup()
    setup(makeIdentity(), [webhook, lark], () => HttpResponse.json(errorBody('validation_failed', '这个渠道不能代管外部身份'), { status: 422 }))
    const box = await section()
    await waitFor(() => expect(within(box).getAllByRole('option')).toHaveLength(2))
    await user.selectOptions(within(box).getByLabelText('代管的插件渠道'), 'Webhook 通知 · 回调')
    await user.click(within(box).getByRole('button', { name: '保存' }))
    expect(await within(box).findByText(/这个渠道不能代管外部身份/)).toBeInTheDocument()
  })

  it('没有能代管的插件时说明，并给出插件市场入口', async () => {
    setup(makeIdentity(), [lark])
    const box = await section()
    expect(await within(box).findByText('还没有能代管外部身份的插件。')).toBeInTheDocument()
    expect(within(box).getByRole('link', { name: '插件市场' })).toHaveAttribute('href', '/plugins')
  })
})

describe('插件页里的“可代管外部身份”', () => {
  it('安装前确认与插件详情的“包含”都列出能代管的渠道，卡片上有标签', async () => {
    const user = userEvent.setup()
    const fake = fakePlugins({ details: [webhook] })
    server.use(...fake.handlers, ...baseHandlers([]), http.post(apiUrl('/plugin-previews'), () => HttpResponse.json(makePreview({ manifest: { ...webhook.manifest, version: '1.3.0' }, installedVersion: '1.2.0' }), { status: 201 })))
    const { router } = renderRoutes(routes, '/plugins')
    const card = (await screen.findByRole('link', { name: 'Webhook 通知' })).closest('li')!
    await waitFor(() => expect(card).toHaveTextContent('可代管外部身份'))
    await router.navigate('/plugins/webhook')
    const contains = await screen.findByRole('region', { name: '这个插件包含' })
    expect(within(contains).getByText('可代管外部身份').parentElement).toHaveTextContent('可代管外部身份 · 回调 · 把发给外部身份的消息推出去，再把回复以这个身份送回')
    expect(within(contains).getByText('identities.manage').parentElement).toHaveTextContent('代管外部身份：读它的收件箱，以它的名义回复与回答')
    await user.click(screen.getByRole('button', { name: '检查更新' }))
    const dialog = await screen.findByRole('dialog', { name: /更新 Webhook 通知/ })
    expect(within(dialog).getByText('可代管外部身份').parentElement).toHaveTextContent('可代管外部身份 · 回调')
    expect(within(dialog).getByText('identities.manage')).toBeInTheDocument()
  })
})
