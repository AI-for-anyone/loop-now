import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, context, errorBody, makeAgent } from '@/test/api'
import { renderChat } from '@/test/chat'
import { server } from '@/test/server'
import { claudeCodeProvider, providers } from '@/test/config'
import { codexProvider } from '@/test/model-auth'

const levels: Schemas['ThinkingLevel'][] = ['low', 'medium', 'high']
const model = (id: string, name: string, thinking = levels): Schemas['Model'] => ({
  id,
  name,
  reasoning: true,
  thinkingLevels: thinking,
  defaultThinkingLevel: 'medium',
  contextWindow: 200000,
  maxTokens: 64000,
  input: ['text'],
})
const models: Record<string, Schemas['Model'][]> = {
  'claude-code': [
    model('haiku', 'Claude Haiku (latest)', ['low', 'medium', 'high', 'xhigh', 'max']),
    model('opus', 'Claude Opus (latest)', ['low', 'medium', 'high', 'xhigh', 'max']),
  ],
  anthropic: [model('claude-sonnet-5-5', 'Claude Sonnet 5.5'), model('claude-haiku-5-5', 'Claude Haiku 5.5')],
  'openai-codex': [model('gpt-a', 'GPT A')],
  // A model with one level only (off): nothing to choose, so the Thinking menu is omitted.
  openai: [model('gpt-x', 'GPT X'), { ...model('gpt-plain', 'GPT Plain', ['off']), reasoning: false, defaultThinkingLevel: 'off' }],
}

async function setup(agent: Schemas['Agent'], patch?: (body: Schemas['AgentUpdate']) => Response, extra: HttpHandler[] = []) {
  const bodies: Schemas['AgentUpdate'][] = []
  let current = agent
  const view = await renderChat({
    agent,
    handlers: [
      ...extra,
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, codexProvider, claudeCodeProvider] })),
      http.get(apiUrl('/model-providers/:providerId/models'), ({ params }) => HttpResponse.json({ providerId: String(params.providerId), models: models[String(params.providerId)] ?? [] })),
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
      http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
        const body = (await request.json()) as Schemas['AgentUpdate']
        bodies.push(body)
        if (patch) return patch(body)
        current = { ...current, ...body, runtime: body.model?.provider === 'claude-code' ? 'claude-code' : body.model ? 'pi' : current.runtime, configVersion: current.configVersion + 1 }
        return HttpResponse.json(current)
      }),
    ],
  })
  return { ...view, bodies }
}

const harnessBtn = () => screen.getByRole('button', { name: /^Harness: / })
const modelBtn = () => screen.getByRole('button', { name: /^Model: / })
const thinkingBtn = () => screen.getByRole('button', { name: /^Thinking: / })
const permissionsBtn = () => screen.getByRole('button', { name: /^Permissions: / })

