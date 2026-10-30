import type { Schemas } from '@/api/client'
import { t } from '@/i18n'

export type Participant = Schemas['InboxParticipant']

// 参与方的显示：名称、已删除标记与地址。
// 参与方已删除时，服务端的 name 为“已删除身份”或“已删除 Agent”，address 仍是原地址：
// 这时用原地址中的名称作标题，“已删除…”作标记；没有地址时只显示服务端的 name。
// 插件渠道来的（kind plugin，地址 plugin:<id>/<渠道>）：标题写成“插件 · 名称”，插件已卸载时标“已卸载”。
export function partyDisplay(party: Participant): { title: string; deleted: string | null; address: string | null } {
  if (party.kind === 'plugin') return { title: t.plugins.chat.sender(party.deleted ? (party.pluginId ?? party.name) : party.name), deleted: party.deleted ? t.plugins.chat.uninstalled : null, address: party.address }
  if (!party.deleted) return { title: party.name, deleted: null, address: party.address }
  if (!party.address) return { title: party.name, deleted: null, address: null }
  const title = party.address.startsWith('@') ? party.address.slice(1) : (party.address.split('/').at(-1) ?? party.address)
  return { title, deleted: party.name, address: party.address }
}
