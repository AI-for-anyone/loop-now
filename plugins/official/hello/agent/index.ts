// Agent 半边：标准的 Pi 扩展，由已授权 Agent 的 Pi 进程加载（plugins/SPEC.md §2.1）。
// 平台把生效的配置（不含 secret 字段）放在环境变量 LOOP_NOW_PLUGIN_HELLO_CONFIG 里。

import { defineTool, type ExtensionAPI } from '@earendil-works/pi-coding-agent'
import { Type } from 'typebox'

function greeting(): string {
  try {
    const config = JSON.parse(process.env.LOOP_NOW_PLUGIN_HELLO_CONFIG ?? '{}') as { greeting?: unknown }
    return typeof config.greeting === 'string' && config.greeting !== '' ? config.greeting : 'Hello'
  } catch {
    return 'Hello'
  }
}

const helloTool = defineTool({
  name: 'hello',
  label: 'Hello',
  description: '向某人问好。用于演示 Loop Now 插件的 Agent 半边。',
  parameters: Type.Object({
    name: Type.String({ description: '要问候的人' }),
  }),
  async execute(_toolCallId, params) {
    const text = `${greeting()}, ${params.name}!`
    return { content: [{ type: 'text', text }], details: { greeted: params.name } }
  },
})

export default function (pi: ExtensionAPI) {
  pi.registerTool(helloTool)
}
