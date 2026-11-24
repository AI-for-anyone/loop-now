import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'

type Provider = Schemas['ModelProvider']
export type Unavailable = 'create' | NonNullable<Provider['unavailableReason']>

// Why a plugin provider cannot be used by this Agent right now; when creating an Agent it always has to wait until after creation and a
// grant. plugins_disabled does not block saving.
export const unavailableOf = (provider: Provider, agentId: string | undefined): Unavailable | null =>
  provider.source !== 'plugin' ? null : agentId === undefined ? 'create' : provider.unavailableReason
export const blocks = (reason: Unavailable | null) => reason !== null && reason !== 'plugins_disabled'
export const pluginName = (provider: Provider) => (provider.pluginDisplayName === null ? provider.pluginId ?? '' : localizedText(provider.pluginDisplayName) || provider.pluginId || '')

// The chosen plugin provider cannot be used by this Agent right now: why, and where to fix it.
export function unavailableNote(provider: Provider, agentId: string | undefined): string | null {
  const reason = unavailableOf(provider, agentId), note = t.config.basic.pluginUnavailableNote
  if (reason === null || reason === 'create') return null
  if (reason === 'plugin_not_enabled' || reason === 'agent_not_granted') return note[reason](pluginName(provider))
  return note[reason]
}
