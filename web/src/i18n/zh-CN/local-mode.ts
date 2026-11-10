import type { components } from '@/api/schema.gen'

type S = components['schemas']

// 个人版“本机模式”：全局设置里的三个开关，以及项目设置与 Agent 配置里的本机目录。
export const localMode = {
  settings: {
    title: '本机模式',
    intro: '让 Agent 直接用这台电脑上已有的东西。三项各自开关，新装时默认全部开启。',
    effect: '“绑定本机目录”和“用本机的 CLI”在 Agent 处理下一条消息时生效，进行中的回复不受影响；“用本机的 git 身份”从下一次拉取起生效。',
    items: {
      directories: {
        label: '绑定本机目录',
        description: '项目设置和 Agent 配置里登记的本机目录会告诉 Agent，Agent 可以直接读写里面的真实文件；关闭后清单保留，但不再告诉任何 Agent。',
      },
      git: {
        label: '用本机的 git 身份',
        description: '“从 Git 拉取”时使用这台电脑上的 SSH 密钥和已保存的 git 凭证，能拉取 GitHub 以外的私有仓库；只影响拉取这一步。',
      },
      cli: {
        label: '用本机的 CLI',
        description: 'Agent 能调用这台电脑上安装的命令，但这些命令的登录状态仍用 Loop Now 里配置的账号，不沿用你在本机终端里的登录。',
      },
    } satisfies Record<keyof S['LocalModeSettings'], { label: string; description: string }>,
  },
  directories: {
    title: '本机目录',
    intro: {
      project: '告诉项目里的每个 Agent 可以去这些本机目录干活，任务里的 Agent 也一样。',
      agent: '告诉这个 Agent 可以去这些本机目录干活；项目里登记的目录对它同样有效。',
      platform: '告诉这个 Agent 可以去这些本机目录干活。',
    },
    cautionTitle: '使用前请了解',
    cautions: [
      '改动直接落在真实文件上：Agent 在这些目录里做的修改直接写进你的文件，任务里也一样，不走任务副本。',
      '这份清单是告诉 Agent 去哪里干活，不是访问隔离：不在清单里的目录，Agent 同样能读写。',
    ],
    off: '全局设置里的“绑定本机目录”已关闭：清单照常保存，但不会告诉任何 Agent。',
    openSettings: '去全局设置',
    list: '本机目录列表',
    empty: '还没有登记本机目录。',
    add: '添加目录',
    full: (max: number) => `最多登记 ${max} 个目录。`,
    edit: '编辑',
    editLabel: (path: string) => `编辑 ${path}`,
    remove: '移除',
    removeLabel: (path: string) => `移除 ${path}`,
    removeTitle: (path: string) => `从清单移除“${path}”？`,
    removeBody: '只从清单里移除，不会删除这个目录或其中的文件。',
    status: {
      missing: '目录不存在',
      not_directory: '不是目录',
      inaccessible: '无法读取',
    } satisfies Record<Exclude<S['LocalDirectory']['status'], 'ok'>, string>,
    statusHint: '这一项暂不告诉 Agent。',
    dialog: {
      addTitle: '添加本机目录',
      editTitle: '编辑本机目录',
      path: '绝对路径',
      pathHint: '这台电脑上的完整路径，例如 /Users/me/code/app，Windows 上如 C:\\Users\\me\\code\\app。目录暂不存在也能保存。',
      note: '备注（可选）',
      noteHint: '会和路径一起告诉 Agent，例如这个目录放的是什么。',
    },
    conflict: '清单刚在别处被修改，已读取最新内容；请核对后再保存一次。',
    gone: '这一项已在别处被移除。',
    inherited: {
      title: (project: string) => `来自项目“${project}”`,
      hint: '对项目里的每个 Agent 都有效，在这里只读。',
      empty: '项目还没有登记本机目录。',
      edit: '在项目设置中修改',
    },
    own: '这个 Agent 自己的目录',
  },
} as const
