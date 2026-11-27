// The config page's sub-tabs and sections: every section keeps its legacy #config-<key> anchor so external links
// (such as StatsPage -> #config-prompt) activate the owning sub-tab and scroll/focus the target section.
export const CONFIG_TABS = ['general', 'workspace', 'tools', 'danger'] as const
export type ConfigTab = (typeof CONFIG_TABS)[number]

export const SECTION_KEYS = [
  'basic',
  'model-credential',
  'permissions',
  'prompt',
  'work',
  'local-dirs',
  'skills',
  'clis',
  'plugins',
  'danger',
] as const
export type SectionKey = (typeof SECTION_KEYS)[number]

export const sectionId = (key: SectionKey) => `config-${key}`
export const sectionTitleId = (key: SectionKey) => `config-${key}-title`
export const tabPanelId = (tab: ConfigTab) => `config-panel-${tab}`

export const TAB_SECTIONS: Record<ConfigTab, readonly SectionKey[]> = {
  general: ['basic', 'model-credential', 'permissions'],
  workspace: ['prompt', 'work', 'local-dirs'],
  tools: ['skills', 'clis', 'plugins'],
  danger: ['danger'],
}

export const SECTION_TAB: Record<SectionKey, ConfigTab> = {
  basic: 'general',
  'model-credential': 'general',
  permissions: 'general',
  prompt: 'workspace',
  work: 'workspace',
  'local-dirs': 'workspace',
  skills: 'tools',
  clis: 'tools',
  plugins: 'tools',
  danger: 'danger',
}

// Legacy anchor aliases preserved for external deep links.
const HASH_ALIASES: Record<string, SectionKey> = {
  'config-model-account': 'model-credential',
}

export function resolveConfigHash(hash: string): { tab: ConfigTab; section: SectionKey } | null {
  const id = hash.startsWith('#') ? hash.slice(1) : hash
  if (!id) return null
  const alias = HASH_ALIASES[id]
  if (alias) return { tab: SECTION_TAB[alias], section: alias }
  const section = SECTION_KEYS.find((key) => sectionId(key) === id)
  if (!section) return null
  return { tab: SECTION_TAB[section], section }
}

export function parseConfigTab(value: string | null): ConfigTab {
  return CONFIG_TABS.includes(value as ConfigTab) ? (value as ConfigTab) : 'general'
}
