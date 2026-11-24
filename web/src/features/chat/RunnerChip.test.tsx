import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { renderChat } from '@/test/chat'
import { server } from '@/test/server'
import { claudeCodeProvider, providers } from '@/test/config'
import { codexProvider } from '@/test/model-auth'
import { chooseModel, findRunnerPicker } from '@/test/model-picker'
import { chooseThinking } from '@/test/thinking'

const levels: Schemas['ThinkingLevel'][] = ['low', 'medium', 'high']
const model = (id: string, name: string, thinking = levels): Schemas['Model'] => ({ id, name, reasoning: true, thinkingLevels: thinking, defaultThinkingLevel: 'medium', contextWindow: 200000, maxTokens: 64000, input: ['text'] })
const models: Record<string, Schemas['Model'][]> = {
  'claude-code': [model('haiku', 'Claude Haiku (latest)', ['low', 'medium', 'high', 'xhigh', 'max']), model('opus', 'Claude Opus (latest)', ['low', 'medium', 'high', 'xhigh', 'max'])],
  anthropic: [model('claude-sonnet-5-5', 'Claude Sonnet 5.5'), model('claude-haiku-5-5', 'Claude Haiku 5.5')],
  openai: [model('gpt-x', 'GPT X')],
  'openai-codex': [model('gpt-a', 'GPT A')],
}

// The chat page of an Agent, with the catalog and a record of every change saved from the chip.
async function setup(agent: Schemas['Agent'], patch?: (body: Schemas['AgentUpdate']) => Response) {
  const bodies: Schemas['AgentUpdate'][] = []
  let current = agent
  const view = await renderChat({
    agent,
    handlers: [
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

const chip = () => screen.getByRole('button', { name: /^Runs on: / })

describe('who runs it, on the message box', () => {
  it('a CLI on this computer reads "name · model · level", the level being the model default when none is set', async () => {
    await setup(makeAgent({ model: { provider: 'claude-code', modelId: 'haiku' }, runtime: 'claude-code', thinkingLevel: null }))
    await waitFor(() => expect(chip()).toHaveAccessibleName('Runs on: Claude Code · haiku · Medium. Open to change'))
    expect(chip()).toHaveAttribute('title', 'Run by Claude Code (this computer) · provider claude-code · model Claude Haiku (latest) (haiku) · thinking Medium')
  })

  it('the bundled runtime reads "provider · model · level"; its name is only in the details', async () => {
    await setup(makeAgent({ model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, runtime: 'pi', thinkingLevel: 'high' }))
    await waitFor(() => expect(chip()).toHaveAccessibleName('Runs on: anthropic · claude-sonnet-5-5 · High. Open to change'))
    expect(chip()).toHaveAttribute('title', 'Run by Pi (built in) · provider anthropic · model Claude Sonnet 5.5 (claude-sonnet-5-5) · thinking High')
  })

  it('choosing a model in the popover saves at once, with the level the new model has, and says it applies from the next message', async () => {
    const { user, bodies } = await setup(makeAgent({ model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, runtime: 'pi', thinkingLevel: 'high' }))
    await user.click(chip())
    const popover = await screen.findByRole('dialog', { name: 'Runs on' })
    expect(within(popover).getByText('A choice is saved at once and applies from the next message')).toBeInTheDocument()
    // The search box takes focus, so typing filters right away.
    await waitFor(() => expect(within(popover).getByRole('combobox')).toHaveFocus())
    await chooseModel(user, popover, 'haiku', { runtime: 'Claude Code' })
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'haiku' }, thinkingLevel: 'high' }]))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Runs on' })).toBeNull())
    expect(await screen.findByText('Changed; applies from the next message')).toBeInTheDocument()
    await waitFor(() => expect(chip()).toHaveAccessibleName('Runs on: Claude Code · haiku · High. Open to change'))
  })

  it('a level chosen while a model change is still saving shows both, and both are sent', async () => {
    let release = () => {}
    const held = new Promise<void>((resolve) => { release = resolve })
    const agent = makeAgent({ model: { provider: 'claude-code', modelId: 'haiku' }, runtime: 'claude-code', thinkingLevel: null })
    let current = agent
    const { user, bodies } = await setup(agent)
    // The server applies each change as it arrives; the answer to the first is held back until after the second's, so the answers come
    // back out of order.
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
    await user.click(chip())
    const popover = await screen.findByRole('dialog', { name: 'Runs on' })
    await waitFor(() => expect(within(popover).getByRole('radio', { name: 'Max' })).toBeEnabled())
    await user.click(within(popover).getByRole('treeitem', { name: 'Claude Opus (latest) (opus)' }))
    await chooseThinking(user, popover, 'Max')
    expect(chip()).toHaveAccessibleName('Runs on: Claude Code · opus · Max. Open to change')
    release()
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'opus' }, thinkingLevel: null }, { thinkingLevel: 'max' }]))
    await waitFor(() => expect(chip()).toHaveAccessibleName('Runs on: Claude Code · opus · Max. Open to change'))
  })

  it('a thinking level is saved on its own and the popover stays open', async () => {
    const { user, bodies } = await setup(makeAgent({ model: { provider: 'claude-code', modelId: 'haiku' }, runtime: 'claude-code', thinkingLevel: null }))
    await user.click(chip())
    const popover = await findRunnerPicker(await screen.findByRole('dialog', { name: 'Runs on' }))
    await waitFor(() => expect(within(popover).getByRole('radio', { name: 'Max' })).toBeEnabled())
    await chooseThinking(user, popover, 'Max')
    await waitFor(() => expect(bodies).toEqual([{ thinkingLevel: 'max' }]))
    expect(screen.getByRole('dialog', { name: 'Runs on' })).toBeInTheDocument()
  })

  it('leaving openai-codex also clears its speed', async () => {
    const { user, bodies } = await setup(makeAgent({ model: { provider: 'openai-codex', modelId: 'gpt-a' }, runtime: 'pi', thinkingLevel: null, serviceTier: 'priority' }))
    await user.click(chip())
    const popover = await screen.findByRole('dialog', { name: 'Runs on' })
    await chooseModel(user, popover, 'claude-haiku-5-5', { provider: 'anthropic' })
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'anthropic', modelId: 'claude-haiku-5-5' }, thinkingLevel: null, serviceTier: null }]))
  })

  it('when the server refuses a change while replying, says so in the popover and keeps it open', async () => {
    const { user } = await setup(makeAgent({ model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, runtime: 'pi' }), () =>
      HttpResponse.json(errorBody('agent_not_idle', 'The Agent is replying'), { status: 409 }),
    )
    await user.click(chip())
    const popover = await screen.findByRole('dialog', { name: 'Runs on' })
    await chooseModel(user, popover, 'haiku', { runtime: 'Claude Code' })
    expect(await within(popover).findByRole('alert')).toHaveTextContent('The Agent is replying. Switch after this reply finishes.')
    expect(screen.getByRole('dialog', { name: 'Runs on' })).toBeInTheDocument()
  })
})
