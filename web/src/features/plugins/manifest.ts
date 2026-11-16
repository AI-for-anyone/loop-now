import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'

type Manifest = Schemas['PluginManifest']

// Whether the manifest has an Agent side: Pi extensions, Skills, MCP servers or a CLI folder; if it has any of these, it must be granted
// per Agent.
export const hasAgentHalf = (manifest: Manifest) => {
  const c = manifest.components
  return c.agentExtensions > 0 || c.skills.length > 0 || c.mcpServers.length > 0 || c.bin.length > 0
}

// The row of small tags on a card: what each side contains, how many events it subscribes to, how many permissions it asks for.
export function componentChips(manifest: Manifest): string[] {
  const c = manifest.components, chips = t.plugins.chips
  return [
    c.platform ? chips.platform : null,
    hasAgentHalf(manifest) ? chips.agent : null,
    c.mcpServers.length ? chips.mcp(c.mcpServers.length) : null,
    c.skills.length ? chips.skills(c.skills.length) : null,
    c.bin.length ? chips.bin(c.bin.length) : null,
    ...c.channels.map((channel) => chips.channel(localizedText(channel.label))),
    c.channels.some((channel) => channel.manageIdentities) ? chips.manageIdentities : null,
    ...manifest.accounts.map((account) => chips.account(localizedText(account.label))),
    c.http ? chips.http : null,
    c.schedules.length ? chips.schedule : null,
    manifest.events.length ? chips.events(manifest.events.length) : null,
    manifest.permissions.length ? chips.permissions(manifest.permissions.length) : null,
  ].filter((chip): chip is string => chip !== null)
}
