// 平台半边：在插件宿主里运行。register 只做注册；宿主在插件停用时自动取消这些注册。

import type { PluginAPI } from '@loop-now/plugin-sdk'

export default function register(api: PluginAPI) {
  api.on('run.ended', (event, ctx) => {
    const tokens = event.tokens ? `，用了 ${event.tokens.total} 个 token` : ''
    api.log.info(`Agent ${ctx.agentId ?? '（平台）'} 的一轮结束：${event.status}${tokens}`)
  })
}
