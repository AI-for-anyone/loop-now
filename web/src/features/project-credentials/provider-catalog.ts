import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'

// The front end's small table of model providers: each entry is grouped by use and holds one provider id, or several region variants of
// the same provider (switched after selection; the account still stores the concrete id).
// Display names, search aliases and variant names live in the copy table under credentials.providerPicker.entries. Ids in the API
// catalog that aren't in this table fall into "其他" (Other)
// and show the bare id, so providers newly added to the Pi catalog aren't lost; ids in this table but not in the catalog are not shown.
// Plugin-registered providers (source is plugin)
// skip this table and form their own "插件提供" (From plugins) group, showing the name the extension gives (untrusted text, shown as is)
// and the plugin that registers it.
export const providerGroups = ['vendor', 'domestic', 'gateway', 'cloud', 'inference', 'plugin', 'other'] as const
export type ProviderGroup = (typeof providerGroups)[number]
type EntryKey = keyof typeof t.credentials.providerPicker.entries

// The recommended ones go in the chips at the top of the picker. color is the background of the initial-letter block.
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

// The entry each id in the table belongs to.
const byId = new Map(providerCatalog.flatMap(entry => entry.ids.map(id => [id, entry] as const)))

export type ProviderEntry = {
  key: string
  group: ProviderGroup
  name: string
  color: string
  recommended: boolean
  // The ids actually present in the catalog, in table order; when there are several, label is the variant name, used for switching in the
  // selection bar.
  options: { id: string; label: string; provider: Schemas['ModelProvider'] }[]
  authMethods: Schemas['ModelAuthMethod'][]
  // Plugin provider: the display name of the plugin that registers it; null otherwise.
  plugin: string | null
  // The variants are different regions or plans for the same set of models, so take the largest count rather than adding them up.
  modelCount: number
  search: string
}

const copyOf = (key: EntryKey) => t.credentials.providerPicker.entries[key] as { name: string; alias: string; variants?: readonly string[] }

// Display name where only the id is known (account lists etc.): use the display name if it is in the table, otherwise the bare id. A
// plugin provider uses the name the extension gives (when its catalog entry is passed in).
export function providerName(id: string, provider?: Schemas['ModelProvider']): string {
  if (provider?.source === 'plugin') return provider.name || id
  const known = byId.get(id)
  return known ? copyOf(known.key).name : id
}

// The name of the plugin that registers a plugin provider: the display name from the plugin manifest (in the UI language), or the plugin
// id if there is none.
export const pluginName = (provider: Schemas['ModelProvider']) =>
  provider.pluginDisplayName === null ? provider.pluginId ?? '' : localizedText(provider.pluginDisplayName) || provider.pluginId || ''

// Turn the provider catalog from the API into picker entries: groups in providerGroups order, table order within a group, and unknown ids
// at the end of "其他" (Other).
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
    const plugin = pluginName(provider)
    entries.push(entry(`plugin:${provider.id}`, 'plugin', provider.name || provider.id, unknownColor, false, [{ id: provider.id, label: provider.id, provider }], `${plugin} ${provider.pluginId ?? ''}`, plugin))
  }
  return providerGroups.flatMap(group => entries.filter(item => item.group === group))
}

function entry(key: string, group: ProviderGroup, name: string, color: string, recommended: boolean, options: ProviderEntry['options'], alias: string, plugin: string | null = null): ProviderEntry {
  const authMethods = [...new Set(options.flatMap(option => option.provider.authMethods))]
  const modelCount = Math.max(...options.map(option => option.provider.modelCount))
  return { key, group, name, color, recommended, options, authMethods, plugin, modelCount, search: [name, ...options.map(option => option.id), alias].join(' ').toLowerCase() }
}

// Filter as you type: every word must appear in the display name, an id or the aliases. When not searching, hide entries with no
// available models.
export function matchesProvider(entry: ProviderEntry, query: string): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  if (!words.length) return entry.modelCount > 0
  return words.every(word => entry.search.includes(word))
}

// The letter inside the initial-letter block.
export const providerInitial = (name: string) => name.trim().charAt(0).toUpperCase()
