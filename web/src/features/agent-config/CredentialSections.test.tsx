import { screen, waitFor, within } from '@testing-library/react'
import { delay, http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { keys } from '@/api/queries'
import { apiUrl, errorBody, makeAgent as makeBaseAgent } from '@/test/api'
import type { Schemas } from '@/api/client'
import { ghTemplate, larkTemplate, renderConfig } from '@/test/config'

const region = (name: string) => screen.getByRole('region', { name })
const makeAgent = (overrides: Partial<Schemas['Agent']> = {}) => makeBaseAgent({ scope: 'platform', projectId: null, ...overrides })

describe('CLI 凭证的校验', () => {
  it('服务端的字段级错误按 fields.<字段名> 显示在对应输入旁，其余的全部列出', async () => {
    const agent = makeAgent({ clis: [{ name: 'lark-cli', hasTemplate: true, credential: null }] })
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.put(apiUrl(`/agents/${agent.id}/clis/lark-cli/credential`), () =>
          HttpResponse.json(
            errorBody('validation_failed', 'fields.appId：App ID 格式不对', {
              fields: [
                { field: 'fields.appId', message: 'App ID 格式不对' },
                { field: 'fields.extra', message: '模板中没有这个录入项' },
              ],
            }),
            { status: 422 },
          ),
        ),
      ],
    })
    const item = within(region('可用 CLI')).getByRole('listitem', { name: 'lark-cli' })
    await waitFor(() => expect(within(item).getByRole('button', { name: '录入凭证' })).toBeEnabled())
    await user.click(within(item).getByRole('button', { name: '录入凭证' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('App ID'), 'bad')
    await user.type(within(dialog).getByLabelText('App Secret'), 'secret')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    const message = await within(dialog).findByText('App ID 格式不对')
    expect(within(dialog).getByLabelText('App ID').parentElement).toContainElement(message)
    const alert = within(dialog).getByRole('alert')
    expect(alert).toHaveTextContent('fields.extra：模板中没有这个录入项')
    expect(alert).not.toHaveTextContent('fields.appId')
  })
})

describe('CLI 模板不可用时的凭证录入', () => {
  it('模板加载中时按钮禁用并说明原因', async () => {
    await renderConfig({
      agent: makeAgent({ clis: [{ name: 'gh', hasTemplate: true, credential: null }] }),
      handlers: [
        http.get(apiUrl('/cli-templates'), async () => {
          await delay('infinite')
          return HttpResponse.json({ templates: [] })
        }),
      ],
    })
    const item = within(region('可用 CLI')).getByRole('listitem', { name: 'gh' })
    expect(within(item).getByRole('button', { name: '录入凭证' })).toBeDisabled()
    expect(within(item).getByText('正在加载 CLI 模板，加载完成后才能录入凭证。')).toBeInTheDocument()
  })

  it('模板加载失败时显示错误、按钮禁用；之后模板到达也不会自己弹出对话框', async () => {
    let fail = true
    const { client, user } = await renderConfig({
      agent: makeAgent({ clis: [{ name: 'gh', hasTemplate: true, credential: null }] }),
      handlers: [
        http.get(apiUrl('/cli-templates'), () =>
          fail ? HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 }) : HttpResponse.json({ templates: [ghTemplate, larkTemplate] }),
        ),
      ],
    })
    const clis = region('可用 CLI')
    expect(await within(clis).findByText('无法获取 CLI 模板。')).toBeInTheDocument()
    expect(within(clis).getByRole('alert')).toHaveTextContent('服务端内部错误')
    const item = within(clis).getByRole('listitem', { name: 'gh' })
    expect(within(item).getByText('CLI 模板加载失败，暂时不能录入凭证。')).toBeInTheDocument()
    const button = within(item).getByRole('button', { name: '录入凭证' })
    expect(button).toBeDisabled()
    await user.click(button)
    fail = false
    await client.invalidateQueries({ queryKey: keys.cliTemplates })
    expect(await within(item).findByText('内置模板 v2.100.0')).toBeInTheDocument()
    expect(within(item).getByRole('button', { name: '录入凭证' })).toBeEnabled()
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
