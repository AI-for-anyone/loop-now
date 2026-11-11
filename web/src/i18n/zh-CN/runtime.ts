import type { components } from '@/api/schema.gen'

type S = components['schemas']

// Agent 的运行时：随包的 Pi，或用户本机安装的 Claude Code（由模型提供方决定）。
export const runtime = {
  names: { pi: 'Pi', 'claude-code': 'Claude Code' } satisfies Record<S['AgentRuntime'], string>,
  // 侧栏与 Agent 头部的标记，只给 Claude Code 的 Agent；Pi 是默认，不标。
  badge: 'Claude Code',
  badgeTitle: '运行时：本机的 Claude Code',
  // 模型选择里的 Claude Code 提供方。
  providerOption: (name: string, count: number) => `${name}（本机，${count} 个模型）`,
  providerNote: '用本机安装的 Claude Code 和你在本机的登录，平台不保存登录信息。',
  // 配置页选了 Claude Code 时，和 Pi 的不同之处。
  differences: {
    title: '用 Claude Code 运行时',
    home: '用你本机的家目录和登录运行：它执行 gh、git 等命令时用的是你本人的登录。',
    commands: '不经确认直接执行命令，与 Pi 的 Agent 相同。',
    plugins: '只含 Pi 扩展的插件对它不起作用；插件的技能和 MCP 工具可以用。',
    piCommands: '没有 /pi: 命令。',
  },
  // 保存时运行时要变：先确认。
  switch: {
    title: '切换运行时？',
    body: (from: string, to: string) => `运行时会从 ${from} 换成 ${to}。Agent 会从新会话开始：原会话保留，但不带到新会话里；原会话里等待中的 sleep 会取消。`,
    confirm: '切换并保存',
    notIdle: 'Agent 正在回复，等这次回复结束后再切换。',
  },
  // 模型账号一节：Claude Code 不需要账号。
  account: '这个 Agent 用本机的 Claude Code 和你在本机的登录，不需要在平台配置账号，平台也不保存登录信息。',
} as const
