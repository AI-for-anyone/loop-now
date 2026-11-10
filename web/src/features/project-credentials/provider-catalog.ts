import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'

// 模型提供方的前端小表：每一条按用途分组，收进一个提供方 id，或者同一家的几个地区变体（选中后再切，写入账号的仍是具体 id）。
// 显示名、搜索别名、变体名在文案表的 credentials.providerPicker.entries 里。接口目录里不在这张表上的 id 落到"其他"，
// 直接显示 id，所以 Pi 目录新增提供方不会丢；这张表上有、目录里没有的 id 不显示。插件注册的提供方（source 为 plugin）
// 不查这张表，单独成"插件提供"一组，显示扩展给的名称（不可信文本，原样显示）和注册它的插件。
export const providerGroups = ['vendor', 'domestic', 'gateway', 'cloud', 'inference', 'plugin', 'other'] as const
export type ProviderGroup = (typeof providerGroups)[number]
type EntryKey = keyof typeof t.credentials.providerPicker.entries

// recommended 的几家放在选择器顶部的芯片里。color 是首字母色块的底色。
export const providerCatalog: { key: EntryKey; group: ProviderGroup; ids: string[]; color: string; recommended?: boolean }[] = [
  { key: 'codex', group: 'vendor', ids: ['openai-codex'], color: '#10a37f', recommended: true },
  { key: 'anthropic', group: 'vendor', ids: ['anthropic'], color: '#c96a3a', recommended: true },
  { key: 'openai', group: 'vendor', ids: ['openai'], color: '#1f8a6b', recommended: true },
  { key: 'gemini', group: 'vendor', ids: ['google'], color: '#4285f4', recommended: true },
  { key: 'xai', group: 'vendor', ids: ['xai'], color: '#222222' },
  { key: 'mistral', group: 'vendor', ids: ['mistral'], color: '#f0532d' },
  { key: 'meta', group: 'vendor', ids: ['meta'], color: '#0866ff' },
  { key: 'copilot', group: 'vendor', ids: ['github-copilot'], color: '#24292f' },
  { key: 'deepseek', group: 'domestic', ids: ['deepseek'], color: '#4d6bfe', recommended: true },
  { key: 'moonshot', group: 'domestic', ids: ['moonshotai', 'moonshotai-cn'], color: '#111111', recommended: true },
  { key: 'kimiCoding', group: 'domestic', ids: ['kimi-coding'], color: '#333333' },
  { key: 'qwen', group: 'domestic', ids: ['qwen-token-plan', 'qwen-token-plan-cn', 'qwen-token-plan-individual'], color: '#6f3ff5' },
  { key: 'zai', group: 'domestic', ids: ['zai', 'zai-coding-cn'], color: '#2d6cdf' },
  { key: 'minimax', group: 'domestic', ids: ['minimax', 'minimax-cn'], color: '#e0245e' },
  { key: 'xiaomi', group: 'domestic', ids: ['xiaomi', 'xiaomi-token-plan-cn', 'xiaomi-token-plan-sgp', 'xiaomi-token-plan-ams'], color: '#ff6900' },
  { key: 'ling', group: 'domestic', ids: ['ant-ling'], color: '#1677ff' },
  { key: 'openrouter', group: 'gateway', ids: ['openrouter'], color: '#6466e9' },
  { key: 'opencode', group: 'gateway', ids: ['opencode', 'opencode-go'], color: '#111111' },
  { key: 'vercel', group: 'gateway', ids: ['vercel-ai-gateway'], color: '#000000' },
  { key: 'cloudflareGateway', group: 'gateway', ids: ['cloudflare-ai-gateway'], color: '#f38020' },
  { key: 'huggingface', group: 'gateway', ids: ['huggingface'], color: '#ffbd2e' },
  { key: 'bedrock', group: 'cloud', ids: ['amazon-bedrock'], color: '#ff9900' },
  { key: 'azure', group: 'cloud', ids: ['azure'], color: '#0078d4' },
  { key: 'vertex', group: 'cloud', ids: ['google-vertex'], color: '#34a853' },
  { key: 'workersAi', group: 'cloud', ids: ['cloudflare-workers-ai'], color: '#f38020' },
  { key: 'groq', group: 'inference', ids: ['groq'], color: '#f55036' },
  { key: 'cerebras', group: 'inference', ids: ['cerebras'], color: '#1f4ed8' },
  { key: 'fireworks', group: 'inference', ids: ['fireworks'], color: '#7c3aed' },
  { key: 'together', group: 'inference', ids: ['together'], color: '#0ea5e9' },
  { key: 'baseten', group: 'inference', ids: ['baseten'], color: '#2563eb' },
  { key: 'nvidia', group: 'inference', ids: ['nvidia'], color: '#76b900' },
  { key: 'radius', group: 'other', ids: ['radius'], color: '#666666' },
  { key: 'typesafe', group: 'other', ids: ['typesafe'], color: '#999999' },
]
const unknownColor = '#666666'

