// Agent side: a standard Pi extension, loaded by the Pi process of granted Agents (plugins/SPEC.md §2.1).
// The platform puts the effective config (without secret fields) in the environment variable LOOP_NOW_PLUGIN_HELLO_CONFIG.

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
  description: 'Say hello to someone. Demonstrates the Agent side of a Loop Now plugin.',
  parameters: Type.Object({
    name: Type.String({ description: 'The person to greet' }),
  }),
  async execute(_toolCallId, params) {
    const text = `${greeting()}, ${params.name}!`
    return { content: [{ type: 'text', text }], details: { greeted: params.name } }
  },
})

export default function (pi: ExtensionAPI) {
  pi.registerTool(helloTool)
}
