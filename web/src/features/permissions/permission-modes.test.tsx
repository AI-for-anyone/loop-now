import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, makeAgent } from '@/test/api'
import { renderChat } from '@/test/chat'
import { catalogHandlers } from '@/test/config'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { withAllRuntimes } from '@/test/runtimes'

const claude = (overrides: Partial<Schemas['Agent']> = {}) => makeAgent({ model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code', ...overrides })

function patches(agent: Schemas['Agent']) {
  const bodies: Schemas['AgentUpdate'][] = []
  return { bodies, handler: http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
    const body = (await request.json()) as Schemas['AgentUpdate']
    bodies.push(body)
    return HttpResponse.json({ ...agent, ...body })
  }) }
}

const openPermissions = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(await screen.findByRole('button', { name: /^Permissions: / }))
  return screen.findByRole('dialog', { name: 'Permissions' })
}

const openHarness = async (user: ReturnType<typeof userEvent.setup>) => {
  await user.click(await screen.findByRole('button', { name: /^Harness: / }))
  return screen.findByRole('dialog', { name: 'Harness' })
}

describe('The permission mode in the message box', () => {
  it('is chosen in its own independent Permissions menu and saved at once', async () => {
    const agent = claude()
    const api = patches(agent)
    const { user } = await renderChat({ agent, handlers: [api.handler, ...catalogHandlers] })
    expect(await screen.findByRole('button', { name: /^Permissions: / })).toHaveAccessibleName(/Full access/)
    const menu = await openPermissions(user)
    const modes = within(menu).getByRole('radiogroup', { name: 'Permissions' })
    expect(within(modes).getByRole('radio', { name: /Full access/ })).toHaveAttribute('aria-checked', 'true')
    expect(within(menu).getByText('Asks before running commands and changing files')).toBeInTheDocument()
    await user.click(within(modes).getByRole('radio', { name: /^Supervised/ }))
    await waitFor(() => expect(api.bodies).toEqual([{ permissionMode: 'supervised' }]))
    await waitFor(() => expect(screen.getByRole('button', { name: /^Permissions: / })).toHaveAccessibleName(/Supervised/))
  })

  it('on a runtime that asks by its own rules, the mode menu says some commands run without asking', async () => {
    const agent = makeAgent({ model: { provider: 'grok-cli', modelId: 'grok-4' }, runtime: 'grok', permissionMode: 'supervised' })
    const { user } = await renderChat({ agent, handlers: [http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(withAllRuntimes())), ...catalogHandlers] })
    const menu = await openPermissions(user)
    expect(await within(menu).findByText('Grok decides by its own permission rules what to ask about, so some commands run without asking; to be asked about each one, use Claude Code or Codex')).toBeInTheDocument()
  })

  it('an Agent with its own folder is not offered Pi as who runs it', async () => {
    const agent = claude({ workingDirectory: '/Users/me/code/app' })
    const { user } = await renderChat({ agent, handlers: catalogHandlers })
    const menu = await openHarness(user)
    const options = await within(menu).findAllByRole('option')
    expect(options.map((opt) => opt.textContent)).not.toContainEqual(expect.stringMatching(/^Pi/))
    expect(options.map((opt) => opt.textContent)).toContainEqual(expect.stringMatching(/^Claude Code/))
  })

  it('an Agent that asks before acting is not offered Pi either', async () => {
    const { user } = await renderChat({ agent: claude({ permissionMode: 'supervised' }), handlers: catalogHandlers })
    const menu = await openHarness(user)
    expect((await within(menu).findAllByRole('option')).map((opt) => opt.textContent)).not.toContainEqual(expect.stringMatching(/^Pi/))
  })

  it('an Agent on Full access with no folder still is', async () => {
    const { user } = await renderChat({ agent: claude(), handlers: catalogHandlers })
    const menu = await openHarness(user)
    expect((await within(menu).findAllByRole('option')).map((opt) => opt.textContent)).toContainEqual(expect.stringMatching(/^Pi/))
  })
})

describe('The default for new Agents (Settings)', () => {
  it('starts on Full access and is saved at once', async () => {
    const bodies: Schemas['PermissionSettings'][] = []
    server.use(
      ...baseHandlers([]),
      http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{}', revision: 1 })),
      http.put(apiUrl('/permission-settings'), async ({ request }) => {
        const body = (await request.json()) as Schemas['PermissionSettings']
        bodies.push(body)
        return HttpResponse.json(body)
      }),
    )
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    const section = await screen.findByRole('region', { name: 'Permissions for new Agents' })
    const modes = await within(section).findByRole('radiogroup', { name: 'Permissions for new Agents' })
    expect(within(modes).getByRole('radio', { name: /Full access/ })).toBeChecked()
    await user.click(within(modes).getByRole('radio', { name: /^Supervised/ }))
    await waitFor(() => expect(bodies).toEqual([{ defaultMode: 'supervised' }]))
    expect(await within(section).findByText('Saved')).toBeInTheDocument()
  })
})