// 表里每个 id 所在的条目。
const byId = new Map(providerCatalog.flatMap(entry => entry.ids.map(id => [id, entry] as const)))

export type ProviderEntry = {
  key: string
  group: ProviderGroup
  name: string
  color: string
  recommended: boolean
  // 目录里实际有的 id，按表里的顺序；有几个时 label 是变体名，用来在选中条里切换。
  options: { id: string; label: string; provider: Schemas['ModelProvider'] }[]
  authMethods: Schemas['ModelAuthMethod'][]
  // 插件提供方：注册它的插件的显示名；其余为 null。
  plugin: string | null
  // 各变体是同一批模型的不同区域或套餐，取最大的一个，不相加。
  modelCount: number
  search: string
}

const copyOf = (key: EntryKey) => t.credentials.providerPicker.entries[key] as { name: string; alias: string; variants?: readonly string[] }

// 账号列表等只有 id 时的显示名：表里有的用显示名，没有的直接用 id。插件提供方用扩展给的名称（给了目录里的这一项时）。
export function providerName(id: string, provider?: Schemas['ModelProvider']): string {
  if (provider?.source === 'plugin') return provider.name || id
  const known = byId.get(id)
  return known ? copyOf(known.key).name : id
}

// 把接口给的提供方目录收成选择器的条目：分组按 providerGroups 的顺序，组内按表里的顺序，未知 id 排在"其他"的最后。
export function providerEntries(providers: Schemas['ModelProvider'][]): ProviderEntry[] {
  const builtin = providers.filter(provider => provider.source !== 'plugin')
  const present = new Map(builtin.map(provider => [provider.id, provider]))
  const entries: ProviderEntry[] = []
  for (const item of providerCatalog) {
    const copy = copyOf(item.key)
    const options = item.ids.flatMap((id, index) => {
      const provider = present.get(id)
      return provider ? [{ id, label: copy.variants?.[index] ?? id, provider }] : []
    })
    if (options.length) entries.push(entry(item.key, item.group, copy.name, item.color, !!item.recommended, options, copy.alias))
  }
  for (const provider of builtin) {
    if (!byId.has(provider.id)) entries.push(entry(provider.id, 'other', provider.id, unknownColor, false, [{ id: provider.id, label: provider.id, provider }], ''))
  }
  for (const provider of providers) {
    if (provider.source !== 'plugin') continue
    const plugin = provider.pluginDisplayName === null ? provider.pluginId ?? '' : localizedText(provider.pluginDisplayName) || provider.pluginId || ''
    entries.push(entry(`plugin:${provider.id}`, 'plugin', provider.name || provider.id, unknownColor, false, [{ id: provider.id, label: provider.id, provider }], `${plugin} ${provider.pluginId ?? ''}`, plugin))
  }
  return providerGroups.flatMap(group => entries.filter(item => item.group === group))
}

function entry(key: string, group: ProviderGroup, name: string, color: string, recommended: boolean, options: ProviderEntry['options'], alias: string, plugin: string | null = null): ProviderEntry {
  const authMethods = [...new Set(options.flatMap(option => option.provider.authMethods))]
  const modelCount = Math.max(...options.map(option => option.provider.modelCount))
  return { key, group, name, color, recommended, options, authMethods, plugin, modelCount, search: [name, ...options.map(option => option.id), alias].join(' ').toLowerCase() }
}

// 输入即筛：每个词都要出现在显示名、id 或别名里。不搜索时隐藏没有可用模型的条目。
export function matchesProvider(entry: ProviderEntry, query: string): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return entry.modelCount > 0
  return words.every(word => entry.search.includes(word))
}

// 首字母色块里的字。
export const providerInitial = (name: string) => name.trim().charAt(0).toUpperCase()
