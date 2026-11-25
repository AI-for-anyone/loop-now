import type { components } from '@/api/schema.gen'
import { formatBytes, formatDayClock } from './format'

type Schemas = components['schemas']

// Importing conversations from the CLIs on this computer (as T3's welcome page): each folder becomes a project, each conversation an
// Agent that continues it.
export const importing = {
  title: '导入对话',
  intro: '把这台电脑上 Claude Code 和 Codex 的对话接到 Loop Now 里继续：每个文件夹成为一个项目，每段对话成为项目里的一个 Agent。',
  firstRun: '第一次打开 Loop Now。可以先把终端里的对话接过来，也可以跳过，以后在设置里再打开这一页。',
  skip: '先跳过',
  // The original keeps working; the Agent continues a copy.
  keepRunning: '导入不会改动终端里的原会话，它照常能用；Agent 从一份副本接着说。',
  sameFolder: '别让终端里的会话和 Agent 同时在同一个文件夹里干活。',
  scanning: '正在查找对话…',
  scanProgress: (done: number, total: number) => `已读 ${done} / ${total} 个记录文件`,
  scanFailed: '查找对话没有成功',
  retry: '重新查找',
  nothing: '没有找到对话。',
  nothingRecent: '最近 30 天没有 Claude Code 或 Codex 的对话。',
  sinceLabel: '时间范围',
  since: { recent: '最近 30 天', all: '全部' },
  found: (count: number) => `找到 ${count} 个文件夹`,
  pickedInFolder: (count: number) => `选了 ${count} 段`,
  skipReasons: { too_large: '记录文件超过 4 GB', too_many_records: '记录超过 10 万条', empty: '里面没有对话', unreadable: '读不出来' } satisfies Record<NonNullable<Schemas['ImportConversation']['skipReason']>, string>,
  tiles: {
    label: '找到的对话',
    conversations: '段对话',
    byCli: (claude: number, codex: number) => `Claude Code ${claude} · Codex ${codex}`,
    folders: '个文件夹',
    suggested: (count: number) => `建议导入 ${count} 个`,
    imported: '段已导入过',
    importedNote: '不会重复导入',
  },
  groups: { git: 'Git 仓库', other: '其他文件夹' },
  otherHint: '不是 Git 仓库，默认不勾选',
  suggested: (count: number) => `建议导入 ${count} 个文件夹`,
  noneSuggested: '没有建议导入的文件夹（最近 30 天、至少 3 段对话的 Git 仓库）。',
  reviewAll: (count: number) => `查看全部 ${count} 个文件夹`,
  suggestedOnly: '只看建议的',
  selectAll: '全选',
  selectNone: '全不选',
  folder: {
    conversations: (count: number) => `${count} 段对话`,
    lastActive: (time: string) => `最近 ${formatDayClock(time)}`,
    existingProject: (name: string) => `加入已有项目「${name}」`,
    newProject: '新建项目',
    worktree: '工作树',
    truncated: '对话太多，这次最多导入 100 段、4 GB；导入后再打开这一页接着导入剩下的。',
    show: '展开对话',
    hide: '收起对话',
  },
  conversation: {
    lastActive: (time: string) => formatDayClock(time),
    messages: (count: number) => `${count} 条消息`,
    size: (bytes: number) => formatBytes(bytes),
    imported: '已导入',
    openAgent: '打开 Agent',
    skipped: (reason: string) => `不能导入：${reason}`,
    untitled: '（没有标题）',
    inFolder: (path: string) => `在 ${path}`,
  },
  cli: { claude: 'Claude Code', codex: 'Codex' },
  selected: (folders: number, conversations: number) => `已选 ${folders} 个文件夹 · ${conversations} 段对话`,
  nothingSelected: '还没有选',
  start: '导入',
  importing: (done: number, total: number) => `正在导入 ${done} / ${total}`,
  done: '导入完成',
  doneSummary: (agents: number) => `建好了 ${agents} 个 Agent`,
  failed: '导入没有完成',
  errors: (count: number) => `${count} 段没有导入成功`,
  openProject: (name: string) => `打开项目「${name}」`,
  again: '再导入一些',
  // The conversation an imported Agent continues, in its chat.
  history: {
    divider: '导入的历史：从终端的对话复制过来',
    continued: '以下是在 Loop Now 里接着说的',
  },
  // Entries: Settings, and a project (for its folder).
  entry: {
    settingsTitle: '导入对话',
    settingsHint: '把终端里 Claude Code、Codex 的对话接到 Loop Now 里继续。',
    open: '打开导入页',
    project: '导入这个项目文件夹里的对话',
  },
} as const
