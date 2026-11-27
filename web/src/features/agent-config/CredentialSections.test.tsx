import { screen, waitFor, within } from '@testing-library/react'
import { delay, http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { keys } from '@/api/queries'
import { apiUrl, errorBody, makeAgent as makeBaseAgent } from '@/test/api'
import type { Schemas } from '@/api/client'
import { ghTemplate, larkTemplate, renderConfig } from '@/test/config'

const region = (name: string) => screen.getByRole('region', { name })
const makeAgent = (overrides: Partial<Schemas['Agent']> = {}) => makeBaseAgent({ scope: 'platform', projectId: null, ...overrides })

const expandCli = async (user: Awaited<ReturnType<typeof renderConfig>>['user'], cli: string) => {
  const item = within(region('Available CLIs')).getByRole('listitem', { name: cli })
  const toggle = within(item).getByRole('button', { name: new RegExp(`^${cli}\\b`) })
  if (toggle.getAttribute('aria-expanded') !== 'true') await user.click(toggle)
  return item
}

describe('CLI credential validation', () => {
  it('server field errors show next to the matching input by fields.<field name>; the rest are all listed', async () => {
    const agent = makeAgent({ clis: [{ name: 'lark-cli', hasTemplate: true, credential: null }] })
    const { user } = await renderConfig({
      agent,
      handlers: [
        http.put(apiUrl(`/agents/${agent.id}/clis/lark-cli/credential`), () =>
          HttpResponse.json(
            errorBody('validation_failed', 'fields.appId: Invalid App ID format', {
              fields: [
                { field: 'fields.appId', message: 'Invalid App ID format' },
                { field: 'fields.extra', message: 'The template has no such field' },
              ],
            }),
            { status: 422 },
          ),
        ),
      ],
    })
    const item = await expandCli(user, 'lark-cli')
    await waitFor(() => expect(within(item).getByRole('button', { name: 'Enter credential' })).toBeEnabled())
    await user.click(within(item).getByRole('button', { name: 'Enter credential' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('App ID'), 'bad')
    await user.type(within(dialog).getByLabelText('App Secret'), 'secret')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    const message = await within(dialog).findByText('Invalid App ID format')
    expect(within(dialog).getByLabelText('App ID').parentElement).toContainElement(message)
    const alert = within(dialog).getByRole('alert')
    expect(alert).toHaveTextContent('fields.extra: The template has no such field')
    expect(alert).not.toHaveTextContent('fields.appId')
  })
})

describe('Entering credentials when CLI templates are unavailable', () => {
  it('while templates load, the button is disabled and the reason is shown', async () => {
    const { user } = await renderConfig({
      agent: makeAgent({ clis: [{ name: 'gh', hasTemplate: true, credential: null }] }),
      handlers: [
        http.get(apiUrl('/cli-templates'), async () => {
          await delay('infinite')
          return HttpResponse.json({ templates: [] })
        }),
      ],
    })
    const item = await expandCli(user, 'gh')
    expect(within(item).getByRole('button', { name: 'Enter credential' })).toBeDisabled()
    expect(within(item).getByText('Loading CLI templates; credentials can be entered once they load.')).toBeInTheDocument()
  })

  it('when templates fail to load, the error shows and the button is disabled; templates arriving later do not open the dialog on their own', async () => {
    let fail = true
    const { client, user } = await renderConfig({
      agent: makeAgent({ clis: [{ name: 'gh', hasTemplate: true, credential: null }] }),
      handlers: [
        http.get(apiUrl('/cli-templates'), () =>
          fail ? HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 }) : HttpResponse.json({ templates: [ghTemplate, larkTemplate] }),
        ),
      ],
    })
    const clis = region('Available CLIs')
    expect(await within(clis).findByText('Couldn\'t load CLI templates.')).toBeInTheDocument()
    expect(within(clis).getByRole('alert')).toHaveTextContent('Internal server error')
    const item = await expandCli(user, 'gh')
    expect(within(item).getByText('CLI templates failed to load, so credentials can\'t be entered right now.')).toBeInTheDocument()
    const button = within(item).getByRole('button', { name: 'Enter credential' })
    expect(button).toBeDisabled()
    await user.click(button)
    fail = false
    await client.invalidateQueries({ queryKey: keys.cliTemplates })
    expect(await within(item).findByText('Built-in template v2.100.0')).toBeInTheDocument()
    expect(within(item).getByRole('button', { name: 'Enter credential' })).toBeEnabled()
    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
