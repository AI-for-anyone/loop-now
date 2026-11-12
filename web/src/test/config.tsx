import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { t } from '@/i18n'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { agentsUrl, apiUrl, context, defaultAgentPath, makeAgent, makeStatus, platformAgentsUrl, toListItem } from './api'
import { emptySelections } from './project-credentials'
import { renderRoutes } from './render'
import { server } from './server'

export const providers: Schemas['ModelProviderList'] = {
  piVersion: '1.1.0',
  catalogGeneratedAt: null,
  providers: [
    { id: 'anthropic', name: null, pluginId: null, pluginDisplayName: null, unavailableReason: null, source: 'builtin', runtime: 'pi', apiKeyEnvVars: ['ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_OAUTH_TOKEN', 'ANTHROPIC_API_KEY'], modelCount: 2, authMethods: ['api_key'] },
    { id: 'openai', name: null, pluginId: null, pluginDisplayName: null, unavailableReason: null, source: 'builtin', runtime: 'pi', apiKeyEnvVars: ['OPENAI_API_KEY'], modelCount: 1, authMethods: ['api_key'] },
  ],
}

const model = (id: string, name: string): Schemas['Model'] => ({ id, name, reasoning: false, contextWindow: 1, maxTokens: 1, input: ['text'] })

// 插件的 Pi 扩展注册的提供方：只有 OAuth；unavailableReason 只在带 agentId 读目录时可能非空。
export function pluginProvider(id: string, overrides: Partial<Schemas['ModelProvider']> = {}): Schemas['ModelProvider'] {
  return { id, name: 'Grok CLI', source: 'plugin', runtime: 'pi', pluginId: 'grok', pluginDisplayName: { 'zh-CN': 'Grok 订阅', 'en-US': 'Grok subscription' }, unavailableReason: null, apiKeyEnvVars: [], modelCount: 2, authMethods: ['oauth'], ...overrides }
}

// 本机的 Claude Code（契约 ModelProvider source=local_cli）：用它自己在本机的登录，没有账号；模型是别名，思考等级 low 到 max，没有 off。
export const claudeCodeProvider: Schemas['ModelProvider'] = { id: 'claude-code', name: 'Claude Code', source: 'local_cli', runtime: 'claude-code', pluginId: null, pluginDisplayName: null, unavailableReason: null, apiKeyEnvVars: [], modelCount: 2, authMethods: [] }
const claudeCodeModel = (id: string, name: string): Schemas['Model'] => ({ ...model(id, name), reasoning: true, thinkingLevels: ['low', 'medium', 'high', 'xhigh', 'max'], defaultThinkingLevel: 'medium' })
export const withClaudeCode: Schemas['ModelProviderList'] = { ...providers, providers: [...providers.providers, claudeCodeProvider] }

export const catalogHandlers = [
  http.get(apiUrl('/model-providers/claude-code/models'), () =>
    HttpResponse.json({ providerId: 'claude-code', models: [claudeCodeModel('sonnet', 'Claude Sonnet (latest)'), claudeCodeModel('opus', 'Claude Opus (latest)')] }),
  ),
  http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })),
  http.get(apiUrl('/model-providers'), () => HttpResponse.json(providers)),
  http.get(apiUrl('/model-providers/anthropic/models'), () =>
    HttpResponse.json({ providerId: 'anthropic', models: [model('claude-sonnet-5-5', 'Claude Sonnet 5.5'), model('claude-haiku-5-5', 'Claude Haiku 5.5')] }),
  ),
  http.get(apiUrl('/model-providers/openai/models'), () => HttpResponse.json({ providerId: 'openai', models: [model('gpt-x', 'GPT X')] })),
]

export const ghTemplate: Schemas['CliTemplate'] = {
  name: 'gh',
  version: '2.100.0',
  fields: [{ name: 'token', label: '访问令牌', type: 'secret', required: true }],
  injection: 'env',
  install: { status: 'installed' },
  warnings: [],
}

export const larkTemplate: Schemas['CliTemplate'] = {
  name: 'lark-cli',
  version: '1.0.94',
  fields: [
    { name: 'appId', label: 'App ID', type: 'string', required: true },
    { name: 'appSecret', label: 'App Secret', type: 'secret', required: true },
    { name: 'brand', label: '品牌', type: 'enum', options: ['feishu', 'lark'], default: 'feishu', required: true },
  ],
  injection: 'files',
  install: { status: 'not_installed' },
  warnings: [{ code: 'host_keychain_risk', message: 'keychain' }],
}

export function credentialMeta(overrides: Partial<Schemas['CredentialMeta']> = {}): Schemas['CredentialMeta'] {
  return { status: 'set', updatedAt: '2026-10-12T08:00:00Z', fieldNames: [], envNames: [], filePaths: [], warnings: [], ...overrides }
}

interface ConfigSetup {
  agent?: Schemas['Agent']
  status?: Partial<Schemas['AgentStatus']>
  handlers?: HttpHandler[]
  agentSkills?: Schemas['AgentSkillSummary'][]
  library?: Schemas['SkillTemplateSummary'][]
  templates?: Schemas['CliTemplate'][]
  limits?: Partial<Schemas['Limits']>
}

export async function renderConfig(setup: ConfigSetup = {}) {
  const agent = setup.agent ?? makeAgent()
  server.use(
    ...(setup.handlers ?? []),
    http.get(apiUrl('/context'), () => HttpResponse.json({ ...context, limits: { ...context.limits, ...setup.limits } })),
    http.get(agentsUrl, () => HttpResponse.json({ agents: agent.scope === 'project' ? [toListItem(agent)] : [] })),
    http.get(platformAgentsUrl, () => HttpResponse.json({ agents: agent.scope === 'platform' ? [toListItem(agent)] : [] })),
    http.get(apiUrl(`/agents/${agent.id}/credential-selections`), () => HttpResponse.json(emptySelections(agent))),
    http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(agent)),
    http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: agent.modelCredential, operation: null })),
    http.get(apiUrl(`/agents/${agent.id}/status`), () => HttpResponse.json(makeStatus(agent.id, setup.status))),
    http.get(apiUrl(`/agents/${agent.id}/skills`), () => HttpResponse.json({ skills: setup.agentSkills ?? [] })),
    http.get(apiUrl(`/agents/${agent.id}/messages`), () => HttpResponse.json({ messages: [], hasMore: false })),
    http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: setup.library ?? [] })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: setup.templates ?? [ghTemplate, larkTemplate] })),
    ...catalogHandlers,
  )
  const user = userEvent.setup()
  const result = renderRoutes(routes, agent.scope === 'platform' ? `/platform/agents/${agent.id}/config` : defaultAgentPath(agent.id, 'config'))
  await screen.findByRole('region', { name: t.config.basic.title })
  return { ...result, agent, user }
}
