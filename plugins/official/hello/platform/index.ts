// Platform side: runs in the plugin host. register only registers things;
// the host cancels these registrations automatically when the plugin is disabled.

import type { PluginAPI } from '@loop-now/plugin-sdk'

export default function register(api: PluginAPI) {
  api.on('run.ended', (event, ctx) => {
    const tokens = event.tokens ? `, ${event.tokens.total} tokens` : ''
    api.log.info(`Agent ${ctx.agentId ?? '(platform)'} finished a run: ${event.status}${tokens}`)
  })
}
