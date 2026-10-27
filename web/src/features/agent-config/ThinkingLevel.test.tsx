import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { renderConfig } from '@/test/config'
import { chooseModel } from '@/test/model-picker'

const catalog: Schemas['Model'][] = [
  { id: 'claude-sonnet-5-5', name: 'Sonnet', reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['off', 'low', 'medium', 'high'], defaultThinkingLevel: 'medium' },
  { id: 'claude-haiku-5-5', name: 'Haiku', reasoning: true, contextWindow: 100, maxTokens: 100, input: ['text'], thinkingLevels: ['off', 'low'], defaultThinkingLevel: 'low' },
]
const modelHandler = http.get(apiUrl('/model-providers/anthropic/models'), () => HttpResponse.json({ providerId: 'anthropic', models: catalog }))
const basic = () => screen.getByRole('region', { name: '基本信息' })
const picker = () => within(basic()).getByLabelText('思考等级')
const save = () => within(basic()).getByRole('button', { name: '保存' })

async function setup(thinkingLevel: Schemas['ThinkingLevel'] | null = null, fail = false) {
  let current = makeAgent({ thinkingLevel })
  const agent = current
  const writes: Schemas['AgentUpdate'][] = []
  const view = await renderConfig({ agent, handlers: [modelHandler,
    http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
    http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
      const body = await request.json() as Schemas['AgentUpdate']; writes.push(body)
      if (fail) return HttpResponse.json(errorBody('validation_failed', '不支持的等级', { fields: [{ field: 'thinkingLevel', message: '当前模型不支持该等级' }] }), { status: 422 })
      current = { ...current, ...body }; return HttpResponse.json(current)
    }),
  ] })
  await waitFor(() => expect(picker()).toBeEnabled())
  return { ...view, writes, changeServer: (level: Schemas['ThinkingLevel']) => { current = { ...current, thinkingLevel: level } } }
}

it('只列模型支持等级及实际默认值，单独保存等级，不夹带模型或名称', async () => {
  const { user, writes } = await setup()
  expect(within(picker()).getAllByRole('option').map(option => option.getAttribute('value'))).toEqual(['', 'off', 'low', 'medium', 'high'])
  expect(picker()).toHaveDisplayValue('默认（中）')
  await user.selectOptions(picker(), 'high'); await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ thinkingLevel: 'high' }]))
  await waitFor(() => expect(save()).toBeDisabled())
})

it('恢复默认明确发送null，后续只改名称不重复发送等级', async () => {
  const { user, writes } = await setup('high')
  await user.selectOptions(picker(), ''); await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ thinkingLevel: null }]))
  await waitFor(() => expect(save()).toBeDisabled())
  await user.type(within(basic()).getByLabelText('显示名称'), '2'); await user.click(save())
  await waitFor(() => expect(writes[1]).toEqual({ name: '开发助手2' }))
})

it('切换模型恢复默认，避免将旧模型的等级带入新模型', async () => {
  const { user, writes } = await setup('high')
  await chooseModel(user, basic(), 'claude-haiku-5-5')
  expect(picker()).toHaveDisplayValue('默认（低）')
  expect(within(picker()).queryByRole('option', { name: '高' })).toBeNull()
  await user.click(save())
  await waitFor(() => expect(writes).toEqual([{ model: { provider: 'anthropic', modelId: 'claude-haiku-5-5' }, thinkingLevel: null }]))
})

it('等级校验失败在字段旁提示并保留草稿', async () => {
  const { user, writes } = await setup(null, true)
  await user.selectOptions(picker(), 'high'); await user.click(save())
  expect(await within(basic()).findByText('当前模型不支持该等级')).toBeInTheDocument()
  expect(picker()).toHaveValue('high'); expect(save()).toBeEnabled(); expect(writes).toHaveLength(1)
})

it('刷新跟随未编辑等级，已编辑等级保留并提示并发更新', async () => {
  const { user, client, agent, changeServer, writes } = await setup()
  changeServer('low'); await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(picker()).toHaveValue('low')); expect(save()).toBeDisabled()
  await user.selectOptions(picker(), 'high'); changeServer('medium')
  await client.invalidateQueries({ queryKey: keys.agent(agent.id) })
  await waitFor(() => expect(within(basic()).getByText(/服务端的配置已更新/)).toBeInTheDocument())
  expect(picker()).toHaveValue('high')
  await user.click(save()); await waitFor(() => expect(writes).toEqual([{ thinkingLevel: 'high' }]))
})
