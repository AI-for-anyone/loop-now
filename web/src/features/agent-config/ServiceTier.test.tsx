import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { providers, renderConfig } from '@/test/config'
import { chooseModel } from '@/test/model-picker'
import { thinkingChoice, thinkingPicker } from '@/test/thinking'

const catalog: Schemas['Model'][] = ['gpt-a', 'gpt-b'].map(id => ({ id, name: id, reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['low', 'medium', 'high'], defaultThinkingLevel: 'medium' }))
const basic = () => screen.getByRole('region', { name: 'Basics' })
const picker = () => within(basic()).getByLabelText('Codex speed')
const save = () => within(basic()).getByRole('button', { name: 'Save' })
async function setup(tier?: Schemas['CodexServiceTier'] | null, fail = false) {
  let current = makeAgent({ model: { provider: 'openai-codex', modelId: 'gpt-a' }, serviceTier: tier, thinkingLevel: 'high' })
  const agent = current, writes: Schemas['AgentUpdate'][] = []
  const view = await renderConfig({ agent, handlers: [
    http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, { id: 'openai-codex', source: 'builtin', modelCount: 2, apiKeyEnvVars: [], authMethods: ['oauth'] }] })),
    http.get(apiUrl('/model-providers/openai-codex/models'), () => HttpResponse.json({ providerId: 'openai-codex', models: catalog })),
    http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
    http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
      const body = await request.json() as Schemas['AgentUpdate']; writes.push(body)
      if (fail) return HttpResponse.json(errorBody('validation_failed', 'serviceTier: This speed is not available for this account', { fields: [{ field: 'serviceTier', message: 'This speed is not available for this account' }] }), { status: 422 })
      current = { ...current, ...body }; return HttpResponse.json(current)
    }),
  ] })
  await waitFor(() => expect(within(thinkingPicker(basic())).getAllByRole('radio')[0]).toBeEnabled())
  return { ...view, writes, changeServer: (tier: Schemas['CodexServiceTier']) => { current = { ...current, serviceTier: tier } } }
}
it('an older response without a tier means Standard and only three tiers are listed; speed and thinking level are independent, speed is saved on its own', async () => {
  const { user, writes } = await setup()
  expect(picker()).toHaveDisplayValue('Standard')
  expect(within(picker()).getAllByRole('option').map(option => option.textContent)).toEqual(['Standard', 'Fast', 'Ultrafast'])
  await user.selectOptions(picker(), 'priority')
  expect(thinkingChoice(basic())).toBe('High')
  await user.click(save()); await waitFor(() => expect(writes).toEqual([{ serviceTier: 'priority' }]))
  await waitFor(() => expect(save()).toBeDisabled())
})
it('going back to Standard sends null, a later name-only change does not carry the speed; an explicit default also shows Standard', async () => {
  const { user, writes, client, agent, changeServer } = await setup('ultrafast')
  await user.selectOptions(picker(), 'default'); await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ serviceTier: null }]))
  await waitFor(() => expect(save()).toBeDisabled())
  changeServer('default'); await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(picker()).toHaveDisplayValue('Standard'))
  expect(save()).toBeDisabled()
  await user.type(within(basic()).getByLabelText('Display name'), '2'); await user.click(save())
  await waitFor(() => expect(writes[1]).toEqual({ name: 'dev-helper2' }))
})
it('switching to another provider hides and clears the speed while keeping the name draft; the Prompt is not touched', async () => {
  const { user, writes } = await setup('priority')
  await user.type(within(basic()).getByLabelText('Display name'), '2')
  await user.selectOptions(within(basic()).getByLabelText('Model provider'), 'anthropic')
  await chooseModel(user, basic(), 'claude-sonnet-5-5')
  expect(within(basic()).queryByLabelText('Codex speed')).toBeNull()
  expect(within(basic()).getByLabelText('Display name')).toHaveValue('dev-helper2')
  await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ name: 'dev-helper2', model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, thinkingLevel: null, serviceTier: null }]))
})
it('switching models within the same provider keeps the speed; it is not tied to the thinking default', async () => {
  const { user, writes } = await setup('ultrafast')
  await chooseModel(user, basic(), 'gpt-b')
  expect(picker()).toHaveDisplayValue('Ultrafast')
  await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ model: { provider: 'openai-codex', modelId: 'gpt-b' }, thinkingLevel: null }]))
})
it('a 422 field error keeps the speed and other drafts and does not roll back on its own', async () => {
  const { user, writes } = await setup(null, true)
  await user.type(within(basic()).getByLabelText('Display name'), '2')
  await user.selectOptions(picker(), 'ultrafast'); await user.click(save())
  expect(await within(basic()).findByText('This speed is not available for this account')).toBeInTheDocument()
  expect(picker()).toHaveValue('ultrafast')
  expect(within(basic()).getByLabelText('Display name')).toHaveValue('dev-helper2')
  expect(save()).toBeEnabled(); expect(writes).toHaveLength(1)
})
it('a server refresh updates an untouched speed; an edited draft is kept and a concurrent change is reported', async () => {
  const { user, client, agent, changeServer } = await setup()
  changeServer('priority'); await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(picker()).toHaveValue('priority'))
  await user.selectOptions(picker(), 'ultrafast')
  changeServer('default'); await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(within(basic()).getByText(/The server's config was updated/)).toBeInTheDocument())
  expect(picker()).toHaveValue('ultrafast'); expect(save()).toBeEnabled()
})
