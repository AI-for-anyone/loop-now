import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { providers, renderConfig } from '@/test/config'
import { chooseModel } from '@/test/model-picker'

const catalog: Schemas['Model'][] = ['gpt-a', 'gpt-b'].map(id => ({ id, name: id, reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['low', 'medium', 'high'], defaultThinkingLevel: 'medium' }))
const basic = () => screen.getByRole('region', { name: '基本信息' })
const picker = () => within(basic()).getByLabelText('Codex 速度')
const save = () => within(basic()).getByRole('button', { name: '保存' })
async function setup(tier?: Schemas['CodexServiceTier'] | null, fail = false) {
  let current = makeAgent({ model: { provider: 'openai-codex', modelId: 'gpt-a' }, serviceTier: tier, thinkingLevel: 'high' })
  const agent = current, writes: Schemas['AgentUpdate'][] = []
  const view = await renderConfig({ agent, handlers: [
    http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, { id: 'openai-codex', modelCount: 2, apiKeyEnvVars: [], authMethods: ['oauth'] }] })),
    http.get(apiUrl('/model-providers/openai-codex/models'), () => HttpResponse.json({ providerId: 'openai-codex', models: catalog })),
    http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
    http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
      const body = await request.json() as Schemas['AgentUpdate']; writes.push(body)
      if (fail) return HttpResponse.json(errorBody('validation_failed', '速度不可用', { fields: [{ field: 'serviceTier', message: '此账号暂不可用该速度' }] }), { status: 422 })
      current = { ...current, ...body }; return HttpResponse.json(current)
    }),
  ] })
  await waitFor(() => expect(within(basic()).getByLabelText('思考等级')).toBeEnabled())
  return { ...view, writes, changeServer: (tier: Schemas['CodexServiceTier']) => { current = { ...current, serviceTier: tier } } }
}
it('旧响应缺省为标准，仅列三档；速度和思考等级独立，单独保存速度', async () => {
  const { user, writes } = await setup()
  expect(picker()).toHaveDisplayValue('标准')
  expect(within(picker()).getAllByRole('option').map(option => option.textContent)).toEqual(['标准', 'Fast', 'Ultrafast'])
  await user.selectOptions(picker(), 'priority')
  expect(within(basic()).getByLabelText('思考等级')).toHaveValue('high')
  await user.click(save()); await waitFor(() => expect(writes).toEqual([{ serviceTier: 'priority' }]))
  await waitFor(() => expect(save()).toBeDisabled())
})
it('恢复标准发null，后续仅改名称不夹带速度；显式default也显示标准', async () => {
  const { user, writes, client, agent, changeServer } = await setup('ultrafast')
  await user.selectOptions(picker(), 'default'); await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ serviceTier: null }]))
  await waitFor(() => expect(save()).toBeDisabled())
  changeServer('default'); await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(picker()).toHaveDisplayValue('标准'))
  expect(save()).toBeDisabled()
  await user.type(within(basic()).getByLabelText('显示名称'), '2'); await user.click(save())
  await waitFor(() => expect(writes[1]).toEqual({ name: '开发助手2' }))
})
it('切换其他provider隐藏并清空速度，同时保留名称草稿；不会动Prompt', async () => {
  const { user, writes } = await setup('priority')
  await user.type(within(basic()).getByLabelText('显示名称'), '2')
  await user.selectOptions(within(basic()).getByLabelText('模型提供方'), 'anthropic')
  await chooseModel(user, basic(), 'claude-sonnet-5-5')
  expect(within(basic()).queryByLabelText('Codex 速度')).toBeNull()
  expect(within(basic()).getByLabelText('显示名称')).toHaveValue('开发助手2')
  await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ name: '开发助手2', model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' }, thinkingLevel: null, serviceTier: null }]))
})
it('同provider切模型保留速度；不把它与思考默认绑定', async () => {
  const { user, writes } = await setup('ultrafast')
  await chooseModel(user, basic(), 'gpt-b')
  expect(picker()).toHaveDisplayValue('Ultrafast')
  await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ model: { provider: 'openai-codex', modelId: 'gpt-b' }, thinkingLevel: null }]))
})
it('422字段错误保留速度与其他草稿，不自动回退', async () => {
  const { user, writes } = await setup(null, true)
  await user.type(within(basic()).getByLabelText('显示名称'), '2')
  await user.selectOptions(picker(), 'ultrafast'); await user.click(save())
  expect(await within(basic()).findByText('此账号暂不可用该速度')).toBeInTheDocument()
  expect(picker()).toHaveValue('ultrafast')
  expect(within(basic()).getByLabelText('显示名称')).toHaveValue('开发助手2')
  expect(save()).toBeEnabled(); expect(writes).toHaveLength(1)
})
it('服务端刷新跟随未编辑速度，已编辑草稿保留并提示并发修改', async () => {
  const { user, client, agent, changeServer } = await setup()
  changeServer('priority'); await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(picker()).toHaveValue('priority'))
  await user.selectOptions(picker(), 'ultrafast')
  changeServer('default'); await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(within(basic()).getByText(/服务端的配置已更新/)).toBeInTheDocument())
  expect(picker()).toHaveValue('ultrafast'); expect(save()).toBeEnabled()
})
