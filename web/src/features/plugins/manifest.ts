import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

type Manifest = Schemas['PluginManifest']

// 清单里有没有 Agent 半边：Pi 扩展、Skill、MCP 服务器或 CLI 目录，有任一项就要按 Agent 授权。
export const hasAgentHalf = (manifest: Manifest) => {
  const c = manifest.components
  return c.agentExtensions > 0 || c.skills.length > 0 || c.mcpServers.length > 0 || c.bin.length > 0
}

// 卡片上的一行小标签：两半各有什么、订阅几个事件、要几项权限。
export function componentChips(manifest: Manifest): string[] {
  const c = manifest.components, chips = t.plugins.chips
  return [
    c.platform ? chips.platform : null,
    hasAgentHalf(manifest) ? chips.agent : null,
    c.mcpServers.length ? chips.mcp(c.mcpServers.length) : null,
    c.skills.length ? chips.skills(c.skills.length) : null,
    c.bin.length ? chips.bin(c.bin.length) : null,
    ...c.channels.map((channel) => chips.channel(channel.label)),
    c.channels.some((channel) => channel.manageIdentities) ? chips.manageIdentities : null,
    ...manifest.accounts.map((account) => chips.account(account.label)),
    c.http ? chips.http : null,
    c.schedules.length ? chips.schedule : null,
    manifest.events.length ? chips.events(manifest.events.length) : null,
    manifest.permissions.length ? chips.permissions(manifest.permissions.length) : null,
  ].filter((chip): chip is string => chip !== null)
}
