import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject } from '@/test/api'
import { projectAccount } from '@/test/project-credentials'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { ProjectAccounts } from '@/features/project-credentials/ProjectAccounts'

// The provider search matches the names and aliases of the UI language's copy table, so the Chinese names “智谱” (Zhipu) and “月之暗面” (Moonshot) are
// found in the Chinese UI. The English search is in src/features/project-credentials/ProviderPicker.test.tsx.
const provider = (id: string, modelCount: number, authMethods: Schemas['ModelAuthMethod'][] = ['api_key']): Schemas['ModelProvider'] => ({ id, name: null, pluginId: null, pluginDisplayName: null, unavailableReason: null, usable: null, source: 'builtin', runtime: 'pi', apiKeyEnvVars: [], modelCount, authMethods })
const catalog: Schemas['ModelProvider'][] = [
  provider('anthropic', 17), provider('deepseek', 2), provider('moonshotai', 4), provider('moonshotai-cn', 4), provider('no-login', 5, []),
  provider('openai-codex', 7, ['oauth']), provider('openrouter', 401), provider('typesafe', 0), provider('zai', 7), provider('zai-coding-cn', 4), provider('zz-new', 3),
  // The local Claude Code: it uses its own login on this machine and has no project account.
  { ...provider('claude-code', 4, []), name: 'Claude Code', source: 'local_cli', runtime: 'claude-code' },
]
const codexAccount = projectAccount({ id: '10000000-0000-7000-8000-000000000002', provider: 'openai-codex', authMethod: 'oauth', status: 'logged_in', name: 'Codex 账号' })

function start(accounts: Schemas['ProjectCredentialAccount'][] = [codexAccount], providers: Schemas['ModelProvider'][] = catalog) {
  const bodies: unknown[] = []
  server.use(
    http.get(apiUrl('/model-providers'), () => HttpResponse.json({ piVersion: '1.4.0', catalogGeneratedAt: null, providers })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
    http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
    http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: null, agents: [] })),
    http.post(apiUrl('/projects/:projectId/credential-accounts'), async ({ request }) => { bodies.push(await request.json()); return HttpResponse.json(projectAccount(), { status: 201 }) }),
  )
  renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="model" /></DraftNavigationGuard> }], '/')
  return { user: userEvent.setup(), bodies }
}

describe('Model provider picker', () => {
  it('filters as you type: matches the Chinese name, English name, id and aliases; providers without models appear when searching; with no match it says where to add a custom provider', async () => {
    const { user } = start()
    await user.click(screen.getByRole('button', { name: '添加模型账号 · 提供方' }))
    await screen.findByRole('option', { name: /^Anthropic/ })
    const search = screen.getByRole('searchbox', { name: '搜索提供方' })
    const results = async (query: string) => { await user.clear(search); if (query) await user.type(search, query); return screen.queryAllByRole('option').map(item => item.querySelector('.font-bold')?.textContent) }
    expect(await results('智谱')).toEqual(['智谱 Z.ai'])
    expect(await results('zhipu')).toEqual(['智谱 Z.ai'])
    expect(await results('kimi')).toEqual(['Moonshot Kimi'])
    expect(await results('月之暗面')).toEqual(['Moonshot Kimi'])
    expect(await results('moonshotai-cn')).toEqual(['Moonshot Kimi'])
    expect(await results('claude')).toEqual(['Anthropic'])
    expect(screen.queryByRole('group', { name: '常用提供方' })).toBeNull()
    expect(await results('typesafe')).toEqual(['typesafe'])
    expect(await results('没有这家')).toEqual([])
    expect(screen.getByRole('status')).toHaveTextContent('没有匹配的提供方。要加自定义 Provider，去全局设置的 models.json。')
    expect(await results('')).toContain('Anthropic')
    expect(screen.getByRole('group', { name: '常用提供方' })).toBeInTheDocument()
  })
})
