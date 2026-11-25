import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, makeAgent } from '@/test/api'
import { renderConfig } from '@/test/config'
import { grokDefinition, withAllRuntimes } from '@/test/runtimes'
import { canRunAgent, effectiveMode } from '@/lib/permissions'

const claude = (overrides: Partial<Schemas['Agent']> = {}) => makeAgent({ model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code', ...overrides })

// Records each PATCH of the Agent and answers with the Agent changed; reading the Agent again gives the changed one.
function patches(agent: Schemas['Agent']) {
  const bodies: Schemas['AgentUpdate'][] = []
  let current = agent
  const handler = http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
    const body = (await request.json()) as Schemas['AgentUpdate']
    bodies.push(body)
    current = { ...current, ...body } as Schemas['Agent']
    return HttpResponse.json(current)
  })
  const read = http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current))
  return { bodies, handler, handlers: [handler, read] }
}

const work = () => screen.getByRole('region', { name: 'How it works' })

describe('How an Agent works (config page)', () => {
  it('the working folder is typed (or chosen) and saved on its own; it can go back to Loop Now’s workspace', async () => {
    const agent = claude()
    const api = patches(agent)
    const { user } = await renderConfig({ agent, handlers: api.handlers })
    const section = work()
    expect(within(section).getByText('None chosen: the workspace Loop Now gives it')).toBeInTheDocument()
    await user.type(within(section).getByLabelText('Working folder'), '/Users/me/code/app')
    await user.click(within(section).getByRole('button', { name: 'Save folder' }))
    await waitFor(() => expect(api.bodies).toEqual([{ workingDirectory: '/Users/me/code/app' }]))
    expect(await within(section).findByText(/Saved · Applies from the next message/)).toBeInTheDocument()
  })

  it('an Agent with a folder can go back to the workspace', async () => {
    const agent = claude({ workingDirectory: '/Users/me/code/app' })
    const api = patches(agent)
    const { user } = await renderConfig({ agent, handlers: api.handlers })
    expect(within(work()).getByLabelText('Working folder')).toHaveValue('/Users/me/code/app')
    await user.click(within(work()).getByRole('button', { name: "Use Loop Now's workspace again" }))
    await waitFor(() => expect(api.bodies).toEqual([{ workingDirectory: null }]))
  })

  it('“Use my own CLI configuration” saves at once and says in plain words what it turns on', async () => {
    const agent = claude()
    const api = patches(agent)
    const { user } = await renderConfig({ agent, handlers: api.handlers })
    const section = work()
    expect(within(section).getByText(/Off: Loop Now runs it with its own configuration/)).toBeInTheDocument()
    await user.click(within(section).getByRole('switch', { name: 'Use my own CLI configuration' }))
    await waitFor(() => expect(api.bodies).toEqual([{ useLocalConfig: true }]))
  })

  it('an imported Agent says where it came from, and with its own configuration lists what that turns on', async () => {
    const agent = claude({ useLocalConfig: true, importedFrom: { cli: 'claude', sessionId: 's1', path: '/Users/me/.claude/projects/app/s1.jsonl', importedAt: '2026-11-25T08:00:00Z' } })
    await renderConfig({ agent })
    const section = work()
    expect(within(section).getByText(/Imported from a Claude Code conversation/)).toBeInTheDocument()
    expect(within(section).getByText('/Users/me/.claude/projects/app/s1.jsonl')).toBeInTheDocument()
    expect(within(section).getByRole('switch', { name: 'Use my own CLI configuration' })).toBeChecked()
    expect(within(section).getByText('Your plugins, skills and MCP servers')).toBeInTheDocument()
    expect(within(section).getByText(/the permission mode below is what holds it back/)).toBeInTheDocument()
  })

  it('the permission mode is chosen from the four, saved at once', async () => {
    const agent = claude()
    const api = patches(agent)
    const { user } = await renderConfig({ agent, handlers: api.handlers })
    const modes = within(work()).getByRole('radiogroup', { name: 'Permissions' })
    expect(within(modes).getAllByRole('radio').map((radio) => radio.textContent)).toEqual([
      expect.stringContaining('Supervised'),
      expect.stringContaining('Auto-accept edits'),
      expect.stringContaining('Auto'),
      expect.stringContaining('Full access'),
    ])
    expect(within(modes).getByRole('radio', { name: /Full access/ })).toBeChecked()
    await user.click(within(modes).getByRole('radio', { name: /^Supervised/ }))
    await waitFor(() => expect(api.bodies).toEqual([{ permissionMode: 'supervised' }]))
  })

  it('a runtime without the chosen mode says it runs as Supervised', async () => {
    const agent = makeAgent({ model: { provider: 'grok-cli', modelId: 'grok-4' }, runtime: 'grok', permissionMode: 'accept_edits' })
    await renderConfig({ agent, handlers: [http.get(apiUrl('/runtime-definitions'), () => HttpResponse.json(withAllRuntimes()))] })
    expect(await within(work()).findByText('Grok has no “Auto-accept edits”; it runs as Supervised')).toBeInTheDocument()
  })

  it('on Pi the folder and the local configuration are not offered, and only Full access is', async () => {
    const agent = makeAgent()
    await renderConfig({ agent })
    const section = work()
    expect(await within(section).findByText("Pi can't work in a chosen folder.")).toBeInTheDocument()
    expect(within(section).getByLabelText('Working folder')).toBeDisabled()
    expect(within(section).getByRole('switch', { name: 'Use my own CLI configuration' })).toBeDisabled()
    expect(within(section).getByRole('radio', { name: /^Supervised/ })).toBeDisabled()
    expect(within(section).getByRole('radio', { name: /Full access/ })).toBeEnabled()
    expect(within(section).getByText('Pi never asks for approval; it runs with full access only')).toBeInTheDocument()
  })
})

describe('The permission rules', () => {
  it('a runtime without a mode runs it as Supervised, never Full access', () => {
    expect(effectiveMode('accept_edits', grokDefinition)).toBe('supervised')
    expect(effectiveMode('auto', grokDefinition)).toBe('auto')
    expect(effectiveMode('full', undefined)).toBe('full')
  })

  it('an Agent with its own folder, or asking before it acts, cannot run on a runtime that cannot do that', () => {
    const pi = withAllRuntimes().runtimes[0]
    expect(canRunAgent({ workingDirectory: null, permissionMode: 'full' }, pi)).toBe(true)
    expect(canRunAgent({ workingDirectory: '/code/app', permissionMode: 'full' }, pi)).toBe(false)
    expect(canRunAgent({ workingDirectory: null, permissionMode: 'supervised' }, pi)).toBe(false)
    expect(canRunAgent({ workingDirectory: '/code/app', permissionMode: 'accept_edits' }, grokDefinition)).toBe(true)
  })
})
