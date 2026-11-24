import type { components } from '@/api/schema.gen'

type S = components['schemas']

// An Agent's runtime (decided by the model provider). Names and capabilities come from GET /runtime-definitions (lib/runtime.ts); the
// text here is built from a runtime's name and capabilities, except where a runtime has wording of its own, keyed by its id.
export const runtime = {
  // Badge in the sidebar and the Agent header, only for runtimes installed on this computer (not bundled); the badge text is the name.
  badgeTitle: (name: string) => `运行时：本机的 ${name}`,
  // The note on a runtime that uses the login on this computer (the runner picker's foot line).
  providerNote: (name: string) => `用本机安装的 ${name} 和你在本机的登录，平台不保存登录信息。`,
  // How a runtime differs from the bundled Pi, item by item from its capabilities, when it is selected.
  differences: {
    title: (name: string) => `用 ${name} 运行时`,
    home: '用你本机的家目录和登录运行：它执行 gh、git 等命令时用的是你本人的登录。',
    commands: '不经确认直接执行命令，与 Pi 的 Agent 相同。',
    plugins: '只含 Pi 扩展的插件对它不起作用；插件的技能和 MCP 工具可以用。',
    piCommands: '没有 /pi: 命令。',
  },
  // A change of runtime while the Agent is replying.
  switch: {
    notIdle: 'Agent 正在回复，等这次回复结束后再切换。',
  },
  // Runtime status in global settings (GET /runtimes); the problems also explain a runtime in the runner picker's setup panel.
  status: {
    title: 'Agent 运行时',
    // One sentence per runtime, in the order of the definitions: the runtime's own sentence when it has one, otherwise by
    // capabilities (bundled; installed here with the login here; installed here).
    intro: {
      bundled: (name: string) => `${name} 随平台安装。`,
      local: (name: string) => `${name} 用本机的安装和你在本机的登录，平台不读取、不保存它的登录信息。`,
      installed: (name: string) => `${name} 用本机的安装。`,
      byRuntime: { 'claude-code': 'Claude Code 用本机安装的 claude 和你在本机的登录，平台不读取、不保存它的登录信息。' } as Partial<Record<S['AgentRuntime'], string>>,
      separator: '',
    },
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
    // Why it can't be used and how to fix it.
    // problemsByRuntime holds a runtime's own wording (its command, how to log in), problems the wording for any runtime.
    problems: {
      not_found: (name: string) => `本机没找到 ${name}：装好它，并确认在终端里能运行。`,
      version_failed: (name: string) => `读取 ${name} 的版本失败：确认在终端里能正常运行它。`,
      unavailable: (name: string) => `本机的 ${name} 现在不可用。`,
      logged_out: (name: string) => `本机的 ${name} 没有登录：在本机终端里登录它。`,
    },
    problemsByRuntime: {
      'claude-code': {
        not_found: '本机没找到 claude：装好 Claude Code，并确认在终端里能运行 claude。',
        version_failed: '运行 claude --version 失败：确认在终端里能正常运行 claude。',
        logged_out: '本机的 Claude Code 没有登录：在本机终端运行 claude 并登录。',
      },
    } as Partial<Record<S['AgentRuntime'], Partial<Record<'not_found' | 'version_failed' | 'unavailable' | 'logged_out', string>>>>,
    unknownLogin: '判断不了登录状态；Agent 运行失败时会如实报错。',
  },
  // The runner picker (new Agent, the config page, and the popover from the message box): runtimes on the left, the chosen runtime's
  // models and the thinking level on the right.
  picker: {
    label: '谁来跑',
    rail: '运行时',
    ready: '能用',
    checking: '正在检查…',
    unknown: '状态未知',
    // Short stamps on a runtime that cannot run here; the full reason is in the setup panel.
    problem: { not_found: '没装', version_failed: '运行不了', unavailable: '用不了', logged_out: '没登录' },
    bundled: '内置',
    bundledNote: '用 API key 或其他订阅',
    current: '当前',
    search: '搜索模型',
    searchGrouped: '搜索提供方或模型',
    models: '模型',
    empty: '没有匹配的模型',
    loading: '正在读取模型…',
    more: (count: number) => `还有 ${count} 个匹配的模型，再多输几个字缩小范围`,
    modelCount: (count: number) => `${count} 个模型`,
    matchCount: (found: number, count: number) => `${found} / ${count} 个模型`,
    inUse: '现在用的是',
    notInCatalog: (ref: string) => `现在用的 ${ref} 不在模型目录里`,
    noThinking: '这个模型不分思考等级',
    noModel: '先选模型',
    // The chip on the message box that says who runs the Agent now and opens the picker; a choice is saved at once.
    chip: {
      label: (summary: string) => `谁来跑：${summary}。点开更换`,
      detail: (runtime: string, where: string, provider: string, model: string, level: string | null) =>
        `由 ${runtime}（${where}）运行 · 提供方 ${provider} · 模型 ${model}${level ? ` · 思考等级 ${level}` : ''}`,
      local: '本机',
      bundled: '内置',
      title: '谁来跑',
      hint: '选了立刻保存，从下一条消息起生效',
      saving: '正在保存…',
      saved: '已换好，下一条消息起生效',
    },
    // A runtime that is not ready: the command for this computer from the server (RuntimeDefinition.setup), then check again.
    setup: {
      title: (name: string) => `先把 ${name} 准备好`,
      install: '在本机终端运行，装好它',
      login: '在本机终端运行，按提示登录',
      reinstall: '重新装一次通常就好了：',
      copy: '复制',
      copied: '已复制',
      docs: '官方的安装和登录说明',
      recheck: '再检查一次',
      rechecking: '正在检查…',
      locked: (name: string) => `准备好之前不能选 ${name}。`,
    },
    // Headings of the two provider groups when the catalog says which ones the Agent can use now.
    usable: { true: '现在就能用', false: '要先加账号或 key' },
    managed: '用平台保存的模型账号或 API key 运行。',
  },
  // Model account section of an Agent whose runtime uses the login on this computer: it needs no account.
  account: (name: string) => `这个 Agent 用本机的 ${name} 和你在本机的登录，不需要在平台配置账号，平台也不保存登录信息。`,
} as const