describe('independent composer menus (Harness, Model, Thinking, Permissions)', () => {
  it('shows independent Harness, shortened Model, Thinking, and Permissions triggers with full hover details', async () => {
    await setup(makeAgent({ model: { provider: 'claude-code', modelId: 'haiku' }, runtime: 'claude-code', thinkingLevel: null }))
    await waitFor(() => expect(harnessBtn()).toHaveAccessibleName('Harness: Claude Code. Open to change'))
    expect(modelBtn()).toHaveAccessibleName('Model: Haiku. Open to change')
    expect(thinkingBtn()).toHaveAccessibleName('Thinking: Medium. Open to change')
    expect(permissionsBtn()).toHaveAccessibleName('Permissions: Full access. Open to change')
    expect(modelBtn()).toHaveAttribute('title', 'Run by Claude Code (this computer) · provider claude-code · model Claude Haiku (latest) (haiku) · thinking Medium')
  })

  it('the Harness trigger shows the runtime ink frame icon', async () => {
    await setup(makeAgent({ model: { provider: 'claude-code', modelId: 'haiku' }, runtime: 'claude-code', thinkingLevel: null }))
    await waitFor(() => expect(harnessBtn().querySelector('svg[data-logo="claude"]')).not.toBeNull())
    const svg = harnessBtn().querySelector('svg[data-logo="claude"]')!
    expect(svg).toHaveClass('size-5')
    expect(svg.querySelector(':scope > rect')).toHaveAttribute('fill', 'var(--ink)')
  })

  it('shortens vendor model names for bundled Pi while keeping full details in the title tooltip', async () => {
    await setup(makeAgent({ model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, runtime: 'pi', thinkingLevel: 'high' }))
    await waitFor(() => expect(harnessBtn()).toHaveAccessibleName('Harness: Pi. Open to change'))
    expect(modelBtn()).toHaveAccessibleName('Model: Sonnet 5.5. Open to change')
    expect(thinkingBtn()).toHaveAccessibleName('Thinking: High. Open to change')
    expect(modelBtn()).toHaveAttribute('title', 'Run by Pi (built in) · provider anthropic · model Claude Sonnet 5.5 (claude-sonnet-5-5) · thinking High')
  })

  it('enables or disables image attachment according to the active model capabilities', async () => {
    const seeing = { ...model('claude-sonnet-5-5', 'Claude Sonnet 5.5'), input: ['text', 'image'] }
    const withImages = { ...context, limits: { ...context.limits, maxMessageImages: 4, maxMessageImageBytes: 5 * 1024 * 1024 } }
    const extra = [
      http.get(apiUrl('/model-providers/anthropic/models'), () => HttpResponse.json({ providerId: 'anthropic', models: [seeing, model('claude-haiku-5-5', 'Claude Haiku 5.5')] })),
      http.get(apiUrl('/context'), () => HttpResponse.json(withImages)),
    ]
    const { unmount } = await setup(makeAgent({ model: { provider: 'anthropic', modelId: 'claude-haiku-5-5' }, runtime: 'pi', thinkingLevel: null }), undefined, extra)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Add image' })).toBeDisabled())
    expect(screen.getByRole('button', { name: 'Add image' })).toHaveAttribute('title', "This model doesn't take images")
    unmount()
    await setup(makeAgent({ model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, runtime: 'pi', thinkingLevel: null }), undefined, extra)
    await waitFor(() => expect(modelBtn()).toHaveAccessibleName('Model: Sonnet 5.5. Open to change'))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Add image' })).toBeEnabled())
  })

  it('omits the Thinking menu when the active model has only one fixed level (off)', async () => {
    await setup(makeAgent({ model: { provider: 'openai', modelId: 'gpt-plain' }, runtime: 'pi', thinkingLevel: null }))
    await waitFor(() => expect(modelBtn()).toHaveAccessibleName('Model: GPT Plain. Open to change'))
    expect(screen.queryByRole('button', { name: /^Thinking: / })).toBeNull()
  })

  it('switching Harness opens the Model menu for that Harness and saves immediately on model choice', async () => {
    const { user, bodies } = await setup(makeAgent({ model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, runtime: 'pi', thinkingLevel: 'high' }))
    await user.click(harnessBtn())
    const harnessMenu = await screen.findByRole('dialog', { name: 'Harness' })
    await user.click(within(harnessMenu).getByRole('option', { name: /Claude Code/ }))

    const modelMenu = await screen.findByRole('dialog', { name: 'Model' })
    await waitFor(() => expect(within(modelMenu).getByRole('combobox')).toHaveFocus())
    await user.click(await within(modelMenu).findByRole('treeitem', { name: 'Claude Haiku (latest) (haiku)' }))

    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'haiku' }, thinkingLevel: 'high' }]))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Model' })).toBeNull())
    expect(await screen.findByText('Changed; applies from the next message')).toBeInTheDocument()
    await waitFor(() => expect(harnessBtn()).toHaveAccessibleName('Harness: Claude Code. Open to change'))
    expect(modelBtn()).toHaveAccessibleName('Model: Haiku. Open to change')
    expect(thinkingBtn()).toHaveAccessibleName('Thinking: High. Open to change')
  })

  it('choosing a thinking level while a model save is still in flight keeps both optimistic changes and sends both', async () => {
    let release = () => {}
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    const agent = makeAgent({ model: { provider: 'claude-code', modelId: 'haiku' }, runtime: 'claude-code', thinkingLevel: null })
    let current = agent
    const { user, bodies } = await setup(agent)
    server.use(
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
      http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
        const body = (await request.json()) as Schemas['AgentUpdate']
        bodies.push(body)
        current = { ...current, ...body, configVersion: current.configVersion + 1 }
        const answer = current
        if (bodies.length === 1) await held
        return HttpResponse.json(answer)
      }),
    )
    await user.click(modelBtn())
    const modelMenu = await screen.findByRole('dialog', { name: 'Model' })
    await user.click(await within(modelMenu).findByRole('treeitem', { name: 'Claude Opus (latest) (opus)' }))
    expect(modelBtn()).toHaveAccessibleName('Model: Opus. Open to change')

    await user.click(thinkingBtn())
    const thinkMenu = await screen.findByRole('dialog', { name: 'Thinking' })
    await user.click(within(thinkMenu).getByRole('radio', { name: 'Max' }))
    expect(modelBtn()).toHaveAccessibleName('Model: Opus. Open to change')
    expect(thinkingBtn()).toHaveAccessibleName('Thinking: Max. Open to change')

    release()
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'opus' }, thinkingLevel: null }, { thinkingLevel: 'max' }]))
    await waitFor(() => expect(modelBtn()).toHaveAccessibleName('Model: Opus. Open to change'))
    expect(thinkingBtn()).toHaveAccessibleName('Thinking: Max. Open to change')
  })

  it('choosing a thinking level from its independent menu saves immediately and closes', async () => {
    const { user, bodies } = await setup(makeAgent({ model: { provider: 'claude-code', modelId: 'haiku' }, runtime: 'claude-code', thinkingLevel: null }))
    await waitFor(() => expect(thinkingBtn()).toHaveAccessibleName('Thinking: Medium. Open to change'))
    await user.click(thinkingBtn())
    const thinkMenu = await screen.findByRole('dialog', { name: 'Thinking' })
    await user.click(within(thinkMenu).getByRole('radio', { name: 'Max' }))
    await waitFor(() => expect(bodies).toEqual([{ thinkingLevel: 'max' }]))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Thinking' })).toBeNull())
  })

  it('leaving openai-codex also clears its speed', async () => {
    const { user, bodies } = await setup(makeAgent({ model: { provider: 'openai-codex', modelId: 'gpt-a' }, runtime: 'pi', thinkingLevel: null, serviceTier: 'priority' }))
    await user.click(modelBtn())
    const modelMenu = await screen.findByRole('dialog', { name: 'Model' })
    await user.click(await within(modelMenu).findByText('anthropic'))
    await user.click(await within(modelMenu).findByRole('treeitem', { name: 'Claude Haiku 5.5 (claude-haiku-5-5)' }))
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'anthropic', modelId: 'claude-haiku-5-5' }, thinkingLevel: null, serviceTier: null }]))
  })

  it('when a change cannot be saved, the menu stays open and reports the error', async () => {
    const { user } = await setup(makeAgent({ model: { provider: 'claude-code', modelId: 'haiku' }, runtime: 'claude-code' }), () =>
      HttpResponse.json(errorBody('validation_failed', 'model.modelId: no such model', { fields: [{ field: 'model.modelId', message: 'no such model' }] }), { status: 422 }),
    )
    await user.click(modelBtn())
    const modelMenu = await screen.findByRole('dialog', { name: 'Model' })
    await user.click(await within(modelMenu).findByRole('treeitem', { name: 'Claude Opus (latest) (opus)' }))
    expect(await within(modelMenu).findByRole('alert')).toHaveTextContent('no such model')
    expect(screen.getByRole('dialog', { name: 'Model' })).toBeInTheDocument()
  })
})
