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
    commands: '运行命令前问不问你，看它的批准模式；「完全放开」时不问，与 Pi 的 Agent 相同。',
    plugins: '只含 Pi 扩展的插件对它不起作用；插件的技能和 MCP 工具可以用。',
    piCommands: '没有 /pi: 命令。',
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
      byRuntime: {
        'claude-code': 'Claude Code 用本机安装的 claude 和你在本机的登录，平台不读取、不保存它的登录信息。',
        codex: 'Codex 用本机安装的 codex 和你在本机的登录（ChatGPT 账号或 API key），平台不读取、不保存它的登录信息。',
        grok: 'Grok 用本机安装的 grok 和你在本机的登录，平台不读取、不保存它的登录信息。',
        antigravity: 'Antigravity 用本机安装的 agy 和你在本机的登录，平台不读取、不保存它的登录信息。',
      } as Partial<Record<S['AgentRuntime'], string>>,
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
      codex: {
        not_found: '本机没找到 codex：装好 Codex CLI，并确认在终端里能运行 codex。',
        version_failed: '运行 codex --version 失败：确认在终端里能正常运行 codex。',
        logged_out: '本机的 Codex 没有登录：在本机终端运行 codex login，用 ChatGPT 账号或 API key 登录。',
      },
      grok: {
        not_found: '本机没找到 grok：装好 Grok CLI，并确认在终端里能运行 grok。',
        version_failed: '运行 grok --version 失败：确认在终端里能正常运行 grok。',
      },
      antigravity: {
        not_found: '本机没找到 agy：装好 Antigravity 的命令行，并确认在终端里能运行 agy。',
        version_failed: '运行 agy --version 失败：确认在终端里能正常运行 agy。',
      },
    } as Partial<Record<S['AgentRuntime'], Partial<Record<'not_found' | 'version_failed' | 'unavailable' | 'logged_out', string>>>>,
    // A CLI older than the runtime supports: the version it needs and the one on this computer, when both are known.
    versionUnsupported: (name: string, needed: string | null, actual: string | null) =>
      needed && actual ? `本机的 ${name} 是 ${actual} 版，需要 ${needed} 或更新的版本：更新后再检查一次。` : `本机的 ${name} 版本太旧，Loop Now 用不了：更新后再检查一次。`,
    // How a CLI is logged in (RuntimeLogin.authMethod), in words; other methods show as the CLI reports them.
    authMethods: { chatgpt: 'ChatGPT 账号', api_key: 'API key' } as Record<string, string>,
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
    problem: { not_found: '没装', version_failed: '运行不了', version_unsupported: '版本太旧', unavailable: '用不了', logged_out: '没登录' },
    // The stamp of a command a person added whose program isn't found.
    addedNotFound: '找不到',
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
    // A model that thinks at a level of its own (Antigravity's models carry it in their id).
    fixedThinking: '这个模型的思考等级是固定的，已含在模型里',
    // A local ACP command a person added: its one model is the program's own default.
    programThinking: '思考等级由这个程序自己决定',
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
    // Independent inline composer menus (Harness, Model, Thinking, Permissions).
    menus: {
      harnessTitle: 'Harness',
      modelTitle: '模型',
      thinkingTitle: '思考等级',
      permissionsTitle: '权限模式',
      harnessLabel: (name: string) => `Harness：${name}。点开更换`,
      modelLabel: (name: string) => `模型：${name}。点开更换`,
      thinkingLabel: (level: string) => `思考等级：${level}。点开更换`,
      permissionsLabel: (mode: string) => `权限模式：${mode}。点开更换`,
      shortPermission: {
        supervised: '每次确认',
        accept_edits: '自动改码',
        auto: '自动判断',
        full: '完全放开',
      },
    },
    // A runtime that is not ready: the command for this computer from the server (RuntimeDefinition.setup), then check again.
    setup: {
      title: (name: string) => `先把 ${name} 准备好`,
      install: '在本机终端运行，装好它',
      login: '在本机终端运行，按提示登录',
      update: '在本机终端运行，更新到新版本',
      reinstall: '重新装一次通常就好了：',
      copy: '复制',
      copied: '已复制',
      docs: '官方的安装和登录说明',
      recheck: '再检查一次',
      rechecking: '正在检查…',
      locked: (name: string) => `准备好之前不能选 ${name}。`,
      // A local ACP command a person added: where to change it.
      editCommand: '在全局设置里修改这条命令',
    },
    // Headings of the two provider groups when the catalog says which ones the Agent can use now.
    usable: { true: '现在就能用', false: '要先加账号或 key' },
    managed: '用平台保存的模型账号或 API key 运行。',
    // The foot line for a local ACP command a person added.
    addedNote: (name: string) => `${name} 是你在全局设置里添加的 ACP 命令，用它自己在本机的设置和登录。`,
  },
  // Local ACP commands a person added (global settings, under the runtimes): any program on this computer that speaks the Agent Client
  // Protocol becomes a runtime of its own.
  commands: {
    title: '自己添加的 ACP 命令',
    intro: '本机上能用 Agent Client Protocol（ACP）对话的程序，都能加成一个运行时，在新建 Agent 和配置页的“谁来跑”里选它。',
    add: '添加命令',
    empty: '还没有添加命令。',
    edit: '修改',
    remove: '删除',
    command: '命令',
    args: '参数',
    noArgs: '没有参数',
    env: '环境变量',
    saved: '已保存',
    path: '路径',
    found: '找得到',
    notFound: '找不到',
    // GET /runtimes reports only whether the executable is found; the version and the login can't be told.
    unknownState: '版本和登录状态看不到：程序用它自己在本机的设置和登录。',
    notFoundNote: (command: string) => `本机找不到 ${command}：确认命令写对了，并且在终端里能运行；也可以填它的绝对路径。`,
    checkedAt: (time: string) => `检查于 ${time}`,
    dialog: {
      addTitle: '添加 ACP 命令',
      editTitle: (name: string) => `修改“${name}”`,
      trust: '这条命令会以服务端用户的身份在这台电脑上运行，只添加你信任的程序。',
      name: '名字',
      nameHint: '在“谁来跑”里显示的名字。',
      command: '命令',
      commandHint: '可执行文件：登录 shell 的 PATH 上的名字，或绝对路径。',
      args: '参数',
      argsHint: '一行一个，照原样传给程序，不经过 shell：引号和空格都算参数的一部分。空行不算。',
      env: '环境变量',
      envHint: '比如程序要用的 API key。值只写不读：保存后只显示名字。',
      envName: '变量名',
      envValue: (name: string) => (name ? `${name} 的值` : '值'),
      savedValue: '已保存，不显示',
      refill: '重填',
      keep: '不改了',
      addEnv: '添加变量',
      removeEnv: (name: string) => (name ? `去掉 ${name}` : '去掉这一行'),
      submitAdd: '添加',
      submitEdit: '保存',
      saving: '正在保存…',
      required: { name: '填上名字', command: '填上命令' },
      envProblems: { invalid: '变量名只能用字母、数字和 _，不能以数字开头', taken: '这个变量名重复了', value: '填上值' },
    },
    deleteDialog: {
      title: (name: string) => `删除命令“${name}”？`,
      body: '它的运行时会从“谁来跑”里消失。程序自己存下的会话不会动。',
      confirm: '删除',
      inUse: (name: string, count: number) => `还有 ${count} 个 Agent 在用 ${name}，现在删不了。先在它们的配置页把“谁来跑”换成别的运行时，再删除：`,
      platform: '平台',
    },
  },
  // Model account section of an Agent whose runtime uses the login on this computer: it needs no account.
  account: (name: string) => `这个 Agent 用本机的 ${name} 和你在本机的登录，不需要在平台配置账号，平台也不保存登录信息。`,
} as const
