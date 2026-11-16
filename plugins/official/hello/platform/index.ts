// Platform side: runs in the plugin host. register only registers things;
// the host cancels these registrations automatically when the plugin is disabled.

import type { PluginAPI } from '@loop-now/plugin-sdk'

export default function register(api: PluginAPI) {
  api.on('run.ended', (event, ctx) => {
    const tokens = event.tokens ? `，用了 ${event.tokens.total} 个 token` : ''
    api.log.info(`Agent ${ctx.agentId ?? '（平台）'} 的一轮结束：${event.status}${tokens}`)
  })
}
