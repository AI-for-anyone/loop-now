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
  // 全局设置里的运行时状态（GET /runtimes），以及模型选择里的提醒。
  status: {
    title: 'Agent 运行时',
    intro: 'Pi 随平台安装。Claude Code 用本机安装的 claude 和你在本机的登录，平台不读取、不保存它的登录信息。',
    refresh: '重新检查',
    refreshing: '正在检查…',
    available: '可用',
    unavailable: '不可用',
    login: { logged_in: '已登录', logged_out: '未登录', unknown: '登录状态未知' } satisfies Record<S['RuntimeLogin']['state'], string>,
    version: '版本',
    path: '路径',
    account: '登录',
    subscription: (type: string) => `订阅 ${type}`,
    checkedAt: (time: string) => `检查于 ${time}`,
    // 用不了的原因与处理办法；模型选择里接 pickerWarning 的后半句。
    problems: {
      not_found: '本机没找到 claude：装好 Claude Code，并确认在终端里能运行 claude。',
      version_failed: '运行 claude --version 失败：确认在终端里能正常运行 claude。',
      unavailable: '本机的 Claude Code 现在不可用。',
      logged_out: '本机的 Claude Code 没有登录：在本机终端运行 claude 并登录。',
    },
    unknownLogin: '判断不了登录状态；Agent 运行失败时会如实报错。',
    pickerWarning: (problem: string) => `${problem}处理好之前，用它的 Agent 运行会失败。`,
  },
  // 模型账号一节：Claude Code 不需要账号。
  account: '这个 Agent 用本机的 Claude Code 和你在本机的登录，不需要在平台配置账号，平台也不保存登录信息。',
} as const
