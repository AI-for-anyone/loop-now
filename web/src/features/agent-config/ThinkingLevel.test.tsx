import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { renderConfig } from '@/test/config'
import { chooseModel } from '@/test/model-picker'
import { chooseThinking, thinkingChoice, thinkingOptions, thinkingPicker } from '@/test/thinking'

const catalog: Schemas['Model'][] = [
  { id: 'claude-sonnet-5-5', name: 'Sonnet', reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['off', 'low', 'medium', 'high'], defaultThinkingLevel: 'medium' },
  { id: 'claude-haiku-5-5', name: 'Haiku', reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['off', 'low'], defaultThinkingLevel: 'low' },
]
const modelHandler = http.get(apiUrl('/model-providers/anthropic/models'), () => HttpResponse.json({ providerId: 'anthropic', models: catalog }))
const basic = () => screen.getByRole('region', { name: 'Basics' })
const picker = () => thinkingPicker(basic())
const save = () => within(basic()).getByRole('button', { name: 'Save' })

async function setup(thinkingLevel: Schemas['ThinkingLevel'] | null = null, fail = false) {
  let current = makeAgent({ thinkingLevel })
  const agent = current
  const writes: Schemas['AgentUpdate'][] = []
  const view = await renderConfig({ agent, handlers: [modelHandler,
    http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
    http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
      const body = await request.json() as Schemas['AgentUpdate']; writes.push(body)
      if (fail) return HttpResponse.json(errorBody('validation_failed', 'thinkingLevel: The current model does not support this level', { fields: [{ field: 'thinkingLevel', message: 'The current model does not support this level' }] }), { status: 422 })
      current = { ...current, ...body }; return HttpResponse.json(current)
    }),
  ] })
  await waitFor(() => expect(within(picker()).getAllByRole('radio')[0]).toBeEnabled())
  return { ...view, writes, changeServer: (level: Schemas['ThinkingLevel']) => { current = { ...current, thinkingLevel: level } } }
}

it('lists only the levels the model supports and the actual default; saves the level on its own without the model or name', async () => {
  const { user, writes } = await setup()
  expect(thinkingOptions(basic())).toEqual(['Default (Medium)', 'Off', 'Low', 'Medium', 'High'])
  expect(thinkingChoice(basic())).toBe('Default (Medium)')
  await chooseThinking(user, basic(), 'High'); await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ thinkingLevel: 'high' }]))
  await waitFor(() => expect(save()).toBeDisabled())
})

it('going back to the default sends null explicitly; a later name-only change does not send the level again', async () => {
  const { user, writes } = await setup('high')
  await chooseThinking(user, basic(), 'Default (Medium)'); await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ thinkingLevel: null }]))
  await waitFor(() => expect(save()).toBeDisabled())
  await user.type(within(basic()).getByLabelText('Display name'), '2'); await user.click(save())
  await waitFor(() => expect(writes[1]).toEqual({ name: 'dev-helper2' }))
})

it("switching models goes back to the default so the old model's level is not carried into the new model", async () => {
  const { user, writes } = await setup('high')
  await chooseModel(user, basic(), 'claude-haiku-5-5')
  expect(thinkingChoice(basic())).toBe('Default (Low)')
  expect(within(picker()).queryByRole('radio', { name: 'High' })).toBeNull()
  await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ model: { provider: 'anthropic', modelId: 'claude-haiku-5-5' }, thinkingLevel: null }]))
})

it('a level validation failure is shown next to the field and the draft is kept', async () => {
  const { user, writes } = await setup(null, true)
  await chooseThinking(user, basic(), 'High'); await user.click(save())
  expect(await within(basic()).findByText('The current model does not support this level')).toBeInTheDocument()
  expect(thinkingChoice(basic())).toBe('High'); expect(save()).toBeEnabled(); expect(writes).toHaveLength(1)
})

it('a refresh updates an untouched level; an edited level is kept and a concurrent update is reported', async () => {
  const { user, client, agent, changeServer, writes } = await setup()
  changeServer('low'); await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(thinkingChoice(basic())).toBe('Low')); expect(save()).toBeDisabled()
  await chooseThinking(user, basic(), 'High'); changeServer('medium')
  await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(within(basic()).getByText(/The server's config was updated/)).toBeInTheDocument())
  expect(thinkingChoice(basic())).toBe('High')
  await user.click(save()); await waitFor(() => expect(writes).toEqual([{ thinkingLevel: 'high' }]))
})
